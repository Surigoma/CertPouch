// Package config reads and validates operator supplied configuration.
package config

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/Surigoma/CertPouch/internal/distribution"
	"github.com/Surigoma/CertPouch/internal/dns"
	"github.com/Surigoma/CertPouch/internal/pki"
)

type Server struct {
	InventoryCertificates       bool
	Listen                      string
	TLSCertificate              string
	TLSPrivateKey               string
	TLSClientCAs                *x509.CertPool
	CertificateDir              string
	Policies                    map[string]pki.Policy
	Clients                     []distribution.Client
	PublicationRefresh          time.Duration
	InventoryRefresh            time.Duration
	ReloadInventoryClients      func() ([]distribution.Client, error)
	ReloadInventoryPublications func() (map[string]pki.Policy, []distribution.Client, error)
	RevocationFile              string
	RevocationRefresh           time.Duration
}

// serverDocument is the internal form of server.yaml. Only decodeServer fills
// it, from the v2 layout described by workflowDocument.
type serverDocument struct {
	Deployment *DNSDeployment
	Server     struct {
		Auth              string
		Listen            string
		Refresh           string
		RevocationFile    string
		RevocationRefresh string
		TLS               struct {
			Certificate string
			PrivateKey  string
			ClientCA    string
		}
	}
	Storage struct {
		CertificateDir string
		TrustCAFile    string
		IssuerFile     string
	}
	DNSInventory *inventoryDocument
	Sheets       googleSheetsDocument
	Zones        dnsZoneDocument
	Renewal      Renewal
	Certificates []certificateDocument
	Clients      []clientDocument
}

type certificateDocument struct {
	Name       string   `yaml:"name"`
	DNS        []string `yaml:"expected_dns_sans"`
	IPs        []string `yaml:"expected_ip_sans"`
	IssuerFile string   `yaml:"issuer_certificate"`
}

type clientDocument struct {
	ID                      string                `yaml:"id"`
	Auth                    distribution.AuthMode `yaml:"auth"`
	Enabled                 bool                  `yaml:"enabled"`
	AllowedIPs              []string              `yaml:"allowed_ips"`
	AllowedCertificates     []string              `yaml:"allowed_certificates"`
	TokenHashFile           string                `yaml:"token_hash_file"`
	Overrides               map[string][]string   `yaml:"certificate_access_overrides"`
	InventoryAccess         string                `yaml:"inventory_access"`
	ClientCertificateSHA256 []string              `yaml:"client_certificate_sha256"`
}

func LoadServer(path string) (Server, error) {
	doc, err := readServerDocument(path)
	if err != nil {
		return Server{}, err
	}
	return loadServerDocument(doc)
}

// PublicationLocations reads only the paths and names needed for local expiry monitoring.
func PublicationLocations(path string) (string, []string, error) {
	doc, err := readServerDocument(path)
	if err != nil {
		return "", nil, err
	}
	if !filepath.IsAbs(doc.Storage.CertificateDir) {
		return "", nil, fmt.Errorf("certificates.store_dir must be an absolute path")
	}
	if doc.Renewal.FromInventory {
		inventory, err := doc.loadIssuanceInventory()
		if err != nil {
			return "", nil, err
		}
		return doc.Storage.CertificateDir, inventory.Names(), nil
	}
	var names []string
	for _, certificate := range doc.Certificates {
		names = append(names, certificate.Name)
	}
	return doc.Storage.CertificateDir, names, nil
}

func (doc serverDocument) loadIssuanceInventory() (dns.Inventory, error) {
	if doc.DNSInventory == nil {
		return dns.Inventory{}, errIssuanceWithoutInventory
	}
	return doc.DNSInventory.load()
}

func readServerDocument(path string) (serverDocument, error) {
	body, err := readFile(path, false, 1<<20)
	if err != nil {
		return serverDocument{}, fmt.Errorf("read server config: %w", err)
	}
	return decodeServer(body)
}

var errIssuanceWithoutInventory = fmt.Errorf("certificates.issuer without certificates.targets issues a certificate for every inventory name and requires inventory.cache_file or inventory.csv_files")

func loadServerDocument(doc serverDocument) (Server, error) {
	defaultAuth := distribution.AuthMode(doc.Server.Auth)
	if !defaultAuth.Valid() {
		return Server{}, fmt.Errorf("distribution.auth must be token, mtls, or ip")
	}
	if defaultAuth == distribution.AuthMTLS && doc.Server.TLS.ClientCA == "" {
		return Server{}, fmt.Errorf("distribution.auth mtls requires distribution.tls.client_ca")
	}
	if defaultAuth == distribution.AuthIP && doc.Server.TLS.ClientCA != "" {
		return Server{}, fmt.Errorf("distribution.auth ip is incompatible with distribution.tls.client_ca")
	}
	_, port, err := net.SplitHostPort(doc.Server.Listen)
	portNumber, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || portErr != nil || portNumber == 0 {
		return Server{}, fmt.Errorf("distribution.listen must contain a valid host and port")
	}
	for _, field := range []struct{ key, path string }{
		{"certificates.store_dir", doc.Storage.CertificateDir},
		{"certificates.trust_ca_file", doc.Storage.TrustCAFile},
		{"distribution.tls.certificate_file", doc.Server.TLS.Certificate},
		{"distribution.tls.private_key_file", doc.Server.TLS.PrivateKey},
	} {
		if !filepath.IsAbs(field.path) {
			return Server{}, fmt.Errorf("%s must be an absolute path", field.key)
		}
	}
	if _, err := checkedFile(doc.Server.TLS.PrivateKey, true); err != nil {
		return Server{}, fmt.Errorf("check distribution.tls.private_key_file %s: %w", doc.Server.TLS.PrivateKey, err)
	}
	rootsPEM, err := readFile(doc.Storage.TrustCAFile, false, 1<<20)
	if err != nil {
		return Server{}, fmt.Errorf("read certificates.trust_ca_file %s: %w", doc.Storage.TrustCAFile, err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootsPEM) {
		return Server{}, fmt.Errorf("certificates.trust_ca_file contains no certificates")
	}
	result := Server{
		Listen: doc.Server.Listen, TLSCertificate: doc.Server.TLS.Certificate,
		TLSPrivateKey: doc.Server.TLS.PrivateKey, CertificateDir: doc.Storage.CertificateDir,
		Policies: make(map[string]pki.Policy),
	}
	if path := doc.Server.TLS.ClientCA; path != "" {
		if !filepath.IsAbs(path) {
			return Server{}, fmt.Errorf("distribution.tls.client_ca must be an absolute path")
		}
		result.TLSClientCAs, err = loadClientRoots(path)
		if err != nil {
			return Server{}, fmt.Errorf("load distribution.tls.client_ca %s: %w", path, err)
		}
	}
	if value := doc.Server.Refresh; value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval < time.Second {
			return Server{}, fmt.Errorf("distribution.refresh_interval must be a duration of at least one second")
		}
		result.PublicationRefresh = interval
		if doc.DNSInventory != nil {
			result.InventoryRefresh = interval
		}
	}
	if path := doc.Server.RevocationFile; path != "" {
		if !filepath.IsAbs(path) {
			return Server{}, fmt.Errorf("distribution.revocation_file must be an absolute path")
		}
		result.RevocationFile = path
		result.RevocationRefresh = 5 * time.Second
	} else if doc.Server.RevocationRefresh != "" {
		return Server{}, fmt.Errorf("distribution.revocation_refresh requires distribution.revocation_file")
	}
	if value := doc.Server.RevocationRefresh; value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval < time.Second {
			return Server{}, fmt.Errorf("distribution.revocation_refresh must be a duration of at least one second")
		}
		result.RevocationRefresh = interval
	}
	for i, cert := range doc.Certificates {
		if _, exists := result.Policies[cert.Name]; cert.Name == "" || exists {
			return Server{}, fmt.Errorf("missing or duplicate certificates.targets[%d].name", i)
		}
		policy, err := cert.policy(roots, doc.Storage.IssuerFile)
		if err != nil {
			return Server{}, fmt.Errorf("certificates.targets[%d]: %w", i, err)
		}
		result.Policies[cert.Name] = policy
	}
	var inventory *dns.Inventory
	var inventoryExpires time.Time
	var inventoryPolicy pki.Policy
	if source := doc.DNSInventory; source != nil {
		parsed, err := source.loadWithExpiry(&inventoryExpires)
		if err != nil {
			return Server{}, err
		}
		inventory = &parsed
	}
	if doc.Renewal.FromInventory {
		if inventory == nil {
			return Server{}, errIssuanceWithoutInventory
		}
		issuer, err := (certificateDocument{Name: "issuer.example.invalid", IssuerFile: doc.Renewal.CertificateFile}).policy(roots, "")
		if err != nil {
			return Server{}, fmt.Errorf("certificates.issuer.certificate_file: %w", err)
		}
		inventoryPolicy = pki.Policy{Roots: roots, Issuer: issuer.Issuer}
		result.Policies = inventoryPolicies(*inventory, inventoryPolicy)
		result.InventoryCertificates = true
	}
	clientCertOwners := make(map[[sha256.Size]byte]string)
	for i, entry := range doc.Clients {
		if entry.Auth == "" {
			entry.Auth = defaultAuth
		}
		if entry.Auth == distribution.AuthMTLS && result.TLSClientCAs == nil {
			return Server{}, fmt.Errorf("distribution.clients[%d].auth mtls requires distribution.tls.client_ca", i)
		}
		if entry.Auth == distribution.AuthIP && result.TLSClientCAs != nil {
			return Server{}, fmt.Errorf("distribution.clients[%d].auth ip is incompatible with distribution.tls.client_ca", i)
		}
		client, err := entry.client(result.Policies, inventory, result.InventoryCertificates)
		if err != nil {
			return Server{}, fmt.Errorf("distribution.clients[%d]: %w", i, err)
		}
		client.ClientCertificateHashes, err = entry.certificateHashes(result.TLSClientCAs != nil)
		if err != nil {
			return Server{}, fmt.Errorf("distribution.clients[%d]: %w", i, err)
		}
		if err := client.ValidateAuthentication(); err != nil {
			return Server{}, fmt.Errorf("distribution.clients[%d]: %w", i, err)
		}
		if client.Enabled {
			for _, fingerprint := range client.ClientCertificateHashes {
				if owner, exists := clientCertOwners[fingerprint]; exists && owner != client.ID {
					return Server{}, fmt.Errorf("distribution.clients[%d]: client certificate fingerprint is assigned to multiple enabled clients", i)
				}
				clientCertOwners[fingerprint] = client.ID
			}
		}
		client.InventoryExpiresAt = inventoryExpires
		result.Clients = append(result.Clients, client)
	}
	if result.InventoryCertificates {
		source, entries, baseline := *doc.DNSInventory, doc.Clients, result.Clients
		result.ReloadInventoryPublications = func() (map[string]pki.Policy, []distribution.Client, error) {
			var expires time.Time
			inventory, err := source.loadWithExpiry(&expires)
			if err != nil {
				return nil, nil, err
			}
			policies := inventoryPolicies(inventory, inventoryPolicy)
			clients, err := refreshInventoryClients(entries, baseline, policies, inventory, expires, true)
			return policies, clients, err
		}
	} else if result.InventoryRefresh != 0 {
		result.ReloadInventoryClients = inventoryClientLoader(*doc.DNSInventory, doc.Clients, result.Clients, result.Policies)
	}
	return result, nil
}

func (c clientDocument) certificateHashes(mtls bool) ([][sha256.Size]byte, error) {
	if !mtls && len(c.ClientCertificateSHA256) != 0 {
		return nil, fmt.Errorf("client_certificate_sha256 requires distribution.tls.client_ca")
	}
	if mtls && c.Enabled && len(c.ClientCertificateSHA256) == 0 {
		return nil, fmt.Errorf("mTLS requires an allowed client certificate fingerprint")
	}
	return parseCertificateHashes(c.ClientCertificateSHA256)
}

func parseCertificateHashes(values []string) ([][sha256.Size]byte, error) {
	var fingerprints [][sha256.Size]byte
	seen := make(map[[sha256.Size]byte]bool)
	for _, text := range values {
		decoded, err := hex.DecodeString(text)
		if err != nil || len(decoded) != sha256.Size || text != strings.ToLower(text) {
			return nil, fmt.Errorf("expected a lowercase SHA-256 client certificate fingerprint")
		}
		fingerprint := [sha256.Size]byte(decoded)
		if seen[fingerprint] {
			return nil, fmt.Errorf("duplicate client certificate fingerprint")
		}
		seen[fingerprint] = true
		fingerprints = append(fingerprints, fingerprint)
	}
	return fingerprints, nil
}

func decodeYAML(body []byte, target any) error {
	dec := yaml.NewDecoder(bytes.NewReader(body), yaml.Strict())
	if err := dec.Decode(target); err != nil {
		return describeYAMLError(err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("YAML must contain exactly one document")
	}
	return nil
}

// describeYAMLError reports where decoding failed without echoing values.
// Parser diagnostics can quote input lines, so only messages that name keys
// or node types are passed through.
func describeYAMLError(err error) error {
	var yamlErr yaml.Error
	if !errors.As(err, &yamlErr) || yamlErr.GetToken() == nil || yamlErr.GetToken().Position == nil {
		return fmt.Errorf("invalid YAML: check syntax, field names, types and duplicate keys")
	}
	position := yamlErr.GetToken().Position
	at := fmt.Sprintf("line %d, column %d", position.Line, position.Column)
	var unknown *yaml.UnknownFieldError
	var duplicate *yaml.DuplicateKeyError
	var syntax *yaml.SyntaxError
	var typeErr *yaml.TypeError
	var nodeErr *yaml.UnexpectedNodeTypeError
	switch {
	case errors.As(err, &unknown):
		return fmt.Errorf("invalid YAML at %s: %s", at, unknown.GetMessage())
	case errors.As(err, &duplicate):
		return fmt.Errorf("invalid YAML at %s: %s", at, duplicate.GetMessage())
	case errors.As(err, &syntax) && strings.HasPrefix(syntax.GetMessage(), `mapping key "`):
		key, _, _ := strings.Cut(strings.TrimPrefix(syntax.GetMessage(), `mapping key "`), `"`)
		return fmt.Errorf("invalid YAML at %s: duplicate key %q", at, key)
	case errors.As(err, &typeErr), errors.As(err, &nodeErr):
		return fmt.Errorf("invalid YAML at %s: value has the wrong type", at)
	}
	return fmt.Errorf("invalid YAML syntax at %s", at)
}

func (c certificateDocument) policy(roots *x509.CertPool, defaultIssuer string) (pki.Policy, error) {
	if c.IssuerFile == "" {
		c.IssuerFile = defaultIssuer
	}
	if c.DNS == nil && c.IPs == nil && isCertificateHostname(c.Name) {
		c.DNS = []string{c.Name}
	}
	if !filepath.IsAbs(c.IssuerFile) || len(c.DNS)+len(c.IPs) == 0 {
		return pki.Policy{}, fmt.Errorf("issuer path and expected SANs are required")
	}
	body, err := readFile(c.IssuerFile, false, 1<<20)
	if err != nil {
		return pki.Policy{}, fmt.Errorf("read issuer certificate: %w", err)
	}
	block, rest := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return pki.Policy{}, fmt.Errorf("expected one issuer PEM certificate")
	}
	issuer, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !issuer.IsCA {
		return pki.Policy{}, fmt.Errorf("invalid issuer CA certificate")
	}
	policy := pki.Policy{Roots: roots, Issuer: issuer, DNS: c.DNS}
	for _, name := range c.DNS {
		if name == "" || strings.ContainsAny(name, "* \t\r\n") {
			return pki.Policy{}, fmt.Errorf("invalid expected DNS SAN")
		}
	}
	for _, value := range c.IPs {
		addr, err := netip.ParseAddr(value)
		if err != nil || addr.Zone() != "" {
			return pki.Policy{}, fmt.Errorf("invalid expected IP SAN")
		}
		policy.IPs = append(policy.IPs, addr.Unmap())
	}
	return policy, nil
}

func isCertificateHostname(name string) bool {
	if len(name) > 253 || !strings.Contains(name, ".") || net.ParseIP(name) != nil {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range strings.ToLower(label) {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}

func inventoryPolicies(inventory dns.Inventory, policy pki.Policy) map[string]pki.Policy {
	names := inventory.Names()
	policies := make(map[string]pki.Policy, len(names))
	for _, name := range names {
		policy.DNS = []string{name}
		policies[name] = policy
	}
	return policies
}

func (c clientDocument) client(policies map[string]pki.Policy, inventory *dns.Inventory, dynamic bool) (distribution.Client, error) {
	client := distribution.Client{ID: c.ID, Enabled: c.Enabled, Auth: c.Auth, AllowedCertificates: c.AllowedCertificates}
	if !c.Auth.Valid() {
		return client, fmt.Errorf("auth must be token, mtls, or ip")
	}
	if inventory != nil {
		if len(c.AllowedIPs) != 0 {
			return client, fmt.Errorf("allowed_ips is incompatible with DNS inventory mode")
		}
		if c.Auth == distribution.AuthIP && (c.InventoryAccess == "names" || c.InventoryAccess == "all") {
			return client, fmt.Errorf("auth ip requires inventory_access ip or ip_and_names")
		}
		rules, err := c.inventoryRules(policies, *inventory, dynamic)
		if err != nil {
			return client, err
		}
		client.CertificateIPs = rules
		client.AllowedCertificates = nil
	} else if len(c.Overrides) != 0 || c.InventoryAccess != "" {
		return client, fmt.Errorf("certificate_access_overrides and inventory_access require inventory.cache_file or inventory.csv_files")
	}
	for _, value := range c.AllowedIPs {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			addr, addrErr := netip.ParseAddr(value)
			if addrErr != nil || addr.Zone() != "" {
				return client, fmt.Errorf("invalid allowed IP or CIDR")
			}
			addr = addr.Unmap()
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		client.AllowedIPs = append(client.AllowedIPs, prefix.Masked())
	}
	for _, name := range c.AllowedCertificates {
		if _, ok := policies[name]; !ok && inventory == nil {
			return client, fmt.Errorf("certificate assignment references an unconfigured certificate")
		}
	}
	if !c.Auth.UsesToken() {
		if c.TokenHashFile != "" {
			return client, fmt.Errorf("token_hash_file is incompatible with auth %s", c.Auth)
		}
		return client, nil
	}
	if !filepath.IsAbs(c.TokenHashFile) {
		return client, fmt.Errorf("token_hash_file must be an absolute path")
	}
	body, err := readFile(c.TokenHashFile, true, sha256.Size*2+2)
	if err != nil {
		return client, fmt.Errorf("read token hash file: %w", err)
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(body), "\n"), "\r")
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
		return client, fmt.Errorf("expected a lowercase SHA-256 token hash")
	}
	copy(client.TokenHash[:], decoded)
	return client, nil
}

func (c clientDocument) inventoryRules(policies map[string]pki.Policy, inventory dns.Inventory, dynamic bool) (map[string][]netip.Prefix, error) {
	mode := c.InventoryAccess
	if mode == "" {
		mode = "ip"
	}
	switch mode {
	case "ip", "names", "ip_and_names", "all":
	default:
		return nil, fmt.Errorf("invalid inventory_access mode")
	}
	restrictNames := mode == "names" || mode == "ip_and_names"
	if restrictNames != (len(c.AllowedCertificates) != 0) {
		return nil, fmt.Errorf("allowed_certificates is required only for names and ip_and_names modes")
	}
	if len(c.Overrides) != 0 && mode != "ip" && mode != "ip_and_names" {
		return nil, fmt.Errorf("certificate_access_overrides requires IP matching")
	}
	allowed := make(map[string]bool, len(c.AllowedCertificates))
	for _, name := range c.AllowedCertificates {
		if allowed[name] || !isCertificateHostname(name) {
			return nil, fmt.Errorf("invalid or duplicate inventory certificate assignment")
		}
		allowed[name] = true
	}
	rules := make(map[string][]netip.Prefix, len(policies))
	for name := range policies {
		if restrictNames && !allowed[name] {
			continue
		}
		prefixes := inventory.PrefixesForName(name)
		if len(prefixes) != 0 && (mode == "all" || mode == "names") {
			prefixes = []netip.Prefix{netip.PrefixFrom(netip.IPv4Unspecified(), 0), netip.PrefixFrom(netip.IPv6Unspecified(), 0)}
		}
		rules[name] = prefixes
	}
	for name, hosts := range c.Overrides {
		_, exists := policies[name]
		if !exists && !dynamic {
			return nil, fmt.Errorf("certificate access override references an unconfigured certificate")
		}
		if restrictNames && !allowed[name] {
			return nil, fmt.Errorf("certificate access override is outside allowed_certificates")
		}
		var prefixes []netip.Prefix
		var err error
		if dynamic {
			if !isCertificateHostname(name) {
				return nil, fmt.Errorf("invalid inventory certificate access override")
			}
			prefixes, err = inventory.PresentPrefixes(hosts)
		} else {
			prefixes, err = inventory.AllowedPrefixes(hosts)
		}
		if err != nil {
			return nil, fmt.Errorf("certificate access override: %w", err)
		}
		if exists {
			rules[name] = append(rules[name], prefixes...)
		}
	}
	return rules, nil
}

func inventoryClientLoader(source inventoryDocument, entries []clientDocument, initial []distribution.Client, policies map[string]pki.Policy) func() ([]distribution.Client, error) {
	return func() ([]distribution.Client, error) {
		var expires time.Time
		inventory, err := source.loadWithExpiry(&expires)
		if err != nil {
			return nil, err
		}
		return refreshInventoryClients(entries, initial, policies, inventory, expires, false)
	}
}

func refreshInventoryClients(entries []clientDocument, initial []distribution.Client, policies map[string]pki.Policy, inventory dns.Inventory, expires time.Time, dynamic bool) ([]distribution.Client, error) {
	clients := make([]distribution.Client, len(initial))
	for i, client := range initial {
		rules, err := entries[i].inventoryRules(policies, inventory, dynamic)
		if err != nil {
			return nil, fmt.Errorf("derive inventory rules for clients[%d]: %w", i, err)
		}
		client.InventoryExpiresAt = expires
		client.CertificateIPs = rules
		clients[i] = client
	}
	return clients, nil
}

func checkedFile(path string, secret bool) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("expected a regular file")
	}
	if runtime.GOOS != "windows" {
		mask := os.FileMode(0022)
		if secret {
			mask = 0077
		}
		if info.Mode().Perm()&mask != 0 {
			return nil, fmt.Errorf("file permissions are too broad")
		}
	}
	return info, nil
}

func readFile(path string, secret bool, limit int64) ([]byte, error) {
	info, err := checkedFile(path, secret)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("file changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("file exceeds size limit")
	}
	return body, nil
}

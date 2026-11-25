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
	InventoryCertificates  bool
	Listen                 string
	TLSCertificate         string
	TLSPrivateKey          string
	TLSClientCAs           *x509.CertPool
	CertificateDir         string
	Policies               map[string]pki.Policy
	Clients                []distribution.Client
	PublicationRefresh     time.Duration
	InventoryRefresh       time.Duration
	ReloadInventoryClients func() ([]distribution.Client, error)
	RevocationFile         string
	RevocationRefresh      time.Duration
}

type serverDocument struct {
	Deployment *DNSDeployment `yaml:"-"`
	Server     struct {
		Listen             string `yaml:"listen"`
		PublicationRefresh string `yaml:"publication_refresh"`
		InventoryRefresh   string `yaml:"inventory_refresh"`
		RevocationFile     string `yaml:"revocation_file"`
		RevocationRefresh  string `yaml:"revocation_refresh"`
		TLS                struct {
			Certificate string `yaml:"certificate"`
			PrivateKey  string `yaml:"private_key"`
			ClientCA    string `yaml:"client_ca"`
		} `yaml:"tls"`
	} `yaml:"server"`
	Storage struct {
		CertificateDir string `yaml:"certificate_dir"`
		TrustCAFile    string `yaml:"trust_ca_file"`
		IssuerFile     string `yaml:"issuer_certificate"`
	} `yaml:"storage"`
	DNSInventory *inventoryDocument   `yaml:"dns_inventory"`
	Sheets       googleSheetsDocument `yaml:"google_sheets"`
	Zones        dnsZoneDocument      `yaml:"zones"`
	Renewal      Renewal              `yaml:"renewal"`
	Pipeline     *struct {
		DNS struct {
			Deployment DNSDeployment        `yaml:"deployment"`
			Inventory  *inventoryDocument   `yaml:"inventory"`
			Sheets     googleSheetsDocument `yaml:"sheets"`
			Zones      dnsZoneDocument      `yaml:"zones"`
		} `yaml:"dns"`
		Certificates struct {
			Renewal Renewal `yaml:"renewal"`
		} `yaml:"certificates"`
	} `yaml:"pipeline"`
	Certificates []certificateDocument `yaml:"certificates"`
	Clients      []clientDocument      `yaml:"clients"`
}

type certificateDocument struct {
	Name       string   `yaml:"name"`
	DNS        []string `yaml:"expected_dns_sans"`
	IPs        []string `yaml:"expected_ip_sans"`
	IssuerFile string   `yaml:"issuer_certificate"`
}

type clientDocument struct {
	ID                      string              `yaml:"id"`
	Enabled                 bool                `yaml:"enabled"`
	AllowedIPs              []string            `yaml:"allowed_ips"`
	AllowedCertificates     []string            `yaml:"allowed_certificates"`
	TokenHashFile           string              `yaml:"token_hash_file"`
	Overrides               map[string][]string `yaml:"certificate_access_overrides"`
	InventoryAccess         string              `yaml:"inventory_access"`
	ClientCertificateSHA256 []string            `yaml:"client_certificate_sha256"`
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
		return "", nil, fmt.Errorf("storage.certificate_dir must be absolute")
	}
	var names []string
	for _, certificate := range doc.Certificates {
		names = append(names, certificate.Name)
	}
	return doc.Storage.CertificateDir, names, nil
}

func readServerDocument(path string) (serverDocument, error) {
	body, err := readFile(path, false, 1<<20)
	if err != nil {
		return serverDocument{}, fmt.Errorf("read server config: %w", err)
	}
	return decodeServer(body)
}

func loadServerDocument(doc serverDocument) (Server, error) {
	_, port, err := net.SplitHostPort(doc.Server.Listen)
	portNumber, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || portErr != nil || portNumber == 0 {
		return Server{}, fmt.Errorf("server.listen must contain a valid host and port")
	}
	for _, path := range []string{doc.Storage.CertificateDir, doc.Storage.TrustCAFile, doc.Server.TLS.Certificate, doc.Server.TLS.PrivateKey} {
		if !filepath.IsAbs(path) {
			return Server{}, fmt.Errorf("storage and TLS paths must be absolute")
		}
	}
	if _, err := checkedFile(doc.Server.TLS.PrivateKey, true); err != nil {
		return Server{}, fmt.Errorf("check TLS private key: %w", err)
	}
	rootsPEM, err := readFile(doc.Storage.TrustCAFile, false, 1<<20)
	if err != nil {
		return Server{}, fmt.Errorf("read trust CA file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootsPEM) {
		return Server{}, fmt.Errorf("trust CA file contains no certificates")
	}
	result := Server{
		Listen: doc.Server.Listen, TLSCertificate: doc.Server.TLS.Certificate,
		TLSPrivateKey: doc.Server.TLS.PrivateKey, CertificateDir: doc.Storage.CertificateDir,
		Policies: make(map[string]pki.Policy),
	}
	if path := doc.Server.TLS.ClientCA; path != "" {
		if !filepath.IsAbs(path) {
			return Server{}, fmt.Errorf("server.tls.client_ca must be absolute")
		}
		result.TLSClientCAs, err = loadClientRoots(path)
		if err != nil {
			return Server{}, fmt.Errorf("load mTLS client CA: %w", err)
		}
	}
	if value := doc.Server.PublicationRefresh; value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval < time.Second {
			return Server{}, fmt.Errorf("server.publication_refresh must be a duration of at least one second")
		}
		result.PublicationRefresh = interval
	}
	if value := doc.Server.InventoryRefresh; value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval < time.Second || doc.DNSInventory == nil {
			return Server{}, fmt.Errorf("server.inventory_refresh requires dns_inventory and a duration of at least one second")
		}
		result.InventoryRefresh = interval
	}
	if doc.DNSInventory != nil && doc.DNSInventory.MaxAge != "" && result.InventoryRefresh == 0 {
		return Server{}, fmt.Errorf("dns_inventory.max_age requires server.inventory_refresh")
	}
	if path := doc.Server.RevocationFile; path != "" {
		if !filepath.IsAbs(path) {
			return Server{}, fmt.Errorf("server.revocation_file must be absolute")
		}
		result.RevocationFile = path
		result.RevocationRefresh = 5 * time.Second
	} else if doc.Server.RevocationRefresh != "" {
		return Server{}, fmt.Errorf("server.revocation_refresh requires server.revocation_file")
	}
	if value := doc.Server.RevocationRefresh; value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval < time.Second {
			return Server{}, fmt.Errorf("server.revocation_refresh must be a duration of at least one second")
		}
		result.RevocationRefresh = interval
	}
	for i, cert := range doc.Certificates {
		if _, exists := result.Policies[cert.Name]; cert.Name == "" || exists {
			return Server{}, fmt.Errorf("missing or duplicate certificates[%d].name", i)
		}
		policy, err := cert.policy(roots, doc.Storage.IssuerFile)
		if err != nil {
			return Server{}, fmt.Errorf("certificates[%d]: %w", i, err)
		}
		result.Policies[cert.Name] = policy
	}
	var inventory *dns.Inventory
	var inventoryExpires time.Time
	if source := doc.DNSInventory; source != nil {
		parsed, err := source.loadWithExpiry(&inventoryExpires)
		if err != nil {
			return Server{}, err
		}
		inventory = &parsed
	}
	if doc.Renewal.FromInventory {
		if inventory == nil || len(doc.Certificates) != 0 || len(doc.Renewal.Profiles) != 0 ||
			result.InventoryRefresh == 0 || result.PublicationRefresh == 0 {
			return Server{}, fmt.Errorf("inventory issuance requires inventory and both refresh intervals, without explicit certificate targets or profiles")
		}
		issuer, err := (certificateDocument{Name: "issuer.example.invalid", IssuerFile: doc.Renewal.CertificateFile}).policy(roots, "")
		if err != nil {
			return Server{}, err
		}
		for _, name := range inventory.Names() {
			result.Policies[name] = pki.Policy{Roots: roots, Issuer: issuer.Issuer, DNS: []string{name}}
		}
		result.InventoryCertificates = true
	}
	clientCertOwners := make(map[[sha256.Size]byte]string)
	for i, entry := range doc.Clients {
		client, err := entry.client(result.Policies, inventory)
		if err != nil {
			return Server{}, fmt.Errorf("clients[%d]: %w", i, err)
		}
		client.ClientCertificateHashes, err = entry.certificateHashes(result.TLSClientCAs != nil)
		if err != nil {
			return Server{}, fmt.Errorf("clients[%d]: %w", i, err)
		}
		if client.Enabled {
			for _, fingerprint := range client.ClientCertificateHashes {
				if owner, exists := clientCertOwners[fingerprint]; exists && owner != client.ID {
					return Server{}, fmt.Errorf("clients[%d]: client certificate fingerprint is assigned to multiple enabled clients", i)
				}
				clientCertOwners[fingerprint] = client.ID
			}
		}
		client.InventoryExpiresAt = inventoryExpires
		result.Clients = append(result.Clients, client)
	}
	if result.InventoryRefresh != 0 {
		result.ReloadInventoryClients = inventoryClientLoader(*doc.DNSInventory, doc.Clients, result.Clients, result.Policies)
	}
	return result, nil
}

func (c clientDocument) certificateHashes(mtls bool) ([][sha256.Size]byte, error) {
	if !mtls && len(c.ClientCertificateSHA256) != 0 {
		return nil, fmt.Errorf("client_certificate_sha256 requires server.tls.client_ca")
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

func decodeServer(body []byte) (serverDocument, error) {
	var doc serverDocument
	var keys map[string]any
	if err := decodeYAML(body, &keys); err != nil {
		return doc, err
	}
	for _, key := range []string{"inventory", "dns", "distribution"} {
		if _, exists := keys[key]; exists {
			return decodeWorkflowServer(body)
		}
	}
	if err := decodeYAML(body, &doc); err != nil {
		return doc, err
	}
	if p := doc.Pipeline; p != nil {
		var legacy map[string]any
		if err := decodeYAML(body, &legacy); err != nil {
			return doc, err
		}
		for _, key := range []string{"dns_inventory", "google_sheets", "zones", "renewal"} {
			if _, exists := legacy[key]; exists {
				return doc, fmt.Errorf("pipeline cannot be combined with top-level %s", key)
			}
		}
		doc.DNSInventory, doc.Sheets, doc.Zones = p.DNS.Inventory, p.DNS.Sheets, p.DNS.Zones
		doc.Renewal = p.Certificates.Renewal
		doc.Deployment = &p.DNS.Deployment
	}
	return doc, nil
}

func decodeYAML(body []byte, target any) error {
	dec := yaml.NewDecoder(bytes.NewReader(body), yaml.Strict())
	if err := dec.Decode(target); err != nil {
		// YAML diagnostics can contain input lines. Avoid logging configuration contents.
		return fmt.Errorf("invalid YAML: check syntax, field names, types and duplicate keys")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("YAML must contain exactly one document")
	}
	return nil
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

func (c clientDocument) client(policies map[string]pki.Policy, inventory *dns.Inventory) (distribution.Client, error) {
	client := distribution.Client{ID: c.ID, Enabled: c.Enabled, AllowedCertificates: c.AllowedCertificates}
	if inventory != nil {
		if len(c.AllowedIPs) != 0 {
			return client, fmt.Errorf("allowed_ips is incompatible with DNS inventory mode")
		}
		rules, err := c.inventoryRules(policies, *inventory)
		if err != nil {
			return client, err
		}
		client.CertificateIPs = rules
		client.AllowedCertificates = nil
	} else if len(c.Overrides) != 0 || c.InventoryAccess != "" {
		return client, fmt.Errorf("inventory access configuration requires dns_inventory")
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

func (c clientDocument) inventoryRules(policies map[string]pki.Policy, inventory dns.Inventory) (map[string][]netip.Prefix, error) {
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
		if _, exists := policies[name]; !exists {
			return nil, fmt.Errorf("certificate access override references an unconfigured certificate")
		}
		if restrictNames && !allowed[name] {
			return nil, fmt.Errorf("certificate access override is outside allowed_certificates")
		}
		prefixes, err := inventory.AllowedPrefixes(hosts)
		if err != nil {
			return nil, fmt.Errorf("certificate access override: %w", err)
		}
		rules[name] = append(rules[name], prefixes...)
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
		clients := make([]distribution.Client, len(initial))
		for i, client := range initial {
			client.InventoryExpiresAt = expires
			client.CertificateIPs, err = entries[i].inventoryRules(policies, inventory)
			if err != nil {
				return nil, fmt.Errorf("derive inventory rules for clients[%d]: %w", i, err)
			}
			clients[i] = client
		}
		return clients, nil
	}
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

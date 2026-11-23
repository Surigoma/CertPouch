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
	Listen             string
	TLSCertificate     string
	TLSPrivateKey      string
	CertificateDir     string
	Policies           map[string]pki.Policy
	Clients            []distribution.Client
	PublicationRefresh time.Duration
}

type serverDocument struct {
	Server struct {
		Listen             string `yaml:"listen"`
		PublicationRefresh string `yaml:"publication_refresh"`
		TLS                struct {
			Certificate string `yaml:"certificate"`
			PrivateKey  string `yaml:"private_key"`
		} `yaml:"tls"`
	} `yaml:"server"`
	Storage struct {
		CertificateDir string `yaml:"certificate_dir"`
		TrustCAFile    string `yaml:"trust_ca_file"`
	} `yaml:"storage"`
	DNSInventory *inventoryDocument    `yaml:"dns_inventory"`
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
	ID                  string              `yaml:"id"`
	Enabled             bool                `yaml:"enabled"`
	AllowedIPs          []string            `yaml:"allowed_ips"`
	AllowedCertificates []string            `yaml:"allowed_certificates"`
	TokenHashFile       string              `yaml:"token_hash_file"`
	Overrides           map[string][]string `yaml:"certificate_access_overrides"`
}

func LoadServer(path string) (Server, error) {
	body, err := readFile(path, false, 1<<20)
	if err != nil {
		return Server{}, fmt.Errorf("read server config: %w", err)
	}
	doc, err := decodeServer(body)
	if err != nil {
		return Server{}, err
	}
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
	if value := doc.Server.PublicationRefresh; value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval < time.Second {
			return Server{}, fmt.Errorf("server.publication_refresh must be a duration of at least one second")
		}
		result.PublicationRefresh = interval
	}
	for i, cert := range doc.Certificates {
		if _, exists := result.Policies[cert.Name]; cert.Name == "" || exists {
			return Server{}, fmt.Errorf("missing or duplicate certificates[%d].name", i)
		}
		policy, err := cert.policy(roots)
		if err != nil {
			return Server{}, fmt.Errorf("certificates[%d]: %w", i, err)
		}
		result.Policies[cert.Name] = policy
	}
	var inventory *dns.Inventory
	if source := doc.DNSInventory; source != nil {
		parsed, err := source.load()
		if err != nil {
			return Server{}, err
		}
		inventory = &parsed
	}
	for i, entry := range doc.Clients {
		client, err := entry.client(result.Policies, inventory)
		if err != nil {
			return Server{}, fmt.Errorf("clients[%d]: %w", i, err)
		}
		result.Clients = append(result.Clients, client)
	}
	return result, nil
}

func decodeServer(body []byte) (serverDocument, error) {
	var doc serverDocument
	err := decodeYAML(body, &doc)
	return doc, err
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

func (c certificateDocument) policy(roots *x509.CertPool) (pki.Policy, error) {
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

func (c clientDocument) client(policies map[string]pki.Policy, inventory *dns.Inventory) (distribution.Client, error) {
	client := distribution.Client{ID: c.ID, Enabled: c.Enabled, AllowedCertificates: c.AllowedCertificates}
	if inventory != nil {
		if len(c.AllowedIPs) != 0 || len(c.AllowedCertificates) != 0 {
			return client, fmt.Errorf("DNS inventory mode derives access; use certificate_access_overrides for exceptions")
		}
		client.CertificateIPs = make(map[string][]netip.Prefix, len(policies))
		for name := range policies {
			client.CertificateIPs[name] = inventory.PrefixesForName(name)
		}
		for name, hosts := range c.Overrides {
			if _, exists := policies[name]; !exists {
				return client, fmt.Errorf("certificate access override references an unconfigured certificate")
			}
			prefixes, err := inventory.AllowedPrefixes(hosts)
			if err != nil {
				return client, fmt.Errorf("certificate access override: %w", err)
			}
			client.CertificateIPs[name] = append(client.CertificateIPs[name], prefixes...)
		}
	} else if len(c.Overrides) != 0 {
		return client, fmt.Errorf("certificate_access_overrides requires dns_inventory")
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
		if _, ok := policies[name]; !ok {
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

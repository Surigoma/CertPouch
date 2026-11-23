package config

import (
	"crypto/x509"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Surigoma/CertPouch/internal/pki"
)

type Client struct {
	URL      string
	ID       string
	Token    string
	TLSRoots *x509.CertPool
	Targets  []Target
}

type Target struct {
	Name                            string
	Destination                     string
	Policy                          pki.Policy
	Owner, Group                    string
	CertificateMode, PrivateKeyMode string
	Unit, Action                    string
}

type pullDocument struct {
	Server struct {
		URL    string `yaml:"url"`
		CAFile string `yaml:"ca_file"`
	} `yaml:"server"`
	Client struct {
		ID        string `yaml:"id"`
		TokenFile string `yaml:"token_file"`
		StateDir  string `yaml:"state_dir"`
	} `yaml:"client"`
	Validation struct {
		TrustCAFile string `yaml:"trust_ca_file"`
	} `yaml:"validation"`
	Certificates []pullCertificate `yaml:"certificates"`
}

type pullCertificate struct {
	Name            string   `yaml:"name"`
	Destination     string   `yaml:"destination"`
	DNS             []string `yaml:"expected_dns_sans"`
	IPs             []string `yaml:"expected_ip_sans"`
	IssuerFile      string   `yaml:"issuer_certificate"`
	Owner           string   `yaml:"owner"`
	Group           string   `yaml:"group"`
	CertificateMode string   `yaml:"certificate_mode"`
	PrivateKeyMode  string   `yaml:"private_key_mode"`
	Service         struct {
		Unit   string `yaml:"unit"`
		Action string `yaml:"action"`
	} `yaml:"service"`
}

func LoadClient(path string) (Client, error) {
	body, err := readFile(path, false, 1<<20)
	if err != nil {
		return Client{}, fmt.Errorf("read client config: %w", err)
	}
	var doc pullDocument
	if err := decodeYAML(body, &doc); err != nil {
		return Client{}, err
	}
	for _, value := range []string{doc.Server.CAFile, doc.Client.TokenFile, doc.Validation.TrustCAFile} {
		if !filepath.IsAbs(value) {
			return Client{}, fmt.Errorf("client CA and token paths must be absolute")
		}
	}
	tlsRoots, err := loadClientRoots(doc.Server.CAFile)
	if err != nil {
		return Client{}, fmt.Errorf("load TLS CA: %w", err)
	}
	roots, err := loadClientRoots(doc.Validation.TrustCAFile)
	if err != nil {
		return Client{}, fmt.Errorf("load certificate validation CA: %w", err)
	}
	token, err := readFile(doc.Client.TokenFile, true, 1026)
	if err != nil {
		return Client{}, fmt.Errorf("read client token file: %w", err)
	}
	result := Client{
		URL: doc.Server.URL, ID: doc.Client.ID, TLSRoots: tlsRoots,
		Token: strings.TrimSuffix(strings.TrimSuffix(string(token), "\n"), "\r"),
	}
	seen := make(map[string]bool)
	for i, target := range doc.Certificates {
		if target.Name == "" || seen[target.Name] || !filepath.IsAbs(target.Destination) {
			return Client{}, fmt.Errorf("certificates[%d] requires a unique name and absolute destination", i)
		}
		dest := filepath.Clean(target.Destination)
		if filepath.Dir(dest) == dest {
			return Client{}, fmt.Errorf("certificates[%d] cannot use a filesystem root", i)
		}
		for _, existing := range result.Targets {
			if containsPath(existing.Destination, dest) || containsPath(dest, existing.Destination) {
				return Client{}, fmt.Errorf("certificate destinations must not overlap")
			}
		}
		policy, err := (certificateDocument{DNS: target.DNS, IPs: target.IPs, IssuerFile: target.IssuerFile}).policy(roots)
		if err != nil {
			return Client{}, fmt.Errorf("certificates[%d]: %w", i, err)
		}
		seen[target.Name] = true
		result.Targets = append(result.Targets, Target{
			Name: target.Name, Destination: dest, Policy: policy,
			Owner: target.Owner, Group: target.Group,
			CertificateMode: target.CertificateMode, PrivateKeyMode: target.PrivateKeyMode,
			Unit: target.Service.Unit, Action: target.Service.Action,
		})
	}
	if len(result.Targets) == 0 {
		return Client{}, fmt.Errorf("configure at least one certificate")
	}
	return result, nil
}

func containsPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func loadClientRoots(path string) (*x509.CertPool, error) {
	body, err := readFile(path, false, 1<<20)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(body) {
		return nil, fmt.Errorf("CA file contains no certificates")
	}
	return pool, nil
}

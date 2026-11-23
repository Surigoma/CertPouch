package config

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Surigoma/CertPouch/internal/pki"
)

type Client struct {
	URL            string
	ID             string
	Token          string
	TLSRoots       *x509.CertPool
	TLSCertificate *tls.Certificate
	Targets        []Target
}

type Target struct {
	Name                            string
	Destination                     string
	Policy                          pki.Policy
	Owner, Group                    string
	CertificateMode, PrivateKeyMode string
	Unit, Action                    string
	CommandArgs                     []string
	CommandTimeout                  time.Duration
	StateFile                       string
	TLSAddress, TLSServerName       string
	TLSVerifyTimeout                time.Duration
}

type pullDocument struct {
	Server struct {
		URL    string `yaml:"url"`
		CAFile string `yaml:"ca_file"`
	} `yaml:"server"`
	Client struct {
		ID             string `yaml:"id"`
		TokenFile      string `yaml:"token_file"`
		StateDir       string `yaml:"state_dir"`
		TLSCertificate string `yaml:"tls_certificate"`
		TLSPrivateKey  string `yaml:"tls_private_key"`
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
	Service         *struct {
		Unit      string             `yaml:"unit"`
		Action    string             `yaml:"action"`
		VerifyTLS *tlsVerifyDocument `yaml:"verify_tls"`
	} `yaml:"service"`
	Command *struct {
		Args      []string           `yaml:"args"`
		Timeout   string             `yaml:"timeout"`
		VerifyTLS *tlsVerifyDocument `yaml:"verify_tls"`
	} `yaml:"command"`
}

type tlsVerifyDocument struct {
	Address    string `yaml:"address"`
	ServerName string `yaml:"server_name"`
	Timeout    string `yaml:"timeout"`
}

func LoadClient(path string) (Client, error) {
	return loadClient(path, true)
}

func LoadLocalClient(path string) (Client, error) {
	return loadClient(path, false)
}

func loadClient(path string, network bool) (Client, error) {
	body, err := readFile(path, false, 1<<20)
	if err != nil {
		return Client{}, fmt.Errorf("read client config: %w", err)
	}
	var doc pullDocument
	if err := decodeYAML(body, &doc); err != nil {
		return Client{}, err
	}
	if !filepath.IsAbs(doc.Validation.TrustCAFile) || (doc.Client.StateDir != "" && !filepath.IsAbs(doc.Client.StateDir)) {
		return Client{}, fmt.Errorf("validation CA and state paths must be absolute")
	}
	roots, err := loadClientRoots(doc.Validation.TrustCAFile)
	if err != nil {
		return Client{}, fmt.Errorf("load certificate validation CA: %w", err)
	}
	result := Client{URL: doc.Server.URL, ID: doc.Client.ID}
	if network {
		if !filepath.IsAbs(doc.Server.CAFile) || !filepath.IsAbs(doc.Client.TokenFile) {
			return Client{}, fmt.Errorf("TLS CA and token paths must be absolute")
		}
		result.TLSRoots, err = loadClientRoots(doc.Server.CAFile)
		if err != nil {
			return Client{}, fmt.Errorf("load TLS CA: %w", err)
		}
		token, err := readFile(doc.Client.TokenFile, true, 1026)
		if err != nil {
			return Client{}, fmt.Errorf("read client token file: %w", err)
		}
		result.Token = strings.TrimSuffix(strings.TrimSuffix(string(token), "\n"), "\r")
		if doc.Client.TLSCertificate != "" || doc.Client.TLSPrivateKey != "" {
			result.TLSCertificate, err = loadTLSClientCertificate(doc.Client.TLSCertificate, doc.Client.TLSPrivateKey)
			if err != nil {
				return Client{}, err
			}
		}
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
		stateFile := filepath.Join(dest, ".sync.json")
		if doc.Client.StateDir != "" {
			digest := sha256.Sum256([]byte(target.Name + "\x00" + dest))
			stateFile = filepath.Join(doc.Client.StateDir, fmt.Sprintf("%x.json", digest))
		}
		entry := Target{
			Name: target.Name, Destination: dest, Policy: policy,
			Owner: target.Owner, Group: target.Group,
			CertificateMode: target.CertificateMode, PrivateKeyMode: target.PrivateKeyMode,
			StateFile: stateFile,
		}
		var probe *tlsVerifyDocument
		operation := "service"
		if target.Service != nil && target.Command != nil {
			return Client{}, fmt.Errorf("certificates[%d] cannot configure both service and command", i)
		}
		if service := target.Service; service != nil {
			entry.Unit, entry.Action = service.Unit, service.Action
			probe = service.VerifyTLS
		}
		if command := target.Command; command != nil {
			if len(command.Args) == 0 || strings.TrimSpace(command.Args[0]) == "" {
				return Client{}, fmt.Errorf("certificates[%d].command.args requires an executable", i)
			}
			for _, arg := range command.Args {
				if strings.ContainsRune(arg, 0) {
					return Client{}, fmt.Errorf("certificates[%d].command.args cannot contain NUL characters", i)
				}
			}
			entry.CommandArgs = command.Args
			if command.Timeout != "" {
				timeout, err := time.ParseDuration(command.Timeout)
				if err != nil || timeout <= 0 {
					return Client{}, fmt.Errorf("certificates[%d].command.timeout must be a positive duration", i)
				}
				entry.CommandTimeout = timeout
			}
			probe = command.VerifyTLS
			operation = "command"
		}
		if probe != nil {
			if probe.Address == "" || probe.ServerName == "" {
				return Client{}, fmt.Errorf("certificates[%d].%s.verify_tls requires address and server_name", i, operation)
			}
			entry.TLSAddress, entry.TLSServerName = probe.Address, probe.ServerName
			if probe.Timeout != "" {
				timeout, err := time.ParseDuration(probe.Timeout)
				if err != nil || timeout < time.Second || timeout > 2*time.Minute {
					return Client{}, fmt.Errorf("certificates[%d].%s.verify_tls.timeout must be between 1s and 2m", i, operation)
				}
				entry.TLSVerifyTimeout = timeout
			}
		}
		result.Targets = append(result.Targets, entry)
	}
	if len(result.Targets) == 0 {
		return Client{}, fmt.Errorf("configure at least one certificate")
	}
	return result, nil
}

func loadTLSClientCertificate(certPath, keyPath string) (*tls.Certificate, error) {
	if !filepath.IsAbs(certPath) || !filepath.IsAbs(keyPath) {
		return nil, fmt.Errorf("mTLS requires absolute client.tls_certificate and client.tls_private_key paths")
	}
	certificate, err := readFile(certPath, false, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read mTLS client certificate: %w", err)
	}
	key, err := readFile(keyPath, true, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read mTLS client key: %w", err)
	}
	defer clear(key)
	pair, err := tls.X509KeyPair(certificate, key)
	if err != nil {
		return nil, fmt.Errorf("invalid mTLS client certificate and key pair")
	}
	return &pair, nil
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

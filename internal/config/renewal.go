package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/Surigoma/CertPouch/internal/pki"
)

// Renewal is derived from the certificates section of server.yaml.
type Renewal struct {
	FromInventory   bool
	Validity        string
	RenewBefore     string
	CertificateFile string
	KeyFile         string
	ChainFile       string
	PassphraseFile  string
	Profiles        []RenewalProfile
}

type RenewalProfile struct {
	Name            string
	ValidityText    string
	RenewBeforeText string
	Validity        time.Duration
	RenewBefore     time.Duration
}

// LoadRenewalInputs derives authorization policy and issuance profiles from
// the same configuration and committed inventory.
func LoadRenewalInputs(path string) (Server, Renewal, error) {
	doc, err := readServerDocument(path)
	if err != nil {
		return Server{}, Renewal{}, err
	}
	server, err := loadServerDocument(doc)
	if err != nil {
		return Server{}, Renewal{}, err
	}
	names := make([]string, 0, len(server.Policies))
	for name := range server.Policies {
		names = append(names, name)
	}
	slices.Sort(names)
	renewal, err := renewalForNames(doc.Renewal, names)
	return server, renewal, err
}

func renewalForNames(cfg Renewal, names []string) (Renewal, error) {
	if cfg.FromInventory {
		for _, name := range names {
			cfg.Profiles = append(cfg.Profiles, RenewalProfile{Name: name, ValidityText: cfg.Validity, RenewBeforeText: cfg.RenewBefore})
		}
	}
	return validateRenewal(cfg)
}

func validateRenewal(cfg Renewal) (Renewal, error) {
	for _, field := range []struct {
		key, path string
		required  bool
	}{
		{"certificates.issuer.certificate_file", cfg.CertificateFile, true},
		{"certificates.issuer.private_key_file", cfg.KeyFile, true},
		{"certificates.issuer.chain_file", cfg.ChainFile, false},
		{"certificates.issuer.passphrase_file", cfg.PassphraseFile, false},
	} {
		if (field.required || field.path != "") && !filepath.IsAbs(field.path) {
			return cfg, fmt.Errorf("certificate issuance requires %s as an absolute path", field.key)
		}
	}
	if len(cfg.Profiles) == 0 && !cfg.FromInventory {
		return cfg, fmt.Errorf("certificate issuance has no targets: list certificates.targets or omit them to issue every inventory name")
	}
	if cfg.FromInventory && len(cfg.Profiles) == 0 {
		validity, validityErr := time.ParseDuration(cfg.Validity)
		before, beforeErr := time.ParseDuration(cfg.RenewBefore)
		if validityErr != nil || beforeErr != nil || validity%time.Second != 0 || before <= 0 || before >= validity {
			return cfg, fmt.Errorf("certificates.validity and certificates.renew_before must be durations with renew_before shorter than validity")
		}
	}
	seen := make(map[string]bool, len(cfg.Profiles))
	for i := range cfg.Profiles {
		p := &cfg.Profiles[i]
		var validityErr, beforeErr error
		p.Validity, validityErr = time.ParseDuration(p.ValidityText)
		p.RenewBefore, beforeErr = time.ParseDuration(p.RenewBeforeText)
		if p.Name == "" || seen[p.Name] || validityErr != nil || beforeErr != nil ||
			p.Validity%time.Second != 0 || p.RenewBefore <= 0 || p.RenewBefore >= p.Validity {
			return cfg, fmt.Errorf("certificate %q needs a unique name and validity/renew_before durations with renew_before shorter than validity (certificates.validity or certificates.targets[].validity)", p.Name)
		}
		seen[p.Name] = true
	}
	return cfg, nil
}

func (cfg Renewal) LoadIssuer(ctx context.Context) (*pki.Issuer, error) {
	cert, err := readFile(cfg.CertificateFile, false, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read signing certificate: %w", err)
	}
	key, err := readFile(cfg.KeyFile, true, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read signing private key: %w", err)
	}
	defer clear(key)
	if cfg.PassphraseFile != "" {
		secret, err := readFile(cfg.PassphraseFile, true, 1024)
		if err != nil {
			return nil, fmt.Errorf("read issuer passphrase file: %w", err)
		}
		defer clear(secret)
		key, err = decryptIssuerKey(ctx, key, secret)
		if err != nil {
			return nil, err
		}
		defer clear(key)
	}
	var chain []byte
	if cfg.ChainFile != "" {
		chain, err = readFile(cfg.ChainFile, false, 1<<20)
		if err != nil {
			return nil, fmt.Errorf("read issuer chain: %w", err)
		}
	}
	return pki.NewIssuer(cert, key, chain)
}

func decryptIssuerKey(ctx context.Context, encrypted, passphrase []byte) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("encrypted issuer keys require Linux and OpenSSL")
	}
	pass := bytes.TrimSuffix(passphrase, []byte("\n"))
	if len(pass) != len(passphrase) {
		pass = bytes.TrimSuffix(pass, []byte("\r"))
	}
	if len(pass) == 0 || bytes.ContainsAny(pass, "\r\n\x00") {
		return nil, fmt.Errorf("issuer passphrase file must contain one nonempty line")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	input := append(append([]byte(nil), pass...), '\n')
	defer clear(input)
	if _, err := writer.Write(input); err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(timeout, "openssl", "pkey", "-passin", "fd:3")
	cmd.ExtraFiles = []*os.File{reader}
	cmd.Stdin = bytes.NewReader(encrypted)
	decrypted, err := cmd.Output()
	if err != nil || len(decrypted) > 1<<20 {
		clear(decrypted)
		return nil, fmt.Errorf("OpenSSL could not decrypt the configured issuer key")
	}
	return decrypted, nil
}

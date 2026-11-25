package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Surigoma/CertPouch/internal/pki"
)

type Renewal struct {
	FromInventory   bool             `yaml:"from_inventory"`
	Validity        string           `yaml:"validity"`
	RenewBefore     string           `yaml:"renew_before"`
	CertificateFile string           `yaml:"issuer_certificate"`
	KeyFile         string           `yaml:"issuer_private_key"`
	ChainFile       string           `yaml:"issuer_chain"`
	PassphraseFile  string           `yaml:"issuer_passphrase_file"`
	Profiles        []RenewalProfile `yaml:"profiles"`
}

type RenewalProfile struct {
	Name            string        `yaml:"name"`
	ValidityText    string        `yaml:"validity"`
	RenewBeforeText string        `yaml:"renew_before"`
	Validity        time.Duration `yaml:"-"`
	RenewBefore     time.Duration `yaml:"-"`
}

func LoadRenewal(path string) (Renewal, error) {
	var cfg Renewal
	raw, err := readFile(path, false, 1<<20)
	if err != nil {
		return cfg, fmt.Errorf("read renewal configuration: %w", err)
	}
	if doc, unifiedErr := decodeServer(raw); unifiedErr == nil && (len(doc.Renewal.Profiles) != 0 || doc.Renewal.FromInventory) {
		cfg = doc.Renewal
		if cfg.FromInventory {
			if doc.DNSInventory == nil || len(cfg.Profiles) != 0 {
				return cfg, fmt.Errorf("inventory renewal requires an inventory and no explicit profiles")
			}
			inventory, err := doc.DNSInventory.load()
			if err != nil {
				return cfg, err
			}
			for _, name := range inventory.Names() {
				cfg.Profiles = append(cfg.Profiles, RenewalProfile{Name: name, ValidityText: cfg.Validity, RenewBeforeText: cfg.RenewBefore})
			}
		}
	} else if err := decodeYAML(raw, &cfg); err != nil {
		return cfg, err
	}
	return validateRenewal(cfg)
}

func validateRenewal(cfg Renewal) (Renewal, error) {
	if !filepath.IsAbs(cfg.CertificateFile) || !filepath.IsAbs(cfg.KeyFile) ||
		(cfg.ChainFile != "" && !filepath.IsAbs(cfg.ChainFile)) ||
		(cfg.PassphraseFile != "" && !filepath.IsAbs(cfg.PassphraseFile)) || (len(cfg.Profiles) == 0 && !cfg.FromInventory) {
		return cfg, fmt.Errorf("renewal requires absolute issuer paths and at least one certificate profile")
	}
	if cfg.FromInventory && len(cfg.Profiles) == 0 {
		validity, validityErr := time.ParseDuration(cfg.Validity)
		before, beforeErr := time.ParseDuration(cfg.RenewBefore)
		if validityErr != nil || beforeErr != nil || validity%time.Second != 0 || before <= 0 || before >= validity {
			return cfg, fmt.Errorf("invalid inventory renewal validity or renewal threshold")
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
			return cfg, fmt.Errorf("invalid or duplicate renewal profile at index %d", i)
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

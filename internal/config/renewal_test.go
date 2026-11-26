package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadRenewalRejectsInvalidProfileConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	issuer, key := filepath.Join(dir, "issuer.pem"), filepath.Join(dir, "issuer.key")
	base := fmt.Sprintf("issuer_certificate: %q\nissuer_private_key: %q\n", issuer, key)
	const profile = "  - {name: web.example.internal, validity: 24h, renew_before: 1h}\n"
	for _, tc := range []struct{ name, config, want string }{
		{"no profiles", base, "at least one certificate profile"},
		{"relative chain path", base + "issuer_chain: chain.pem\nprofiles:\n" + profile, "absolute issuer paths"},
		{"duplicate profile", base + "profiles:\n" + profile + profile, "invalid or duplicate renewal profile"},
		{"renewal before issuance", base + "profiles:\n" + strings.Replace(profile, "1h", "25h", 1), "invalid or duplicate renewal profile"},
		{"subsecond validity", base + "profiles:\n" + strings.Replace(profile, "24h", "24h1ms", 1), "invalid or duplicate renewal profile"},
		{"unusable inventory policy", base + "from_inventory: true\nvalidity: 24h\nrenew_before: 24h\n", "invalid inventory renewal validity or renewal threshold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadRenewal(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadRenewal error = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := LoadRenewal(filepath.Join(dir, "absent.yaml")); err == nil || !strings.Contains(err.Error(), "read renewal configuration") {
		t.Fatalf("missing renewal config error = %v", err)
	}
}

func TestLoadIssuerRejectsMissingMaterial(t *testing.T) {
	dir := t.TempDir()
	cert := writeTestIssuer(t)
	key := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(key, []byte("placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(dir, "absent")
	for _, tc := range []struct {
		cfg  Renewal
		want string
	}{
		{Renewal{CertificateFile: absent, KeyFile: key}, "read signing certificate"},
		{Renewal{CertificateFile: cert, KeyFile: absent}, "read signing private key"},
		{Renewal{CertificateFile: cert, KeyFile: key, PassphraseFile: absent}, "read issuer passphrase file"},
		{Renewal{CertificateFile: cert, KeyFile: key, ChainFile: absent}, "read issuer chain"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			if _, err := tc.cfg.LoadIssuer(t.Context()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadIssuer error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadIssuerRejectsInvalidPassphraseFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("encrypted issuer keys are supported on Linux")
	}
	dir := t.TempDir()
	cert := writeTestIssuer(t)
	key := filepath.Join(dir, "issuer.key")
	passphrase := filepath.Join(dir, "issuer.passphrase")
	if err := os.WriteFile(key, []byte("placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"", "private-test-phrase\r\nsecond line\r\n", "private-test-phrase\x00"} {
		if err := os.WriteFile(passphrase, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		cfg := Renewal{CertificateFile: cert, KeyFile: key, PassphraseFile: passphrase}
		if _, err := cfg.LoadIssuer(t.Context()); err == nil ||
			!strings.Contains(err.Error(), "one nonempty line") || strings.Contains(err.Error(), "private-test-phrase") {
			t.Fatalf("invalid passphrase file error = %v", err)
		}
	}
}

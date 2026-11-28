package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRenewalRejectsInvalidIssuanceConfiguration(t *testing.T) {
	dir := t.TempDir()
	issuer, key := filepath.Join(dir, "issuer.pem"), filepath.Join(dir, "issuer.key")
	base := "certificates:\n  issuer: {certificate_file: " + strconv.Quote(issuer) + ", private_key_file: " + strconv.Quote(key) + "%s}\n%s"
	const target = "    - {name: web.example.internal, validity: 24h, renew_before: 1h}\n"
	for _, tc := range []struct{ name, config, want string }{
		{"missing key", "certificates:\n  issuer: {certificate_file: " + strconv.Quote(issuer) + "}\n  targets: [{name: web.example.internal}]\n", "certificates.issuer.private_key_file as an absolute path"},
		{"relative chain path", fmt.Sprintf(base, ", chain_file: chain.pem", "  targets:\n"+target), "certificates.issuer.chain_file as an absolute path"},
		{"duplicate target", fmt.Sprintf(base, "", "  targets:\n"+target+target), `certificate "web.example.internal" needs a unique name`},
		{"renewal before issuance", fmt.Sprintf(base, "", "  targets:\n"+strings.Replace(target, "1h", "25h", 1)), "renew_before shorter than validity"},
		{"subsecond validity", fmt.Sprintf(base, "", "  targets:\n"+strings.Replace(target, "24h", "24h1ms", 1)), "renew_before shorter than validity"},
		{"missing target timing", fmt.Sprintf(base, "", "  targets: [{name: web.example.internal}]\n"), "certificates.validity or certificates.targets[].validity"},
		{"unusable inventory policy", fmt.Sprintf(base, "", "  validity: 24h\n  renew_before: 24h\n"), "certificates.validity and certificates.renew_before"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := decodeServer([]byte(tc.config))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := renewalForNames(doc.Renewal, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("renewal error = %v, want %q", err, tc.want)
			}
		})
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

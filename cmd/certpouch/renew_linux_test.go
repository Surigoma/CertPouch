package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/pki"
	"github.com/Surigoma/CertPouch/internal/storage"
)

func TestRenewWithPrivateCA(t *testing.T) {
	base := t.TempDir()
	useSystemd := os.Getenv("CERTPOUCH_TEST_SYSTEMD") == "1"
	if useSystemd {
		var err error
		base, err = os.MkdirTemp("/run", "certpouch-renew-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(base); err != nil {
				t.Error(err)
			}
		})
	}
	store := filepath.Join(base, "store")
	if err := os.Mkdir(store, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(base, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	now := time.Now().UTC().Truncate(time.Second)
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Renewal test CA"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(90 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, root, root, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caFile := write("ca.pem", caPEM)
	trustedRoot, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	intermediate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Renewal signing CA"},
		NotBefore: now.Add(-12 * time.Hour), NotAfter: now.Add(60 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	issuerDER, err := x509.CreateCertificate(rand.Reader, intermediate, trustedRoot, &signingKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	issuerPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuerDER})
	issuerFile := write("intermediate.pem", issuerPEM)
	tlsKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tlsKeyFile := write("tls.key", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(tlsKey)}))
	server := write("server.yaml", []byte(fmt.Sprintf(`
server: {listen: "127.0.0.1:8443", tls: {certificate: %q, private_key: %q}}
storage: {certificate_dir: %q, trust_ca_file: %q}
certificates:
  - name: web.example.internal
    expected_dns_sans: [web.example.internal]
    expected_ip_sans: ["192.0.2.10"]
    issuer_certificate: %q
`, caFile, tlsKeyFile, store, caFile, issuerFile)))
	const name = "web.example.internal"
	keyPath := filepath.Join(base, "issuer.key")
	renewal := write("renewal.yaml", []byte(fmt.Sprintf(`
issuer_certificate: %q
issuer_private_key: %q
issuer_chain: %q
profiles:
  - {name: web.example.internal, validity: "720h", renew_before: "168h"}
`, issuerFile, keyPath, issuerFile)))
	run := func(preview bool, want string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		args := []string{"--config", server, "--renewal-config", renewal}
		if preview {
			args = append(args, "--dry-run")
		}
		if code := runRenew(t.Context(), args, &stdout, &stderr); code != 0 ||
			!strings.HasPrefix(stdout.String(), name+"\t"+want+"\t") {
			t.Fatalf("renew exit %d: stdout=%q stderr=%q", code, &stdout, &stderr)
		}
	}
	run(true, "issue")
	if _, err := os.Lstat(filepath.Join(store, name)); !os.IsNotExist(err) {
		t.Fatal("dry-run created a generation")
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(signingKey)})
	write("issuer.key", keyPEM)
	issuer, err := pki.NewIssuer(issuerPEM, keyPEM, issuerPEM)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadServer(server)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.IssueServer(name, cfg.Policies[name], now, 61*24*time.Hour); err == nil {
		t.Fatal("issuer accepted a validity period extending beyond its certificate")
	}
	old, err := issuer.IssueServer(name, cfg.Policies[name], now.Add(-2*time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := storage.Publish(store, name, cfg.Policies[name], old, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	run(true, "renew")
	if useSystemd {
		runRenewalTimer(t, base, store, server, renewal)
	} else {
		run(false, "renew")
	}
	run(false, "unchanged")
	current, err := storage.Load(store, cfg.Policies)
	if err != nil || len(current) != 1 || current[0].Version == oldID {
		t.Fatalf("new generation not published: %d %v", len(current), err)
	}
	if bytes.Equal(current[0].Bundle.PrivateKeyPEM, old.PrivateKeyPEM) || bytes.Equal(current[0].Bundle.PrivateKeyPEM, keyPEM) {
		t.Fatal("renewed generation must have its own private key")
	}
	if _, err := pki.Validate(current[0].Bundle, cfg.Policies[name], time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.PublishIfCurrent(store, name, cfg.Policies[name], current[0].Bundle, time.Now(), oldID); err == nil {
		t.Fatal("stale renewal replaced a newer generation")
	}
	t.Run("encrypted intermediate", func(t *testing.T) {
		if _, err := exec.LookPath("openssl"); err != nil {
			t.Skip("OpenSSL unavailable")
		}
		const password = "temporary-test-passphrase"
		encryptedKey := filepath.Join(base, "encrypted.key")
		cmd := exec.CommandContext(t.Context(), "openssl", "pkcs8", "-topk8",
			"-in", keyPath, "-out", encryptedKey, "-v2", "aes-256-cbc", "-passout", "stdin")
		cmd.Stdin = strings.NewReader(password + "\n")
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(encryptedKey, 0600); err != nil {
			t.Fatal(err)
		}
		passPath := write("issuer.passphrase", []byte(password+"\n"))
		cfgEncrypted := config.Renewal{
			CertificateFile: issuerFile, KeyFile: encryptedKey, ChainFile: issuerFile, PassphraseFile: passPath,
		}
		protectedIssuer, err := cfgEncrypted.LoadIssuer(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := protectedIssuer.IssueServer(name, cfg.Policies[name], time.Now(), time.Hour); err != nil {
			t.Fatal(err)
		}
		cfgEncrypted.PassphraseFile = write("wrong.passphrase", []byte("incorrect-secret\n"))
		if _, err := cfgEncrypted.LoadIssuer(t.Context()); err == nil || strings.Contains(err.Error(), "incorrect-secret") {
			t.Fatal("wrong passphrase was accepted or disclosed")
		}
	})
}

func runRenewalTimer(t *testing.T, base, store, server, renewal string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	unit := filepath.Base(base)
	result := filepath.Join(store, ".timer-result")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "systemd-run", "--unit="+unit, "--collect", "--on-active=1s",
		"--timer-property=AccuracySec=100ms", "--property=UMask=0077", "--property=RuntimeMaxSec=15s",
		"--property=NoNewPrivileges=true", "--property=ProtectSystem=strict", "--property=ProtectHome=true",
		"--property=PrivateTmp=true", "--property=ReadWritePaths="+store,
		"--setenv=CERTPOUCH_RENEW_TEST_SERVER="+server, "--setenv=CERTPOUCH_RENEW_TEST_CONFIG="+renewal,
		"--setenv=CERTPOUCH_RENEW_TEST_RESULT="+result, executable, "-test.run=^TestRenewalTimerProcess$")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start isolated renewal timer: %v: %s", err, output)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = exec.CommandContext(cleanup, "systemctl", "stop", "--", unit+".timer", unit+".service").Run()
	}()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("systemd did not complete renewal")
		case <-ticker.C:
			body, err := os.ReadFile(result)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(body), "0\nweb.example.internal\trenew\t") {
				t.Fatalf("unexpected renewal timer result: %q", body)
			}
			return
		}
	}
}

// TestRenewalTimerProcess runs under the isolated timer with production-like restrictions.
func TestRenewalTimerProcess(t *testing.T) {
	server, renewal, result := os.Getenv("CERTPOUCH_RENEW_TEST_SERVER"), os.Getenv("CERTPOUCH_RENEW_TEST_CONFIG"), os.Getenv("CERTPOUCH_RENEW_TEST_RESULT")
	if server == "" || renewal == "" || result == "" {
		t.Skip("only used by the renewal timer")
	}
	var stdout, stderr bytes.Buffer
	code := runRenew(t.Context(), []string{"--config", server, "--renewal-config", renewal}, &stdout, &stderr)
	tmp := result + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d\n%s%s", code, &stdout, &stderr)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, result); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("renewal exited with status %d", code)
	}
}

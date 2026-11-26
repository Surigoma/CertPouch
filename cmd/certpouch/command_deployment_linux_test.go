package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/deployment"
	"github.com/Surigoma/CertPouch/internal/pki"
)

func TestCommandDeploymentAndRollback(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "certificates")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	material := workflowCertificates(t)
	ca := filepath.Join(base, "ca.pem")
	if err := os.WriteFile(ca, material.caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(base, "reload-test")
	if err := os.WriteFile(command, []byte(`#!/bin/sh
set -eu
version=$(basename "$(readlink "$1/current")")
printf '%s\t%s\n' "$version" "$3" >> "$2"
test "$version" != "$4"
`), 0700); err != nil {
		t.Fatal(err)
	}
	first, second := strings.Repeat("a", 32), strings.Repeat("b", 32)
	logPath := filepath.Join(base, "reload-log")
	const literal = "with spaces; $(echo example)"
	path := filepath.Join(base, "client.yaml")
	yaml := fmt.Sprintf(`validation: {trust_ca_file: %q}
certificates:
  - name: web.example.internal
    destination: %q
    expected_dns_sans: [web.example.internal]
    issuer_certificate: %q
    command:
      args: [%q, %q, %q, %q, %q]
      timeout: 1s
`, ca, dest, ca, command, dest, logPath, literal, second)
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadLocalClient(path)
	if err != nil {
		t.Fatal(err)
	}
	session, apply, err := beginTarget(t.Context(), cfg.Targets[0])
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	install := func(version string, pair workflowKeyPair) error {
		return session.Install(t.Context(), version, pki.Bundle{CertificatePEM: pair.cert, PrivateKeyPEM: pair.key}, apply)
	}
	if err := install(first, material.first); err != nil {
		t.Fatal(err)
	}
	if err := install(second, material.second); err == nil || !strings.Contains(err.Error(), "run deployment command") {
		t.Fatalf("expected command failure: %v", err)
	}
	current, err := os.Readlink(filepath.Join(dest, "current"))
	if err != nil || filepath.Base(current) != first {
		t.Fatalf("first generation was not restored: %q, %v", current, err)
	}
	log, err := os.ReadFile(logPath)
	want := fmt.Sprintf("%s\t%s\n%s\t%s\n%s\t%s\n", first, literal, second, literal, first, literal)
	if err != nil || string(log) != want {
		t.Fatalf("unexpected commands: %q, %v", log, err)
	}
	if err := os.WriteFile(path, []byte(yaml+"    service: {unit: nginx.service, action: reload}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadLocalClient(path); err == nil {
		t.Fatal("accepted both command and service")
	}
}

func TestDeploymentCommandTimeout(t *testing.T) {
	_, apply, err := deployment.ConfigureCommand(t.TempDir(), "", "", "", "", []string{"/bin/sleep", "10"}, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := apply(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected command deadline: %v", err)
	}
}

func TestDeploymentRollsBackWhenTLSStillServesPreviousCertificate(t *testing.T) {
	material := workflowCertificates(t)
	roots := x509.NewCertPool()
	roots.AddCert(material.ca)
	consumer := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	consumer.TLS = &tls.Config{Certificates: []tls.Certificate{material.first.tlsPair(t)}}
	consumer.StartTLS()
	defer consumer.Close()
	destination := t.TempDir()
	session, apply, err := beginTarget(t.Context(), config.Target{
		Destination: destination, Policy: pki.Policy{Roots: roots},
		TLSAddress: consumer.Listener.Addr().String(), TLSServerName: "web.example.internal",
		TLSVerifyTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	const first, second = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	install := func(version string, pair workflowKeyPair) error {
		return session.Install(t.Context(), version, pki.Bundle{CertificatePEM: pair.cert, PrivateKeyPEM: pair.key}, apply)
	}
	if err := install(first, material.first); err != nil {
		t.Fatal(err)
	}
	if err := install(second, material.second); err == nil || !strings.Contains(err.Error(), "different leaf certificate") {
		t.Fatalf("expected TLS verification failure: %v", err)
	}
	current, err := os.Readlink(filepath.Join(destination, "current"))
	if err != nil || filepath.Base(current) != first {
		t.Fatalf("TLS verification failure changed current to %q: %v", current, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "versions", second)); !os.IsNotExist(err) {
		t.Fatalf("failed generation was not removed: %v", err)
	}
}

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicationStatusUsesOnlyPublicCertificates(t *testing.T) {
	store := t.TempDir()
	if err := os.Chmod(store, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		name       string
		start, end time.Duration
	}{
		{"healthy.example.internal", -time.Hour, 72 * time.Hour},
		{"due.example.internal", -time.Hour, 24 * time.Hour},
		{"expired.example.internal", -2 * time.Hour, -time.Hour},
		{"future.example.internal", time.Hour, 72 * time.Hour},
	} {
		version := strings.Repeat("a", 32)
		path := filepath.Join(store, entry.name, "versions", version)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(1), NotBefore: now.Add(entry.start), NotAfter: now.Add(entry.end),
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, public, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "certificate.pem"),
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("versions", version), filepath.Join(store, entry.name, "current")); err != nil {
			if os.IsPermission(err) {
				t.Skipf("creating symlinks requires privileges: %v", err)
			}
			t.Fatal(err)
		}
	}
	config := filepath.Join(t.TempDir(), "server.yaml")
	body := fmt.Sprintf("storage: {certificate_dir: %q}\ncertificates:\n", store)
	for _, prefix := range []string{"missing", "healthy", "future", "expired", "due"} {
		body += "  - name: " + prefix + ".example.internal\n"
	}
	if err := os.WriteFile(config, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runPublicationStatus([]string{"--config", config, "--expires-within", "24h"}, &stdout, &stderr, now); code != 1 {
		t.Fatalf("expected warning exit status: %d %s", code, &stderr)
	}
	var items []publicationStatus
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	want := []string{"expiring", "expired", "not_yet_valid", "valid", "missing"}
	if len(items) != len(want) {
		t.Fatalf("unexpected report size: %d", len(items))
	}
	for i, item := range items {
		if item.State != want[i] || (item.State != "missing" && (item.Fingerprint == "" || item.NotAfter == nil)) {
			t.Fatalf("unexpected publication status: %+v", item)
		}
	}
}

package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Surigoma/CertPouch/internal/config"
)

func TestConfigureDNSInline(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "config")
	cache := filepath.Join(base, "cache.json")
	var stdout, stderr bytes.Buffer
	code := runConfigure([]string{"dns",
		"--out", out, "--cache", cache, "--zone", "example.internal",
		"--fallback-prefix", "192.0.2", "--primary", "ns.example.internal",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("configure exit %d: %s", code, &stderr)
	}
	path := filepath.Join(out, "server.yaml")
	if strings.TrimSpace(stdout.String()) != path {
		t.Fatalf("unexpected output %q", &stdout)
	}
	if err := config.ValidateDNSConfig(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("DNS config mode: %v", info.Mode().Perm())
	}
	if code := runConfigure([]string{"dns",
		"--out", out, "--cache", cache, "--zone", "example.internal",
		"--fallback-prefix", "192.0.2", "--primary", "ns.example.internal",
	}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("configure overwrote an existing directory")
	}
}

func TestConfigureDNSInteractive(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "interactive")
	cache := filepath.Join(base, "cache.json")
	input := strings.Join([]string{
		out, cache, "example.internal", "192.0.2", "ns.example.internal",
		"", "", "n", "",
	}, "\n")
	var stdout, stderr bytes.Buffer
	code := runConfigure([]string{"dns", "--interactive"}, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("interactive configure exit %d: %s", code, &stderr)
	}
	if err := config.ValidateDNSConfig(filepath.Join(out, "server.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureDNSWithSheets(t *testing.T) {
	base := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "test@example.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
		"token_uri":   "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(base, "service-account.json")
	if err := os.WriteFile(credentialPath, credentials, 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(base, "config")
	var stdout, stderr bytes.Buffer
	code := runConfigure([]string{"dns",
		"--out", out, "--cache", filepath.Join(base, "cache.json"),
		"--zone", "example.internal", "--fallback-prefix", "192.0.2",
		"--primary", "ns.example.internal", "--spreadsheet-id", "spreadsheet_test",
		"--service-account", credentialPath, "--range", "'Inventory'!A2:G256",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("configure with Sheets exit %d: %s", code, &stderr)
	}
	path := filepath.Join(out, "server.yaml")
	if _, err := config.LoadSheetsFetch(path); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 || entries[0].Name() != "server.yaml" {
		t.Fatalf("generated files = %v, %v; want only server.yaml", entries, err)
	}
}

func TestConfigureDNSRejectsInvalidSettingsWithoutLeavingFiles(t *testing.T) {
	out := filepath.Join(t.TempDir(), "invalid")
	var stdout, stderr bytes.Buffer
	code := runConfigure([]string{"dns",
		"--out", out, "--cache", filepath.Join(t.TempDir(), "cache.json"),
		"--zone", "example.internal", "--fallback-prefix", "192.0.2",
		"--primary", "invalid name",
	}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("configure accepted an invalid primary")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("failed configure left output directory: %v", err)
	}
}

func TestConfigureServerAndClientUseInitCommands(t *testing.T) {
	for _, role := range []string{"server", "client"} {
		var stdout, stderr bytes.Buffer
		if code := runConfigure([]string{role, "--help"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("configure %s --help exit %d: %s", role, code, &stderr)
		}
	}
}

package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Surigoma/CertPouch/internal/pki"
)

func TestLoadCurrent(t *testing.T) {
	root := t.TempDir()
	const name = "web.example.internal"
	version := strings.Repeat("a", 32)
	generation := filepath.Join(root, name, "versions", version)
	if err := os.MkdirAll(generation, 0700); err != nil {
		t.Fatal(err)
	}
	for file, contents := range map[string]string{
		"certificate.pem": "certificate", "chain.pem": "", "private-key.pem": "key",
	} {
		if err := os.WriteFile(filepath.Join(generation, file), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	policies := map[string]pki.Policy{name: {}}
	if got, err := Load(root, policies); err != nil || len(got) != 0 {
		t.Fatalf("unpublished certificate: len=%d err=%v", len(got), err)
	}
	if _, err := Load(root, map[string]pki.Policy{"../outside": {}}); err == nil {
		t.Fatal("accepted a certificate name outside the catalog")
	}
	current := filepath.Join(root, name, "current")
	if err := os.Symlink(filepath.Join("versions", version), current); err != nil {
		if os.IsPermission(err) {
			t.Skipf("creating symlinks requires additional privileges: %v", err)
		}
		t.Fatal(err)
	}
	got, err := Load(root, policies)
	if err != nil || len(got) != 1 {
		t.Fatalf("published certificate: len=%d err=%v", len(got), err)
	}
	if got[0].Name != name || got[0].Version != version || !got[0].Current ||
		string(got[0].Bundle.PrivateKeyPEM) != "key" {
		t.Fatal("did not read the named generation")
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(generation, current); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, policies); err == nil {
		t.Fatal("accepted an absolute current link")
	}
}

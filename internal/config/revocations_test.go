package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRevocationsRequiresExplicitLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "revocations.yaml")
	for name, yaml := range map[string]string{
		"valid empty":          "client_ids: []\nclient_certificate_sha256: []",
		"valid entries":        "client_ids: [node-01]\nclient_certificate_sha256: [\"" + strings.Repeat("a", 64) + "\"]",
		"missing fingerprints": "client_ids: []",
		"duplicate client":     "client_ids: [node-01, node-01]\nclient_certificate_sha256: []",
		"bad hash":             "client_ids: []\nclient_certificate_sha256: [token]",
		"unknown field":        "client_ids: []\nclient_certificate_sha256: []\npermit_all: true",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadRevocations(path)
			if (err == nil) != strings.HasPrefix(name, "valid") {
				t.Fatalf("unexpected revocation validation result: %v", err)
			}
		})
	}
}

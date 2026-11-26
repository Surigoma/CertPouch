package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/pki"
)

func TestRenewalContinuesAfterInvalidPublication(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	const broken, healthy = "broken.example.internal", "healthy.example.internal"
	if err := os.WriteFile(filepath.Join(dir, broken), nil, 0600); err != nil {
		t.Fatal(err)
	}
	server := config.Server{CertificateDir: dir, Policies: map[string]pki.Policy{broken: {}, healthy: {}}}
	renewal := config.Renewal{Profiles: []config.RenewalProfile{{Name: broken}, {Name: healthy}}}
	var out bytes.Buffer
	err := renewCertificates(t.Context(), server, renewal, true, &out)
	if err == nil || !strings.Contains(err.Error(), broken) || !strings.Contains(out.String(), healthy+"\tissue") {
		t.Fatalf("output=%q error=%v", out.String(), err)
	}
}

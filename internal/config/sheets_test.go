package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/dns"
)

func TestSheetsCacheStatusWithoutServiceAccount(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "inventory.json")
	config := filepath.Join(root, "fetch.yaml")
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	input := fmt.Sprintf("dns_inventory: {cache_file: %q, max_age: '30m'}\n", cache)
	if err := os.WriteFile(config, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	check := func(want string, at time.Time) {
		t.Helper()
		status, err := ReadSheetsCacheStatus(config, func() time.Time { return at })
		if err != nil || status.State != want {
			t.Fatalf("status = %+v, %v; want %q", status, err, want)
		}
	}
	check("missing", now)
	snapshot := []byte(`{"valueRanges":[]}`)
	if err := os.WriteFile(cache, snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	check("untracked", now)
	record := fmt.Sprintf(`{"fetched_at":%q,"sha256":%q}`, now.Format(time.RFC3339), fmt.Sprintf("%x", sha256.Sum256(snapshot)))
	if err := os.WriteFile(cache+dns.CacheFreshnessSuffix, []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	check("verified", now)
	check("stale", now.Add(30*time.Minute))
	if err := os.WriteFile(cache, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	check("invalid", now)
}

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDNSSyncRenderAndStage(t *testing.T) {
	base := t.TempDir()
	cache := filepath.Join(base, "cache.json")
	if err := os.WriteFile(cache, []byte(`{"valueRanges":[{"values":[["","192.0.2","10","app","","","app"]]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(base, "dns.yaml")
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`
dns_inventory:
  cache_file: %q
  fallback_prefix: "192.0.2"
  zone: example.internal
zones:
  primary: ns1.example.net
  mailbox: hostmaster.example.internal
  nameservers: [ns1.example.net]
  ttl: 300
  serial: 1
  refresh: 3600
  retry: 600
  expire: 604800
  negative_ttl: 300
`, cache)), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if code := runDNSSync(t.Context(), []string{"--config", config, "--dry-run"}, &out, &diagnostic); code != 0 {
		t.Fatalf("preview exit %d: %s", code, &diagnostic)
	}
	if !strings.Contains(out.String(), "app.example.internal. IN A 192.0.2.10") ||
		!strings.Contains(out.String(), "10.2.0.192.in-addr.arpa. IN PTR app.pvt.example.internal.") {
		t.Fatalf("unexpected preview: %s", &out)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 2 {
		t.Fatal("preview wrote files")
	}
	t.Run("native NSD validation", func(t *testing.T) {
		if _, err := exec.LookPath("nsd-checkzone"); err != nil {
			t.Skip("nsd-checkzone is not installed")
		}
		out.Reset()
		diagnostic.Reset()
		if code := runDNSSync(t.Context(), []string{"--config", config, "--stage-dir", base}, &out, &diagnostic); code != 0 {
			t.Fatalf("stage exit %d: %s", code, &diagnostic)
		}
		stage := strings.TrimSpace(out.String())
		for _, name := range []string{"example.internal.zone", "2.0.192.in-addr.arpa.zone"} {
			if _, err := os.Stat(filepath.Join(stage, name)); err != nil {
				t.Fatal(err)
			}
		}
	})
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Surigoma/CertPouch/internal/dns"
)

func TestDNSSyncRenderAndStage(t *testing.T) {
	base := t.TempDir()
	cache := filepath.Join(base, "cache.json")
	if err := os.WriteFile(cache, []byte(`{"valueRanges":[{"values":[["","192.0.2","10","app","","","app"]]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(base, "dns.yaml")
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`
inventory:
  cache_file: %q
  fallback_prefix: "192.0.2"
dns:
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
    static_records:
      - {owner: "_policy.example.internal", type: "TXT", value: "policy=private; version=1"}
`, cache)), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if code := runDNSSync(t.Context(), []string{"--config", config, "--dry-run"}, &out, &diagnostic); code != 0 {
		t.Fatalf("preview exit %d: %s", code, &diagnostic)
	}
	if !strings.Contains(out.String(), "app.example.internal. IN A 192.0.2.10") ||
		!strings.Contains(out.String(), "10.2.0.192.in-addr.arpa. IN PTR app.pvt.example.internal.") ||
		!strings.Contains(out.String(), "_policy.example.internal. IN TXT \"policy=private; version=1\"") {
		t.Fatalf("unexpected preview: %s", &out)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 2 {
		t.Fatal("preview wrote files")
	}
	t.Run("configuration without a serial", func(t *testing.T) {
		raw, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		noSerial := filepath.Join(t.TempDir(), "dns.yaml")
		if err := os.WriteFile(noSerial, bytes.Replace(raw, []byte("    serial: 1\n"), nil, 1), 0600); err != nil {
			t.Fatal(err)
		}
		run := func(want int, args ...string) (string, string) {
			t.Helper()
			var out, diagnostic bytes.Buffer
			if code := runDNSSync(t.Context(), append([]string{"--config", noSerial}, args...), &out, &diagnostic); code != want {
				t.Fatalf("%v exit %d, want %d: %s", args, code, want, &diagnostic)
			}
			return out.String(), diagnostic.String()
		}
		out, diagnostic := run(0, "--dry-run")
		if !strings.Contains(out, "( 1 3600") || !strings.Contains(diagnostic, "preview uses serial 1") {
			t.Fatalf("preview without serial: %q %q", out, diagnostic)
		}
		if out, diagnostic := run(0, "--dry-run", "--serial", "2026112801"); !strings.Contains(out, "( 2026112801 3600") || diagnostic != "" {
			t.Fatalf("explicit serial preview: %q %q", out, diagnostic)
		}
		if _, diagnostic := run(1, "--stage-dir", t.TempDir()); !strings.Contains(diagnostic, "pass --serial N") {
			t.Fatalf("staging without serial: %q", diagnostic)
		}
		if _, diagnostic := run(1, "--dry-run", "--serial", "5", "--serial-from", t.TempDir()); !strings.Contains(diagnostic, "only one of") {
			t.Fatalf("two serial flags: %q", diagnostic)
		}
		run(2, "--dry-run", "--serial", "0")
		var stdout, stderr bytes.Buffer
		if code := runDNSSync(t.Context(), []string{"--config", config, "--dry-run", "--serial", "5"}, &stdout, &stderr); code != 1 ||
			!strings.Contains(stderr.String(), "remove them from server.yaml") {
			t.Fatalf("YAML serial with --serial: %d %q", code, &stderr)
		}
	})
	t.Run("advance serials from a staged baseline", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("DNS snapshots use Linux file permissions")
		}
		baseline := t.TempDir()
		for zone, serial := range map[string]uint32{"example.internal": 10, "2.0.192.in-addr.arpa": 20} {
			body := fmt.Sprintf("$ORIGIN %s.\n$TTL 300\n@ IN SOA ns1.example.net. hostmaster.example.internal. ( %d 3600 600 604800 300 )\n", zone, serial)
			if err := os.WriteFile(filepath.Join(baseline, zone+".zone"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		args := []string{"--config", config, "--dry-run", "--serial-from-baseline", baseline}
		var out, diagnostic bytes.Buffer
		if code := runDNSSync(t.Context(), args, &out, &diagnostic); code != 1 {
			t.Fatalf("sync accepted two serial sources: %d", code)
		}
		raw, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		configWithoutSerial := filepath.Join(t.TempDir(), "dns.yaml")
		if err := os.WriteFile(configWithoutSerial, bytes.Replace(raw, []byte("    serial: 1\n"), nil, 1), 0600); err != nil {
			t.Fatal(err)
		}
		args[1] = configWithoutSerial
		diagnostic.Reset()
		if code := runDNSSync(t.Context(), args, &out, &diagnostic); code != 0 {
			t.Fatalf("sync exit %d: %s", code, &diagnostic)
		}
		if !strings.Contains(out.String(), "( 11 3600") || !strings.Contains(out.String(), "( 21 3600") {
			t.Fatalf("unexpected zone serials: %s", &out)
		}
	})
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

func TestDNSDiffBeforeInitialization(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("DNS snapshots use Linux file permissions")
	}
	baseline, candidate := t.TempDir(), t.TempDir()
	writeZone := func(dir, zone string, serial int, record string) {
		t.Helper()
		body := fmt.Sprintf("$ORIGIN %s.\n$TTL 300\n@ IN SOA ns.example.net. hostmaster.example.net. ( %d 3600 600 86400 300 )\n%s\n", zone, serial, record)
		if err := os.WriteFile(filepath.Join(dir, zone+".zone"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeZone(baseline, "example.internal", 7, "@ IN NS ns.example.net.")
	writeZone(candidate, "example.internal", 8, "@ IN NS ns.example.net.")
	writeZone(baseline, "old.example.internal", 7, "@ IN TXT \"old\"")
	writeZone(candidate, "new.example.internal", 8, "@ IN TXT \"new\"")

	var out, diagnostic bytes.Buffer
	args := []string{"--baseline", baseline, "--candidate", candidate}
	if code := runDNSDiff(args, &out, &diagnostic); code != 0 {
		t.Fatalf("diff exit %d: %s", code, &diagnostic)
	}
	var changes []dns.ZoneChange
	if err := json.Unmarshal(out.Bytes(), &changes); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || changes[0].State != "serial_only" || changes[0].CurrentSerial == nil ||
		*changes[0].CurrentSerial != 7 || changes[0].CandidateSerial == nil || *changes[0].CandidateSerial != 8 ||
		changes[1].State != "added" || len(changes[1].Added) == 0 ||
		changes[2].State != "removed" || len(changes[2].Removed) == 0 {
		t.Fatalf("unexpected changes: %s", &out)
	}
	if code := runDNSDiff(append(args, "--zone-dir", baseline), &out, &diagnostic); code != 2 {
		t.Fatalf("diff accepted conflicting baseline sources: %d", code)
	}
}

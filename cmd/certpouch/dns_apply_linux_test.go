package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/dns"
)

func TestNativeNSDActivation(t *testing.T) {
	for _, name := range []string{"nsd", "nsd-control", "nsd-checkzone"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip("native NSD tools are not installed")
		}
	}
	base := t.TempDir()
	managed := filepath.Join(base, "managed")
	if err := os.Mkdir(managed, 0700); err != nil {
		t.Fatal(err)
	}
	makeCandidate := func(address string, serial uint32) string {
		t.Helper()
		inventory, err := dns.NewInventory([]dns.Record{{Name: "svc.example.internal", Type: "A", Value: address}})
		if err != nil {
			t.Fatal(err)
		}
		zones, err := dns.RenderZones(inventory, dns.ZoneOptions{
			Forward: "example.internal", Primary: "ns.example.net", Mailbox: "hostmaster.example.internal",
			Nameservers: []string{"ns.example.net"}, TTL: 300, Serial: serial, Refresh: 3600,
			Retry: 600, Expire: 604800, NegativeTTL: 300,
		})
		if err != nil {
			t.Fatal(err)
		}
		path, err := stageDNSZones(t.Context(), base, zones)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	var output, diagnostic bytes.Buffer
	first := makeCandidate("192.0.2.10", 100)
	if code := runDNSApply(t.Context(), []string{"--candidate", first, "--zone-dir", managed, "--initialize"}, &output, &diagnostic); code != 0 {
		t.Fatalf("initialize exit %d: %s", code, &diagnostic)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(base, "nsd.conf")
	body := fmt.Sprintf(`
server:
  ip-address: 127.0.0.1@%d
  chroot: ""
  username: ""
  zonesdir: %q
  database: ""
  pidfile: %q
  zonelistfile: %q
  xfrdfile: %q
remote-control:
  control-enable: yes
  control-interface: %q
zone:
  name: example.internal
  zonefile: "current/example.internal.zone"
zone:
  name: 2.0.192.in-addr.arpa
  zonefile: "current/2.0.192.in-addr.arpa.zone"
`, port, managed, filepath.Join(base, "nsd.pid"), filepath.Join(base, "zone.list"),
		filepath.Join(base, "xfrd.state"), filepath.Join(base, "control.sock"))
	if err := os.WriteFile(conf, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(base, "nsd.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	ctx, cancel := context.WithCancel(t.Context())
	process := exec.CommandContext(ctx, "nsd", "-d", "-c", conf)
	process.Stdout, process.Stderr = log, log
	if err := process.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() {
		_ = exec.Command("nsd-control", "-c", conf, "stop").Run()
		cancel()
		_ = process.Wait()
	}()
	controller, err := exec.LookPath("nsd-control")
	if err != nil {
		t.Fatal(err)
	}
	checker, err := exec.LookPath("nsd-checkzone")
	if err != nil {
		t.Fatal(err)
	}
	control := nsdCommands{checker: checker, controller: controller, config: conf, address: fmt.Sprintf("127.0.0.1:%d", port)}
	deadline := time.Now().Add(5 * time.Second)
	for {
		serial, err := control.ServedSerial(ctx, "example.internal")
		if err == nil && serial == 100 {
			break
		}
		if time.Now().After(deadline) {
			status, _ := exec.Command(controller, "-c", conf, "zonestatus", "example.internal").CombinedOutput()
			t.Fatalf("NSD did not serve initial serial: %v; zonestatus: %q", err, status)
		}
		time.Sleep(25 * time.Millisecond)
	}
	output.Reset()
	diagnostic.Reset()
	second := makeCandidate("192.0.2.11", 101)
	if code := runDNSDiff([]string{"--candidate", second, "--zone-dir", managed}, &output, &diagnostic); code != 0 ||
		!strings.Contains(output.String(), "svc.example.internal. IN A 192.0.2.11") ||
		!strings.Contains(output.String(), "svc.example.internal. IN A 192.0.2.10") {
		t.Fatalf("preview did not show both old and new record: %d, %s, %s", code, &output, &diagnostic)
	}
	output.Reset()
	diagnostic.Reset()
	if code := runDNSApply(ctx, []string{"--candidate", second, "--zone-dir", managed, "--nsd-config", conf, "--dns-address", control.address}, &output, &diagnostic); code != 0 {
		t.Fatalf("apply exit %d: %s", code, &diagnostic)
	}
	if !strings.HasPrefix(output.String(), "generation-") {
		t.Fatalf("apply did not report a new generation: %s", &output)
	}
	for _, zone := range []string{"example.internal", "2.0.192.in-addr.arpa"} {
		if serial, err := control.ServedSerial(ctx, zone); err != nil || serial != 101 {
			t.Fatalf("NSD served serial for %s: %d, %v", zone, serial, err)
		}
	}
	cachePath := filepath.Join(base, "cache.json")
	if err := os.WriteFile(cachePath, []byte(`{"valueRanges":[{"values":[["","192.0.2","11","svc","","","svc"]]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	serialConfig := filepath.Join(base, "serial.yaml")
	serialYAML := fmt.Sprintf(`
dns_inventory: {cache_file: %q, fallback_prefix: "192.0.2", zone: example.internal}
zones:
  primary: ns.example.net
  mailbox: hostmaster.example.internal
  nameservers: [ns.example.net]
  ttl: 300
  refresh: 3600
  retry: 600
  expire: 604800
  negative_ttl: 300
`, cachePath)
	if err := os.WriteFile(serialConfig, []byte(serialYAML), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	diagnostic.Reset()
	if code := runDNSSync(ctx, []string{"--config", serialConfig, "--serial-from", managed, "--dry-run"}, &output, &diagnostic); code != 0 ||
		strings.Count(output.String(), " ( 102 ") != 2 {
		t.Fatalf("preview did not advance both managed serials: %d, %s, %s", code, &output, &diagnostic)
	}
	third := makeCandidate("192.0.2.12", 102)
	if _, err := dns.Activate(ctx, managed, third, false, &failedNSDReload{nsdCommands: control}); err == nil ||
		!strings.Contains(err.Error(), "previous records restored") {
		t.Fatalf("expected recovery after an injected error following a real NSD reload: %v", err)
	}
	for _, zone := range []string{"example.internal", "2.0.192.in-addr.arpa"} {
		if serial, err := control.ServedSerial(ctx, zone); err != nil || serial != 103 {
			t.Fatalf("NSD rollback serial for %s: %d, %v", zone, serial, err)
		}
	}
	forward, err := os.ReadFile(filepath.Join(managed, "current", "example.internal.zone"))
	if err != nil || !bytes.Contains(forward, []byte("IN A 192.0.2.11")) || bytes.Contains(forward, []byte("IN A 192.0.2.12")) {
		t.Fatalf("did not restore the previous forward record: %v", err)
	}
}

type failedNSDReload struct {
	nsdCommands
	failed bool
}

func (c *failedNSDReload) Reload(ctx context.Context, zone string) error {
	if err := c.nsdCommands.Reload(ctx, zone); err != nil {
		return err
	}
	if !c.failed {
		c.failed = true
		return errors.New("injected failure after NSD accepted reload")
	}
	return nil
}

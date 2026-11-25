package dns

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testControl struct {
	root    string
	active  map[string]uint32
	fail    bool
	reloads int
	queries int
}

func (*testControl) Check(context.Context, string, string) error { return nil }

func TestPrepareZonesRollsBackMembership(t *testing.T) {
	root := t.TempDir()
	control := &testControl{root: root, active: make(map[string]uint32)}
	candidate := func(names ...string) string {
		t.Helper()
		dir := t.TempDir()
		for _, name := range names {
			body := "$ORIGIN " + name + ".\n$TTL 300\n@ IN SOA ns.example.net. hostmaster.example.net. ( 1 3600 600 604800 300 )\n"
			if err := os.WriteFile(filepath.Join(dir, name+".zone"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	apply := func(ctx context.Context, names []string) error {
		clear(control.active)
		for _, name := range names {
			if err := control.Reload(ctx, name); err != nil {
				return err
			}
		}
		return nil
	}
	first := candidate("example.internal")
	if err := PrepareZones(t.Context(), root, first, control, apply); err != nil {
		t.Fatal(err)
	}
	before := mustReadCurrent(t, root)
	control.fail = true
	second := candidate("example.internal", "2.0.192.in-addr.arpa")
	if err := PrepareZones(t.Context(), root, second, control, apply); err == nil {
		t.Fatal("expected injected startup failure")
	}
	if mustReadCurrent(t, root) != before || len(control.active) != 1 || control.active["example.internal"] != 1 {
		t.Fatal("previous zone membership was not restored")
	}
	if err := PrepareZones(t.Context(), root, second, control, apply); err != nil {
		t.Fatal(err)
	}
	if len(control.active) != 2 {
		t.Fatal("new zone was not registered")
	}
}

func (c *testControl) Reload(_ context.Context, zone string) error {
	c.reloads++
	if c.fail {
		c.fail = false
		return errors.New("injected reload failure")
	}
	body, err := os.ReadFile(filepath.Join(c.root, "current", zone+".zone"))
	if err != nil {
		return err
	}
	z, err := parseSnapshotZone(zone, body)
	if err == nil {
		c.active[zone] = z.serial
	}
	return err
}

func (c *testControl) ServedSerial(_ context.Context, name string) (uint32, error) {
	c.queries++
	return c.active[name], nil
}

func TestActivateRestoresPreviousRecordsWithNewSerial(t *testing.T) {
	root := t.TempDir()
	control := &testControl{root: root, active: make(map[string]uint32)}
	candidate := func(ip string, serial uint32) string {
		t.Helper()
		dir := t.TempDir()
		inventory, err := NewInventory([]Record{{Name: "app.example.internal", Type: "A", Value: ip}})
		if err != nil {
			t.Fatal(err)
		}
		zones, err := RenderZones(inventory, ZoneOptions{
			Forward: "example.internal", Primary: "ns.example.net", Mailbox: "hostmaster.example.internal",
			Nameservers: []string{"ns.example.net"}, TTL: 300, Serial: serial, Refresh: 3600,
			Retry: 600, Expire: 604800, NegativeTTL: 300,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, zone := range zones {
			if err := os.WriteFile(filepath.Join(dir, zone.Name+".zone"), zone.Contents, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	first := candidate("192.0.2.10", 1)
	if _, err := Activate(t.Context(), root, first, ActivationOptions{Initialize: true}, control); err != nil {
		t.Fatal(err)
	}
	control.active["example.internal"], control.active["2.0.192.in-addr.arpa"] = 1, 1
	zero := 0
	if result, err := Activate(t.Context(), root, candidate("192.0.2.10", 2), ActivationOptions{MaxRemovedRecords: &zero}, control); err != nil || result != "unchanged" || control.reloads != 0 {
		t.Fatalf("unchanged zones should not reload: %s, %v, %d", result, err, control.reloads)
	}
	next := candidate("192.0.2.11", 2)
	before := mustReadCurrent(t, root)
	queries := control.queries
	one := 1
	if _, err := Activate(t.Context(), root, next, ActivationOptions{MaxRemovedRecords: &one}, control); err == nil ||
		!strings.Contains(err.Error(), "replaces 2 DNS records") {
		t.Fatalf("expected the aggregate A and PTR replacement to exceed the limit: %v", err)
	}
	if mustReadCurrent(t, root) != before || control.queries != queries || control.reloads != 0 {
		t.Fatal("a rejected candidate changed the active generation or contacted NSD")
	}
	control.fail = true
	two := 2
	if _, err := Activate(t.Context(), root, next, ActivationOptions{MaxRemovedRecords: &two}, control); err == nil ||
		!strings.Contains(err.Error(), "previous records restored") {
		t.Fatalf("expected successful rollback and failed activation: %v", err)
	}
	active, err := readSnapshot(filepath.Join(root, mustReadCurrent(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range active {
		if zone.serial != 3 || control.active[zone.name] != 3 || strings.Contains(string(zone.body), "192.0.2.11") {
			t.Fatalf("active snapshot not restored: %s", zone.body)
		}
	}
	if _, err := Activate(t.Context(), root, candidate("192.0.2.12", 4), ActivationOptions{}, control); err != nil {
		t.Fatalf("retry with a newer serial: %v", err)
	}
}

func mustReadCurrent(t *testing.T, root string) string {
	t.Helper()
	current, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil {
		t.Fatal(err)
	}
	return current
}

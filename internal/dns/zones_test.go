package dns

import (
	"bytes"
	"testing"
)

func TestRenderZones(t *testing.T) {
	inventory, err := NewInventory([]Record{
		{Name: "app.example.internal", Type: "A", Value: "192.0.2.10"},
		{Name: "app.example.internal", Type: "A", Value: "192.0.2.10"},
		{Name: "app.example.internal", Type: "AAAA", Value: "2001:db8::10"},
		{Name: "app.pvt.example.internal", Type: "A", Value: "192.0.2.10"},
	})
	if err != nil {
		t.Fatal(err)
	}
	options := ZoneOptions{
		Forward: "example.internal", Primary: "ns1.example.net", Mailbox: "hostmaster.example.internal",
		Nameservers: []string{"ns1.example.net"}, TTL: 300, Serial: 4294967295,
		Refresh: 3600, Retry: 600, Expire: 604800, NegativeTTL: 300,
	}
	zones, err := RenderZones(inventory, options)
	if err != nil || len(zones) != 2 {
		t.Fatalf("expected forward and reverse zones: %d, %v", len(zones), err)
	}
	if zones[0].Name != "2.0.192.in-addr.arpa" ||
		bytes.Count(zones[0].Contents, []byte("10.2.0.192.in-addr.arpa. IN PTR ")) != 2 ||
		zones[1].Name != "example.internal" ||
		bytes.Count(zones[1].Contents, []byte("app.example.internal. IN A 192.0.2.10")) != 1 ||
		!bytes.Contains(zones[1].Contents, []byte("app.example.internal. IN AAAA 2001:db8::10")) ||
		!bytes.Contains(zones[1].Contents, []byte("4294967295 3600 600 604800 300")) {
		t.Fatalf("invalid zone files:\n%s\n%s", zones[0].Contents, zones[1].Contents)
	}
	if _, err := RenderZones(Inventory{}, options); err == nil {
		t.Fatal("empty inventory accepted")
	}
	options.AdvanceSerial = true
	options.PreviousSerials = map[string]uint32{"example.internal": 4294967295, "2.0.192.in-addr.arpa": 41}
	zones, err = RenderZones(inventory, options)
	if err != nil || !bytes.Contains(zones[0].Contents, []byte("( 42 ")) || !bytes.Contains(zones[1].Contents, []byte("( 0 ")) {
		t.Fatalf("previous serials were not advanced correctly: %v, %v", zones, err)
	}
	options.AdvanceSerial = false
	if _, err := RenderZones(inventory, options); err == nil {
		t.Fatal("equal or older serial was accepted")
	}
	if newerSerial(1<<31, 0) || newerSerial(0, 1<<31) || !newerSerial(0, 4294967295) {
		t.Fatal("invalid RFC 1982 serial comparison")
	}
	options.Forward = "other.example.internal"
	if _, err := RenderZones(inventory, options); err == nil {
		t.Fatal("out-of-zone inventory accepted")
	}
}

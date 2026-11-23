package dns

import (
	"bytes"
	"testing"
)

func TestStaticRecordsMergeWithInventory(t *testing.T) {
	inventory, err := NewInventory([]Record{{Name: "app.example.internal", Type: "A", Value: "192.0.2.10"}})
	if err != nil {
		t.Fatal(err)
	}
	const forward = "example.internal"
	priority := uint16(10)
	options := ZoneOptions{
		Forward: forward, Primary: "ns.example.internal", Mailbox: "hostmaster.example.internal",
		Nameservers: []string{"ns.example.internal"}, TTL: 300, Serial: 1, Refresh: 3600,
		Retry: 600, Expire: 604800, NegativeTTL: 300,
		StaticRecords: []StaticRecord{
			{Owner: "ns.example.internal.", Type: "A", Value: "192.0.2.53"},
			{Owner: "www.example.internal.", Type: "CNAME", Value: "app.example.internal."},
			{Owner: "_policy.example.internal.", Type: "TXT", Value: "status=ready; state=active"},
			{Owner: "@", Type: "MX", Priority: &priority, Value: "mail.example.net."},
			{Zone: "2.0.192.in-addr.arpa", Owner: "53.2.0.192.in-addr.arpa.", Type: "PTR", Value: "ns.example.internal."},
		},
	}
	zones, err := RenderZones(inventory, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"app.example.internal. IN A 192.0.2.10",
		"www.example.internal. IN CNAME app.example.internal.",
		"_policy.example.internal. IN TXT \"status=ready; state=active\"",
		"example.internal. IN MX 10 mail.example.net.",
	} {
		if !bytes.Contains(zones[1].Contents, []byte(line)) {
			t.Fatalf("expected forward record: %q", line)
		}
	}
	if !bytes.Contains(zones[0].Contents, []byte("53.2.0.192.in-addr.arpa. IN PTR ns.example.internal.")) {
		t.Fatal("expected static PTR")
	}
	baseline := options.StaticRecords
	for _, extra := range []StaticRecord{
		{Owner: "app.example.internal", Type: "CNAME", Value: "www.example.internal"},
		{Owner: "loop.example.internal", Type: "CNAME", Value: "loop.example.internal"},
		{Owner: "outside.example.net", Type: "TXT", Value: "invalid"},
		{Owner: "test.example.internal", Type: "TXT", Value: "injected\n@"},
		{Owner: "test.example.internal..", Type: "TXT", Value: "invalid"},
	} {
		options.StaticRecords = append(append([]StaticRecord(nil), baseline...), extra)
		if _, err := RenderZones(inventory, options); err == nil {
			t.Fatalf("accepted invalid static record: %+v", extra)
		}
	}
}

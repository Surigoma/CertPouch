package dns

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestReadLegacyCache(t *testing.T) {
	cache := `{"valueRanges":[
		{"values":[["","192.0.2","1"],["","","10","web","",""," app, db "],["skip","","11","ignored"]]},
		{"values":[[],["","","20","fallback"]]}
	]}`
	got, err := ReadLegacyCache(strings.NewReader(cache), "198.51.100", "example.internal.")
	if err != nil {
		t.Fatal(err)
	}
	prefixes, err := got.AllowedPrefixes([]string{"web.pvt.example.internal", "app.example.internal", "db.example.internal", "fallback.pvt.example.internal"})
	want := []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32"), netip.MustParsePrefix("198.51.100.20/32")}
	if err != nil || !slices.Equal(prefixes, want) {
		t.Fatalf("legacy inventory = %v, %v; want %v", prefixes, err, want)
	}
	if _, err := got.AllowedPrefixes([]string{"ignored.pvt.example.internal"}); err == nil {
		t.Fatal("included a row marked to be skipped")
	}
}

func TestAllowedPrefixes(t *testing.T) {
	inventory, err := NewInventory([]Record{
		{Name: "WEB.example.internal.", Type: "A", Value: "192.0.2.10"},
		{Name: "web.example.internal", Type: "AAAA", Value: "2001:db8::10"},
		{Name: "web.example.internal", Type: "A", Value: "192.0.2.10"},
		{Name: "alias.example.internal", Type: "CNAME", Value: "web.example.internal."},
		{Name: "other.example.internal", Type: "A", Value: "192.0.2.11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts := []string{"web.example.internal"}
	got, err := inventory.AllowedPrefixes(hosts)
	want := []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32"), netip.MustParsePrefix("2001:db8::10/128")}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("AllowedPrefixes = %v, %v; want %v", got, err, want)
	}
	if got, err := inventory.AllowedPrefixes(append(hosts, "alias.example.internal")); err == nil || len(got) != 0 {
		t.Fatal("a host without explicit address records must reject the entire assignment")
	}
	if _, err := NewInventory([]Record{
		{Name: "web.example.internal", Type: "AAAA", Value: "192.0.2.10"},
	}); err == nil {
		t.Fatal("accepted an IPv4 address in an AAAA record")
	}
}

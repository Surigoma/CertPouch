package config

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Surigoma/CertPouch/internal/dns"
	"github.com/Surigoma/CertPouch/internal/pki"
)

func TestRenewalProfilesFromInventory(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "inventory.json")
	path := filepath.Join(dir, "server.yaml")
	if err := os.WriteFile(cache, []byte(`{"valueRanges":[{"values":[["","192.0.2","10","web01","","","portal"]]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`storage: {certificate_dir: %q}
dns_inventory: {cache_file: %q, fallback_prefix: "192.0.2", zone: "example.internal"}
renewal: {from_inventory: true, validity: "720h", renew_before: "168h", issuer_certificate: %q, issuer_private_key: %q}
`, dir, cache, filepath.Join(dir, "issuer.pem"), filepath.Join(dir, "issuer.key"))
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRenewal(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != 2 || cfg.Profiles[0].Name != "portal.example.internal" ||
		cfg.Profiles[1].Name != "web01.pvt.example.internal" {
		t.Fatalf("unexpected profiles: %+v", cfg.Profiles)
	}
	_, names, err := PublicationLocations(path)
	if err != nil || !slices.Equal(names, []string{cfg.Profiles[0].Name, cfg.Profiles[1].Name}) {
		t.Fatalf("status and renewal differ: %v, %v", names, err)
	}
}

func TestDecodeWorkflowExample(t *testing.T) {
	body, err := os.ReadFile("../../configs/workflow-server.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodeServer(body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Deployment == nil || doc.Deployment.NSDInclude == "" || len(doc.Sheets.Ranges) != 2 ||
		len(doc.Zones.ReverseZones) != 1 || !doc.Renewal.FromInventory || len(doc.Clients) != 1 {
		t.Fatal("workflow example did not map to the operational configuration")
	}
}

func TestCertificateSourceRules(t *testing.T) {
	inventory, err := dns.NewInventory([]dns.Record{
		{Name: "web.example.internal", Type: "A", Value: "192.0.2.10"},
		{Name: "backup.example.internal", Type: "A", Value: "192.0.2.12"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hashFile := filepath.Join(t.TempDir(), "client.sha256")
	if err := os.WriteFile(hashFile, []byte(strings.Repeat("a", 64)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	doc := clientDocument{ID: "host", Enabled: true, TokenHashFile: hashFile}
	const web = "web.example.internal"
	const absent = "absent.example.internal"
	policies := map[string]pki.Policy{web: {}, absent: {}}
	client, err := doc.client(policies, &inventory)
	want := []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")}
	if err != nil || !slices.Equal(client.CertificateIPs[web], want) || len(client.CertificateIPs[absent]) != 0 {
		t.Fatalf("unexpected default rules: %v, %v", client.CertificateIPs, err)
	}
	doc.Overrides = map[string][]string{web: {"backup.example.internal"}}
	client, err = doc.client(policies, &inventory)
	want = append(want, netip.MustParsePrefix("192.0.2.12/32"))
	if err != nil || !slices.Equal(client.CertificateIPs[web], want) {
		t.Fatalf("unexpected override rules: %v, %v", client.CertificateIPs, err)
	}
	doc.Overrides[web] = []string{"missing.example.internal"}
	if _, err := doc.client(policies, &inventory); err == nil {
		t.Fatal("accepted an override pointing to an unknown host")
	}
}

func TestInventoryAccessModes(t *testing.T) {
	const web = "web.example.internal"
	inventory, err := dns.NewInventory([]dns.Record{{Name: web, Type: "A", Value: "192.0.2.10"}})
	if err != nil {
		t.Fatal(err)
	}
	policies := map[string]pki.Policy{web: {}}
	for _, mode := range []string{"names", "ip_and_names", "all"} {
		doc := clientDocument{InventoryAccess: mode}
		if mode != "all" {
			doc.AllowedCertificates = []string{web}
		}
		rules, err := doc.inventoryRules(policies, inventory)
		if err != nil {
			t.Fatal(err)
		}
		matches := slices.ContainsFunc(rules[web], func(prefix netip.Prefix) bool {
			return prefix.Contains(netip.MustParseAddr("198.51.100.10"))
		})
		if matches != (mode != "ip_and_names") {
			t.Fatalf("%s: unexpected rules %v", mode, rules)
		}
	}
}

func TestDecodeServer(t *testing.T) {
	body, err := os.ReadFile("../../configs/server.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodeServer(body)
	if err != nil || doc.Server.Listen != "127.0.0.1:8443" || len(doc.Clients) != 1 {
		t.Fatalf("example did not decode: %v", err)
	}
	for name, input := range map[string]string{
		"unknown key":               "server:\n  listen: ':8443'\n  unexpected: true\n",
		"duplicate key":             "server:\n  listen: ':8443'\n  listen: ':9443'\n",
		"wrong type":                "clients: [{allowed_ips: true}]",
		"extra document":            "server: {}\n---\nclients: []\n",
		"malformed sensitive value": "token: [should-not-be-disclosed",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeServer([]byte(input))
			if err == nil || strings.Contains(err.Error(), "should-not-be-disclosed") {
				t.Fatal("invalid config was accepted or its contents were disclosed")
			}
		})
	}
}

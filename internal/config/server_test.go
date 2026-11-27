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

func TestInventoryRefreshIsolatesDeletionsAndKeepsStartupCredentials(t *testing.T) {
	dir := t.TempDir()
	ca := writeTestIssuer(t)
	cache, hash, path := filepath.Join(dir, "inventory.json"), filepath.Join(dir, "client.sha256"), filepath.Join(dir, "server.yaml")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(hash, strings.Repeat("a", 64))
	write(cache, `{"valueRanges":[{"values":[["","192.0.2","10","web","","","portal"],["","192.0.2","12","backup"]]}]}`)
	write(path, fmt.Sprintf(`server:
  listen: "127.0.0.1:8443"
  tls: {certificate: %q, private_key: %q}
  publication_refresh: "5s"
  inventory_refresh: "5s"
storage: {certificate_dir: %q, trust_ca_file: %q}
dns_inventory: {cache_file: %q, fallback_prefix: "192.0.2", zone: "example.internal"}
renewal: {from_inventory: true, validity: "720h", renew_before: "168h", issuer_certificate: %q, issuer_private_key: %q}
clients:
  - id: test
    enabled: true
    token_hash_file: %q
    certificate_access_overrides:
      portal.example.internal: [web.pvt.example.internal]
      web.pvt.example.internal: [backup.pvt.example.internal, WEB.pvt.example.internal.]
`, ca, hash, dir, ca, cache, ca, filepath.Join(dir, "issuer.key"), hash))
	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatal(err)
	}
	write(cache, `{"valueRanges":[{"values":[["","192.0.2","11","web"]]}]}`)
	write(hash, strings.Repeat("b", 64))
	policies, clients, err := cfg.ReloadInventoryPublications()
	if err != nil {
		t.Fatal(err)
	}
	const remaining = "web.pvt.example.internal"
	if len(policies) != 1 || len(clients[0].CertificateIPs) != 1 ||
		len(clients[0].CertificateIPs[remaining]) == 0 || clients[0].TokenHash != cfg.Clients[0].TokenHash {
		t.Fatalf("unexpected refreshed policies or authorization: %v", clients[0].CertificateIPs)
	}
	for _, prefix := range clients[0].CertificateIPs[remaining] {
		if prefix != netip.MustParsePrefix("192.0.2.11/32") {
			t.Fatalf("unexpected prefix after refresh: %v", prefix)
		}
	}
	restarted, profiles, err := LoadRenewalInputs(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles.Profiles) != 1 || profiles.Profiles[0].Name != remaining ||
		restarted.Clients[0].TokenHash == cfg.Clients[0].TokenHash {
		t.Fatal("reloading the configuration did not apply the new credentials and remaining renewal target")
	}
}

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
	client, err := doc.client(policies, &inventory, false)
	want := []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")}
	if err != nil || !slices.Equal(client.CertificateIPs[web], want) || len(client.CertificateIPs[absent]) != 0 {
		t.Fatalf("unexpected default rules: %v, %v", client.CertificateIPs, err)
	}
	doc.Overrides = map[string][]string{web: {"backup.example.internal"}}
	client, err = doc.client(policies, &inventory, false)
	want = append(want, netip.MustParsePrefix("192.0.2.12/32"))
	if err != nil || !slices.Equal(client.CertificateIPs[web], want) {
		t.Fatalf("unexpected override rules: %v, %v", client.CertificateIPs, err)
	}
	doc.Overrides[web] = []string{"missing.example.internal"}
	if _, err := doc.client(policies, &inventory, false); err == nil {
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
		rules, err := doc.inventoryRules(policies, inventory, false)
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

func TestLoadServerRejectsUnsafeSettings(t *testing.T) {
	ca := writeTestIssuer(t)
	dir := t.TempDir()
	key := filepath.Join(dir, "listener.key")
	if err := os.WriteFile(key, []byte("test placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	badCA := filepath.Join(dir, "invalid.pem")
	if err := os.WriteFile(badCA, []byte("test placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`server:
  listen: "127.0.0.1:8443"
  tls: {certificate: %q, private_key: %q}
storage: {certificate_dir: %q, trust_ca_file: %q}
`, ca, key, dir, ca)
	const first = "  - {name: web.example.internal, issuer_certificate: %q}\n"
	tests := []struct {
		name, config, want string
	}{
		{"invalid listen port", strings.Replace(body, "8443", "0", 1), "valid host and port"},
		{"relative private key", strings.Replace(body, fmt.Sprintf("%q", key), "listener.key", 1), "paths must be absolute"},
		{"missing private key", strings.Replace(body, fmt.Sprintf("%q", key), fmt.Sprintf("%q", filepath.Join(dir, "missing")), 1), "check TLS private key"},
		{"invalid trust bundle", strings.Replace(body, "trust_ca_file: "+fmt.Sprintf("%q", ca), "trust_ca_file: "+fmt.Sprintf("%q", badCA), 1), "contains no certificates"},
		{"relative client CA", strings.Replace(body, "private_key: "+fmt.Sprintf("%q", key), "private_key: "+fmt.Sprintf("%q", key)+", client_ca: relative.pem", 1), "client_ca must be absolute"},
		{"fast publication refresh", strings.Replace(body, "  tls:", "  publication_refresh: 50ms\n  tls:", 1), "publication_refresh must be a duration of at least one second"},
		{"refresh without inventory", strings.Replace(body, "  tls:", "  inventory_refresh: 1s\n  tls:", 1), "inventory_refresh requires dns_inventory"},
		{"revocation refresh without file", strings.Replace(body, "  tls:", "  revocation_refresh: 1s\n  tls:", 1), "revocation_refresh requires server.revocation_file"},
		{"duplicate certificate names", body + "certificates:\n" + fmt.Sprintf(first+first, ca, ca), "duplicate certificates"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.yaml")
			if err := os.WriteFile(path, []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadServer(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadServer error = %v, want %q", err, tc.want)
			}
		})
	}
}

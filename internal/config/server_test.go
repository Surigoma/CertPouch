package config

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Surigoma/CertPouch/internal/distribution"
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
	body := fmt.Sprintf(`inventory: {cache_file: %q, fallback_prefix: "192.0.2"}
dns: {zone: "example.internal"}
certificates:
  store_dir: %q
  trust_ca_file: %q
  issuer: {certificate_file: %q, private_key_file: %q}
  validity: "720h"
  renew_before: "168h"
distribution:
  auth: ip
  listen: "127.0.0.1:8443"
  tls: {certificate_file: %q, private_key_file: %q}
  clients:
    - id: test
      enabled: true
      auth: token
      token_hash_file: %q
      certificate_access_overrides:
        portal.example.internal: [web.pvt.example.internal]
        web.pvt.example.internal: [backup.pvt.example.internal, WEB.pvt.example.internal.]
    - id: address-only
      enabled: true
      inventory_access: ip_and_names
      allowed_certificates: [web.pvt.example.internal, portal.example.internal]
`, cache, dir, ca, ca, filepath.Join(dir, "issuer.key"), ca, hash, hash)
	write(path, body)
	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Clients) != 3 || !cfg.Clients[2].Implicit || cfg.Clients[2].ID != "" ||
		!slices.Equal(cfg.Clients[2].CertificateIPs["portal.example.internal"], []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")}) ||
		!slices.Equal(cfg.Clients[2].CertificateIPs["backup.pvt.example.internal"], []netip.Prefix{netip.MustParsePrefix("192.0.2.12/32")}) {
		t.Fatalf("auth ip with an inventory did not add the implicit inventory IP client: %+v", cfg.Clients)
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
	if clients[0].Auth != distribution.AuthToken || clients[1].Auth != distribution.AuthIP ||
		len(clients[1].CertificateIPs) != 1 ||
		!slices.Equal(clients[1].CertificateIPs[remaining], []netip.Prefix{netip.MustParsePrefix("192.0.2.11/32")}) {
		t.Fatal("inventory refresh did not preserve the authentication override and updated IP rule")
	}
	for _, prefix := range clients[0].CertificateIPs[remaining] {
		if prefix != netip.MustParsePrefix("192.0.2.11/32") {
			t.Fatalf("unexpected prefix after refresh: %v", prefix)
		}
	}
	if len(clients) != 3 || !clients[2].Implicit || len(clients[2].CertificateIPs) != 1 ||
		!slices.Equal(clients[2].CertificateIPs[remaining], []netip.Prefix{netip.MustParsePrefix("192.0.2.11/32")}) {
		t.Fatalf("inventory refresh did not rebuild the implicit inventory IP client: %+v", clients)
	}
	// With the default token authentication, unregistered hosts get no access.
	write(path, strings.Replace(strings.Replace(body, "  auth: ip\n", "", 1),
		"    - id: address-only\n", "    - id: address-only\n      auth: ip\n", 1))
	tokenDefault, err := LoadServer(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range tokenDefault.Clients {
		if client.Implicit {
			t.Fatal("added an implicit inventory IP client without distribution.auth ip")
		}
	}
	write(path, body)
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
	body := fmt.Sprintf(`inventory: {cache_file: %q, fallback_prefix: "192.0.2"}
dns: {zone: "example.internal"}
certificates:
  store_dir: %q
  issuer: {certificate_file: %q, private_key_file: %q}
  validity: "720h"
  renew_before: "168h"
`, cache, dir, filepath.Join(dir, "issuer.pem"), filepath.Join(dir, "issuer.key"))
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := readServerDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := doc.loadIssuanceInventory()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := renewalForNames(doc.Renewal, inventory.Names())
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

func TestDecodeServerExample(t *testing.T) {
	body, err := os.ReadFile("../../configs/server.example.yaml")
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

func TestDecodeServerRejectsInvalidYAMLWithoutDisclosingValues(t *testing.T) {
	for name, tc := range map[string]struct{ input, want string }{
		"unknown key":               {"distribution:\n  listen: ':8443'\n  unexpected: true\n", `line 3, column 3: unknown field "unexpected"`},
		"duplicate key":             {"distribution:\n  listen: ':8443'\n  listen: ':9443'\n", `line 3, column 3: duplicate key "listen"`},
		"wrong type":                {"distribution: {clients: [{allowed_ips: true}]}", "value has the wrong type"},
		"extra document":            {"dns: {}\n---\ndistribution: {}\n", "exactly one document"},
		"malformed sensitive value": {"token: [should-not-be-disclosed", "invalid YAML"},
		"target timing without key": {"certificates:\n  targets: [{name: web.example.internal, validity: 24h}]\n", "require certificates.issuer.private_key_file"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeServer([]byte(tc.input))
			if err == nil || !strings.Contains(err.Error(), tc.want) ||
				strings.Contains(err.Error(), "should-not-be-disclosed") || strings.Contains(err.Error(), "8443") {
				t.Fatalf("decodeServer error = %v, want %q without values", err, tc.want)
			}
		})
	}
}

func TestDecodeServerRejectsV1Layouts(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"server:\n  listen: ':8443'\n", "top-level server is a CertPouch v1 key; move it to distribution"},
		{"storage: {certificate_dir: /x}\n", "certificates.store_dir"},
		{"dns_inventory: {cache_file: /x}\n", "move it to inventory"},
		{"pipeline: {dns: {}}\n", "top-level pipeline"},
		{"renewal: {}\n", "certificates.issuer"},
		{"clients: []\n", "distribution.clients"},
		{"certificates:\n  - name: web.example.internal\n", "certificates.targets"},
	} {
		if _, err := decodeServer([]byte(tc.input)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("decodeServer(%q) error = %v, want %q", tc.input, err, tc.want)
		}
	}
}

func TestDecodeServerExplicitTargets(t *testing.T) {
	doc, err := decodeServer([]byte(`certificates:
  issuer: {certificate_file: /ca/issuer.pem, private_key_file: /ca/issuer.key}
  validity: 720h
  renew_before: 168h
  targets:
    - {name: web.example.internal}
    - {name: api.example.internal, expected_dns_sans: [api.example.internal], validity: 2160h}
`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Renewal.FromInventory || doc.DNSInventory != nil || len(doc.Certificates) != 2 ||
		doc.Storage.IssuerFile != "/ca/issuer.pem" || len(doc.Renewal.Profiles) != 2 ||
		doc.Renewal.Profiles[0].ValidityText != "720h" || doc.Renewal.Profiles[1].ValidityText != "2160h" ||
		doc.Renewal.Profiles[1].RenewBeforeText != "168h" || doc.Certificates[1].DNS[0] != "api.example.internal" {
		t.Fatalf("unexpected explicit target mapping: %+v", doc)
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
	body := fmt.Sprintf(`distribution:
  listen: "127.0.0.1:8443"
  tls: {certificate_file: %q, private_key_file: %q}
certificates:
  store_dir: %q
  trust_ca_file: %q
`, ca, key, dir, ca)
	const first = "    - {name: web.example.internal, issuer_certificate: %q}\n"
	ipClients := func(entry string) string {
		return strings.Replace(body, "certificates:\n", "  auth: ip\n  clients:\n    - {id: node, enabled: true, "+entry+"}\ncertificates:\n", 1)
	}
	tests := []struct {
		name, config, want string
	}{
		{"invalid listen port", strings.Replace(body, "8443", "0", 1), "distribution.listen must contain a valid host and port"},
		{"unknown default auth", strings.Replace(body, "  tls:", "  auth: unknown\n  tls:", 1), "distribution.auth must be token, mtls, or ip"},
		{"mtls without client CA", strings.Replace(body, "  tls:", "  auth: mtls\n  tls:", 1), "requires distribution.tls.client_ca"},
		{"IP auth with token hash", ipClients("token_hash_file: /key"), "token_hash_file is incompatible with auth ip"},
		{"IP auth with a subnet", ipClients("allowed_ips: [192.0.2.0/24], allowed_certificates: [web.example.internal]") +
			"  targets:\n" + fmt.Sprintf(first, ca), "requires exact source IPs"},
		{"IP auth without certificate assignments", ipClients("allowed_ips: [192.0.2.10]"), "requires allowed_ips and allowed_certificates"},
		{"relative private key", strings.Replace(body, fmt.Sprintf("%q", key), "listener.key", 1), "distribution.tls.private_key_file must be an absolute path"},
		{"relative store", strings.Replace(body, fmt.Sprintf("store_dir: %q", dir), "store_dir: store", 1), "certificates.store_dir must be an absolute path"},
		{"missing private key", strings.Replace(body, fmt.Sprintf("%q", key), fmt.Sprintf("%q", filepath.Join(dir, "missing")), 1), "check distribution.tls.private_key_file"},
		{"invalid trust bundle", strings.Replace(body, "trust_ca_file: "+fmt.Sprintf("%q", ca), "trust_ca_file: "+fmt.Sprintf("%q", badCA), 1), "trust_ca_file contains no certificates"},
		{"relative client CA", strings.Replace(body, "private_key_file: "+fmt.Sprintf("%q", key), "private_key_file: "+fmt.Sprintf("%q", key)+", client_ca: relative.pem", 1), "distribution.tls.client_ca must be an absolute path"},
		{"fast refresh", strings.Replace(body, "  tls:", "  refresh_interval: 50ms\n  tls:", 1), "distribution.refresh_interval must be a duration of at least one second"},
		{"revocation refresh without file", strings.Replace(body, "  tls:", "  revocation_refresh: 1s\n  tls:", 1), "revocation_refresh requires distribution.revocation_file"},
		{"duplicate certificate names", body + "  targets:\n" + fmt.Sprintf(first+first, ca, ca), "duplicate certificates.targets[1].name"},
		{"issuer without inventory", body + fmt.Sprintf("  issuer: {certificate_file: %q}\n", ca), "requires inventory.cache_file or inventory.csv_files"},
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

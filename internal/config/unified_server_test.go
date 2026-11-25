package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPipelineSchemaRejectsMixedAndUnknownKeys(t *testing.T) {
	for _, body := range []string{
		"pipeline: {}\ndns_inventory: {}\n",
		"pipeline: {dns: {deployment: {stage_directory: /tmp}}}\n",
	} {
		if _, err := decodeServer([]byte(body)); err == nil {
			t.Fatalf("accepted invalid configuration: %s", body)
		}
	}
}

func TestUnifiedServerConfigurationIsAcceptedByFeatureLoaders(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "server.yaml")
	body := fmt.Sprintf(`server:
  listen: "127.0.0.1:8443"
storage:
  certificate_dir: "/var/lib/certpouch/certificates"
  trust_ca_file: "/etc/certpouch/issuers.pem"
dns_inventory:
  cache_file: %q
  fallback_prefix: "192.0.2"
  zone: "example.internal"
google_sheets:
  spreadsheet_id: "spreadsheet_test"
  ranges: ["'Inventory'!A2:G256"]
  service_account_file: %q
zones:
  primary: "ns.example.internal"
  mailbox: "hostmaster.example.internal"
  nameservers: ["ns.example.internal"]
  ttl: 300
  serial: 1
  refresh: 3600
  retry: 600
  expire: 604800
  negative_ttl: 300
certificates: []
clients: []
renewal:
  issuer_certificate: %q
  issuer_private_key: %q
  profiles:
    - name: "web.example.internal"
      validity: "2160h"
      renew_before: "720h"
`, filepath.Join(root, "inventory.json"), filepath.Join(root, "service-account.json"),
		filepath.Join(root, "intermediate.crt"), filepath.Join(root, "intermediate.key"))
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDNSConfig(path); err != nil {
		t.Fatalf("DNS loader rejected unified server config: %v", err)
	}
	if _, err := loadSheetsFetchDocument(path); err != nil {
		t.Fatalf("Sheets loader rejected unified server config: %v", err)
	}
	if renewal, err := LoadRenewal(path); err != nil || len(renewal.Profiles) != 1 {
		t.Fatalf("renewal loader = %+v, %v", renewal, err)
	}
	if doc, err := readServerDocument(path); err != nil || doc.Zones.Primary == "" || doc.Sheets.SpreadsheetID == "" {
		t.Fatalf("server loader document = %+v, %v", doc, err)
	}
}

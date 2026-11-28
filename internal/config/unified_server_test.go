package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestServerConfigurationIsAcceptedByFeatureLoaders(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "server.yaml")
	body := fmt.Sprintf(`inventory:
  cache_file: %q
  fallback_prefix: "192.0.2"
  sheets:
    spreadsheet_id: "spreadsheet_test"
    ranges: ["'Inventory'!A2:G256"]
    credentials_file: %q
dns:
  zone: "example.internal"
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
certificates:
  store_dir: "/var/lib/certpouch/certificates"
  trust_ca_file: "/etc/certpouch/issuers.pem"
  issuer: {certificate_file: %q, private_key_file: %q}
  targets:
    - name: "web.example.internal"
      validity: "2160h"
      renew_before: "720h"
distribution:
  listen: "127.0.0.1:8443"
  clients: []
`, filepath.Join(root, "inventory.json"), filepath.Join(root, "service-account.json"),
		filepath.Join(root, "intermediate.crt"), filepath.Join(root, "intermediate.key"))
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDNSConfig(path); err != nil {
		t.Fatalf("DNS loader rejected server config: %v", err)
	}
	if _, err := loadSheetsFetchDocument(path); err != nil {
		t.Fatalf("Sheets loader rejected server config: %v", err)
	}
	doc, err := readServerDocument(path)
	if err != nil || doc.Zones.Primary == "" || doc.Sheets.SpreadsheetID == "" {
		t.Fatalf("server loader document = %+v, %v", doc, err)
	}
	if renewal, err := renewalForNames(doc.Renewal, nil); err != nil || len(renewal.Profiles) != 1 {
		t.Fatalf("renewal = %+v, %v", renewal, err)
	}
}

package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Surigoma/CertPouch/internal/dns"
	"github.com/Surigoma/CertPouch/internal/pki"
)

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

package config

import (
	"bytes"
	"fmt"
	"path/filepath"

	"github.com/Surigoma/CertPouch/internal/dns"
)

type dnsDocument struct {
	Inventory struct {
		CacheFile      string `yaml:"cache_file"`
		FallbackPrefix string `yaml:"fallback_prefix"`
		Zone           string `yaml:"zone"`
	} `yaml:"dns_inventory"`
	Zones struct {
		Primary         string            `yaml:"primary"`
		Mailbox         string            `yaml:"mailbox"`
		Nameservers     []string          `yaml:"nameservers"`
		TTL             uint32            `yaml:"ttl"`
		Serial          *uint32           `yaml:"serial"`
		PreviousSerials map[string]uint32 `yaml:"previous_serials"`
		Refresh         uint32            `yaml:"refresh"`
		Retry           uint32            `yaml:"retry"`
		Expire          uint32            `yaml:"expire"`
		NegativeTTL     uint32            `yaml:"negative_ttl"`
	} `yaml:"zones"`
}

func LoadDNSZones(path string) ([]dns.Zone, error) {
	raw, err := readFile(path, false, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read DNS config: %w", err)
	}
	var doc dnsDocument
	if err := decodeYAML(raw, &doc); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(doc.Inventory.CacheFile) || (doc.Zones.Serial == nil && len(doc.Zones.PreviousSerials) == 0) {
		return nil, fmt.Errorf("DNS configuration requires an absolute cache_file and zones.serial or zones.previous_serials")
	}
	raw, err = readFile(doc.Inventory.CacheFile, false, 16<<20)
	if err != nil {
		return nil, fmt.Errorf("read DNS inventory: %w", err)
	}
	inventory, err := dns.ReadLegacyCache(bytes.NewReader(raw), doc.Inventory.FallbackPrefix, doc.Inventory.Zone)
	if err != nil {
		return nil, err
	}
	options := doc.Zones
	var serial uint32
	if options.Serial != nil {
		serial = *options.Serial
	}
	return dns.RenderZones(inventory, dns.ZoneOptions{
		Forward: doc.Inventory.Zone, Primary: options.Primary, Mailbox: options.Mailbox,
		Nameservers: options.Nameservers, TTL: options.TTL, Serial: serial,
		PreviousSerials: options.PreviousSerials, AdvanceSerial: options.Serial == nil,
		Refresh: options.Refresh, Retry: options.Retry, Expire: options.Expire, NegativeTTL: options.NegativeTTL,
	})
}

package config

import (
	"fmt"

	"github.com/Surigoma/CertPouch/internal/dns"
)

type dnsDocument struct {
	Inventory inventoryDocument `yaml:"dns_inventory"`
	Zones     struct {
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
		StaticRecords   []struct {
			Zone     string  `yaml:"zone"`
			Owner    string  `yaml:"owner"`
			Type     string  `yaml:"type"`
			Value    string  `yaml:"value"`
			Priority *uint16 `yaml:"priority"`
		} `yaml:"static_records"`
	} `yaml:"zones"`
}

func LoadDNSZones(path, serialDirectory, baselineDirectory string) ([]dns.Zone, error) {
	raw, err := readFile(path, false, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read DNS config: %w", err)
	}
	var doc dnsDocument
	if err := decodeYAML(raw, &doc); err != nil {
		return nil, err
	}
	if serialDirectory != "" || baselineDirectory != "" {
		if doc.Zones.Serial != nil || len(doc.Zones.PreviousSerials) != 0 {
			return nil, fmt.Errorf("a serial source cannot be combined with zones.serial or zones.previous_serials")
		}
		if baselineDirectory != "" {
			doc.Zones.PreviousSerials, err = dns.ReadCandidateSerials(baselineDirectory)
		} else {
			doc.Zones.PreviousSerials, err = dns.ReadManagedSerials(serialDirectory)
		}
		if err != nil {
			return nil, fmt.Errorf("read current zone serials: %w", err)
		}
	}
	if doc.Zones.Serial == nil && len(doc.Zones.PreviousSerials) == 0 {
		return nil, fmt.Errorf("DNS configuration requires zones.serial or zones.previous_serials")
	}
	inventory, err := doc.Inventory.load()
	if err != nil {
		return nil, err
	}
	options := doc.Zones
	var serial uint32
	if options.Serial != nil {
		serial = *options.Serial
	}
	records := make([]dns.StaticRecord, 0, len(options.StaticRecords))
	for _, record := range options.StaticRecords {
		records = append(records, dns.StaticRecord{
			Zone: record.Zone, Owner: record.Owner, Type: record.Type,
			Value: record.Value, Priority: record.Priority,
		})
	}
	return dns.RenderZones(inventory, dns.ZoneOptions{
		Forward: doc.Inventory.Zone, Primary: options.Primary, Mailbox: options.Mailbox,
		Nameservers: options.Nameservers, TTL: options.TTL, Serial: serial,
		PreviousSerials: options.PreviousSerials, AdvanceSerial: options.Serial == nil,
		StaticRecords: records,
		Refresh:       options.Refresh, Retry: options.Retry, Expire: options.Expire, NegativeTTL: options.NegativeTTL,
	})
}

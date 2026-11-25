package config

import (
	"fmt"
	"path/filepath"

	"github.com/Surigoma/CertPouch/internal/dns"
)

type DNSDeployment struct {
	NSDInclude   string `yaml:"include_file"`
	PollInterval string `yaml:"poll_interval"`
	StageDir     string `yaml:"stage_dir"`
	ZoneDir      string `yaml:"zone_dir"`
	NSDConfig    string `yaml:"nsd_config"`
	DNSAddress   string `yaml:"dns_address"`
}

func LoadDNSDeployment(path string) (DNSDeployment, error) {
	doc, err := readServerDocument(path)
	if err != nil {
		return DNSDeployment{}, err
	}
	if doc.Deployment == nil {
		return DNSDeployment{}, fmt.Errorf("DNS deployment configuration is required")
	}
	return *doc.Deployment, nil
}

type dnsZoneDocument struct {
	ReverseZones    []string          `yaml:"reverse_zones"`
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
}

func readDNSDocument(path string) (inventoryDocument, dnsZoneDocument, error) {
	raw, err := readFile(path, false, 1<<20)
	if err != nil {
		return inventoryDocument{}, dnsZoneDocument{}, fmt.Errorf("read server config: %w", err)
	}
	doc, err := decodeServer(raw)
	if err != nil {
		return inventoryDocument{}, dnsZoneDocument{}, err
	}
	if doc.DNSInventory == nil {
		return inventoryDocument{}, dnsZoneDocument{}, fmt.Errorf("server configuration requires dns_inventory")
	}
	return *doc.DNSInventory, doc.Zones, nil
}

func ValidateDNSConfig(path string) error {
	inventory, zones, err := readDNSDocument(path)
	if err != nil {
		return err
	}
	if zones.Serial == nil && len(zones.PreviousSerials) == 0 {
		return fmt.Errorf("DNS configuration requires zones.serial or zones.previous_serials")
	}
	if (inventory.CacheFile == "") == (len(inventory.CSVFiles) == 0) {
		return fmt.Errorf("configure exactly one of dns_inventory.cache_file or dns_inventory.csv_files")
	}
	if inventory.CacheFile != "" && !filepath.IsAbs(inventory.CacheFile) {
		return fmt.Errorf("dns_inventory.cache_file must be absolute")
	}
	if _, err := inventory.freshnessMaxAge(); err != nil {
		return err
	}
	var serial uint32
	if zones.Serial != nil {
		serial = *zones.Serial
	}
	return dns.ValidateZoneOptions(dns.ZoneOptions{
		Forward: inventory.Zone, Primary: zones.Primary, Mailbox: zones.Mailbox,
		Nameservers: zones.Nameservers, TTL: zones.TTL, Serial: serial,
		Refresh: zones.Refresh, Retry: zones.Retry, Expire: zones.Expire,
		NegativeTTL: zones.NegativeTTL,
	})
}

func LoadDNSZones(path, serialDirectory, baselineDirectory string) ([]dns.Zone, error) {
	return loadDNSZones(path, serialDirectory, baselineDirectory, nil, false)
}

// RenderFetchedDNSZones renders a candidate without exposing an unconfirmed
// Sheets snapshot to the certificate distribution service.
func RenderFetchedDNSZones(path, serialDirectory string, inventory dns.Inventory, startup bool) ([]dns.Zone, error) {
	return loadDNSZones(path, serialDirectory, "", &inventory, startup)
}

func loadDNSZones(path, serialDirectory, baselineDirectory string, fetched *dns.Inventory, startup bool) ([]dns.Zone, error) {
	inventoryDocument, zones, err := readDNSDocument(path)
	if err != nil {
		return nil, err
	}
	if startup && zones.ReverseZones == nil {
		return nil, fmt.Errorf("startup zone preparation requires an explicit reverse_zones list")
	}
	if serialDirectory != "" || baselineDirectory != "" {
		if zones.Serial != nil || len(zones.PreviousSerials) != 0 {
			return nil, fmt.Errorf("a serial source cannot be combined with zones.serial or zones.previous_serials")
		}
		if baselineDirectory != "" {
			zones.PreviousSerials, err = dns.ReadCandidateSerials(baselineDirectory)
		} else {
			zones.PreviousSerials, err = dns.ReadManagedSerials(serialDirectory)
		}
		if err != nil {
			return nil, fmt.Errorf("read current zone serials: %w", err)
		}
	}
	if !startup && zones.Serial == nil && len(zones.PreviousSerials) == 0 {
		return nil, fmt.Errorf("DNS configuration requires zones.serial or zones.previous_serials")
	}
	var inventory dns.Inventory
	if fetched != nil {
		inventory = *fetched
	} else {
		inventory, err = inventoryDocument.load()
		if err != nil {
			return nil, err
		}
	}
	options := zones
	var serial uint32
	if startup {
		serial = 1
	}
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
		Forward: inventoryDocument.Zone, Primary: options.Primary, Mailbox: options.Mailbox,
		Nameservers: options.Nameservers, TTL: options.TTL, Serial: serial,
		PreviousSerials: options.PreviousSerials, AdvanceSerial: options.Serial == nil,
		StaticRecords:       records,
		ReverseZones:        options.ReverseZones,
		AllowZoneSetChange:  startup,
		AllowEmptyInventory: fetched != nil && options.ReverseZones != nil,
		Refresh:             options.Refresh, Retry: options.Retry, Expire: options.Expire, NegativeTTL: options.NegativeTTL,
	})
}

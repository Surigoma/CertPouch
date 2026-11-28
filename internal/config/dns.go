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
		return DNSDeployment{}, fmt.Errorf("dns.nsd configuration is required")
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
		return inventoryDocument{}, dnsZoneDocument{}, fmt.Errorf("DNS generation requires inventory.cache_file or inventory.csv_files")
	}
	return *doc.DNSInventory, doc.Zones, nil
}

func ValidateDNSConfig(path string) error {
	inventory, zones, err := readDNSDocument(path)
	if err != nil {
		return err
	}
	if (inventory.CacheFile == "") == (len(inventory.CSVFiles) == 0) {
		return fmt.Errorf("configure exactly one of inventory.cache_file or inventory.csv_files")
	}
	if inventory.CacheFile != "" && !filepath.IsAbs(inventory.CacheFile) {
		return fmt.Errorf("inventory.cache_file must be an absolute path")
	}
	if _, err := inventory.freshnessMaxAge(); err != nil {
		return err
	}
	serial := uint32(1)
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

// SerialSource selects where candidate SOA serials come from. At most one
// of the fields other than Preview may be set; dns.zones.serial and
// dns.zones.previous_serials in server.yaml count as a source too.
type SerialSource struct {
	ManagedDir  string  // advance the serials of the managed NSD generation
	BaselineDir string  // advance the serials of a staged candidate
	Fixed       *uint32 // use this serial for every zone
	// Preview allows rendering with serial 1 when no source is configured.
	Preview bool
}

// LoadDNSZones renders candidate zones. previewSerial reports that no serial
// source was configured and the preview used serial 1.
func LoadDNSZones(path string, source SerialSource) (zones []dns.Zone, previewSerial bool, err error) {
	return loadDNSZones(path, source, nil, false)
}

// RenderFetchedDNSZones renders a candidate without exposing an unconfirmed
// Sheets snapshot to the certificate distribution service.
func RenderFetchedDNSZones(path, serialDirectory string, inventory dns.Inventory, startup bool) ([]dns.Zone, error) {
	zones, _, err := loadDNSZones(path, SerialSource{ManagedDir: serialDirectory}, &inventory, startup)
	return zones, err
}

func loadDNSZones(path string, source SerialSource, fetched *dns.Inventory, startup bool) ([]dns.Zone, bool, error) {
	inventoryDocument, zones, err := readDNSDocument(path)
	if err != nil {
		return nil, false, err
	}
	if startup && zones.ReverseZones == nil {
		return nil, false, fmt.Errorf("startup zone preparation requires an explicit dns.zones.reverse_zones list")
	}
	external := 0
	for _, set := range []bool{source.ManagedDir != "", source.BaselineDir != "", source.Fixed != nil} {
		if set {
			external++
		}
	}
	if external > 1 {
		return nil, false, fmt.Errorf("use only one of --serial, --serial-from and --serial-from-baseline")
	}
	if external == 1 && (zones.Serial != nil || len(zones.PreviousSerials) != 0) {
		return nil, false, fmt.Errorf("dns.zones.serial and dns.zones.previous_serials cannot be combined with another serial source; remove them from server.yaml")
	}
	switch {
	case source.Fixed != nil:
		zones.Serial = source.Fixed
	case source.BaselineDir != "":
		zones.PreviousSerials, err = dns.ReadCandidateSerials(source.BaselineDir)
	case source.ManagedDir != "":
		zones.PreviousSerials, err = dns.ReadManagedSerials(source.ManagedDir)
	}
	if err != nil {
		return nil, false, fmt.Errorf("read current zone serials: %w", err)
	}
	previewSerial := false
	if !startup && zones.Serial == nil && len(zones.PreviousSerials) == 0 {
		if !source.Preview {
			return nil, false, fmt.Errorf("staging a DNS candidate needs a SOA serial: pass --serial N (newer than the serial NSD serves now) for the first candidate, and --serial-from <dns.nsd.zone_dir> once NSD serves a managed generation")
		}
		first := uint32(1)
		zones.Serial, previewSerial = &first, true
	}
	var inventory dns.Inventory
	if fetched != nil {
		inventory = *fetched
	} else {
		inventory, err = inventoryDocument.load()
		if err != nil {
			return nil, false, err
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
	rendered, err := dns.RenderZones(inventory, dns.ZoneOptions{
		Forward: inventoryDocument.Zone, Primary: options.Primary, Mailbox: options.Mailbox,
		Nameservers: options.Nameservers, TTL: options.TTL, Serial: serial,
		PreviousSerials: options.PreviousSerials, AdvanceSerial: options.Serial == nil,
		StaticRecords:       records,
		ReverseZones:        options.ReverseZones,
		AllowZoneSetChange:  startup,
		AllowEmptyInventory: fetched != nil && options.ReverseZones != nil,
		Refresh:             options.Refresh, Retry: options.Retry, Expire: options.Expire, NegativeTTL: options.NegativeTTL,
	})
	return rendered, previewSerial, err
}

package config

import (
	"fmt"
	"slices"
)

// workflowDocument is the only accepted server.yaml layout. It is grouped by
// task: inventory input, DNS output, certificate issuance, and distribution.
type workflowDocument struct {
	Inventory struct {
		CacheFile      string   `yaml:"cache_file"`
		CSVFiles       []string `yaml:"csv_files"`
		FallbackPrefix string   `yaml:"fallback_prefix"`
		MaxAge         string   `yaml:"max_age"`
		PollInterval   string   `yaml:"poll_interval"`
		Sheets         struct {
			SpreadsheetID   string   `yaml:"spreadsheet_id"`
			Ranges          []string `yaml:"ranges"`
			CredentialsFile string   `yaml:"credentials_file"`
		} `yaml:"sheets"`
	} `yaml:"inventory"`
	DNS struct {
		Zone  string          `yaml:"zone"`
		Zones dnsZoneDocument `yaml:"zones"`
		NSD   struct {
			ConfigFile  string `yaml:"config_file"`
			IncludeFile string `yaml:"include_file"`
			StageDir    string `yaml:"stage_dir"`
			ZoneDir     string `yaml:"zone_dir"`
			DNSAddress  string `yaml:"dns_address"`
		} `yaml:"nsd"`
	} `yaml:"dns"`
	Certificates struct {
		Issuer struct {
			CertificateFile string `yaml:"certificate_file"`
			PrivateKeyFile  string `yaml:"private_key_file"`
			ChainFile       string `yaml:"chain_file"`
			PassphraseFile  string `yaml:"passphrase_file"`
		} `yaml:"issuer"`
		Validity    string           `yaml:"validity"`
		RenewBefore string           `yaml:"renew_before"`
		TrustCAFile string           `yaml:"trust_ca_file"`
		StoreDir    string           `yaml:"store_dir"`
		Targets     []targetDocument `yaml:"targets"`
	} `yaml:"certificates"`
	Distribution struct {
		Auth              string `yaml:"auth"`
		Listen            string `yaml:"listen"`
		RefreshInterval   string `yaml:"refresh_interval"`
		RevocationFile    string `yaml:"revocation_file"`
		RevocationRefresh string `yaml:"revocation_refresh"`
		TLS               struct {
			CertificateFile string `yaml:"certificate_file"`
			PrivateKeyFile  string `yaml:"private_key_file"`
			ClientCA        string `yaml:"client_ca"`
		} `yaml:"tls"`
		Clients []clientDocument `yaml:"clients"`
	} `yaml:"distribution"`
}

// targetDocument names one explicitly managed certificate. Without targets,
// every inventory name is a target.
type targetDocument struct {
	Name        string   `yaml:"name"`
	DNS         []string `yaml:"expected_dns_sans"`
	IPs         []string `yaml:"expected_ip_sans"`
	IssuerFile  string   `yaml:"issuer_certificate"`
	Validity    string   `yaml:"validity"`
	RenewBefore string   `yaml:"renew_before"`
}

// legacyServerKeys maps CertPouch v1 top-level keys to their v2 location.
var legacyServerKeys = map[string]string{
	"server":        "distribution (listen, tls, refresh_interval, revocation_file)",
	"storage":       "certificates.store_dir, certificates.trust_ca_file and certificates.issuer.certificate_file",
	"dns_inventory": "inventory and dns.zone",
	"google_sheets": "inventory.sheets",
	"zones":         "dns.zones",
	"renewal":       "certificates.issuer, certificates.validity, certificates.renew_before and certificates.targets",
	"pipeline":      "inventory, dns.zones, dns.nsd and certificates",
	"clients":       "distribution.clients",
}

func decodeServer(body []byte) (serverDocument, error) {
	var doc serverDocument
	var keys map[string]any
	if err := decodeYAML(body, &keys); err != nil {
		return doc, err
	}
	if err := rejectLegacyServerKeys(keys); err != nil {
		return doc, err
	}
	var input workflowDocument
	if err := decodeYAML(body, &input); err != nil {
		return doc, err
	}
	inventory := input.Inventory
	if inventory.CacheFile != "" || len(inventory.CSVFiles) != 0 {
		doc.DNSInventory = &inventoryDocument{
			CacheFile: inventory.CacheFile, CSVFiles: inventory.CSVFiles,
			FallbackPrefix: inventory.FallbackPrefix, Zone: input.DNS.Zone, MaxAge: inventory.MaxAge,
		}
	}
	doc.Sheets = googleSheetsDocument{
		SpreadsheetID: inventory.Sheets.SpreadsheetID, Ranges: inventory.Sheets.Ranges,
		ServiceAccountFile: inventory.Sheets.CredentialsFile,
	}
	doc.Zones = input.DNS.Zones
	nsd := input.DNS.NSD
	doc.Deployment = &DNSDeployment{
		PollInterval: inventory.PollInterval, NSDConfig: nsd.ConfigFile,
		NSDInclude: nsd.IncludeFile, StageDir: nsd.StageDir,
		ZoneDir: nsd.ZoneDir, DNSAddress: nsd.DNSAddress,
	}
	cert := input.Certificates
	doc.Renewal = Renewal{
		FromInventory:   len(cert.Targets) == 0 && cert.Issuer.CertificateFile != "",
		CertificateFile: cert.Issuer.CertificateFile, KeyFile: cert.Issuer.PrivateKeyFile,
		ChainFile: cert.Issuer.ChainFile, PassphraseFile: cert.Issuer.PassphraseFile,
		Validity: cert.Validity, RenewBefore: cert.RenewBefore,
	}
	for i, target := range cert.Targets {
		doc.Certificates = append(doc.Certificates, certificateDocument{
			Name: target.Name, DNS: target.DNS, IPs: target.IPs, IssuerFile: target.IssuerFile,
		})
		if cert.Issuer.PrivateKeyFile == "" {
			if target.Validity != "" || target.RenewBefore != "" {
				return doc, fmt.Errorf("certificates.targets[%d].validity and renew_before require certificates.issuer.private_key_file", i)
			}
			continue
		}
		profile := RenewalProfile{Name: target.Name, ValidityText: target.Validity, RenewBeforeText: target.RenewBefore}
		if profile.ValidityText == "" {
			profile.ValidityText = cert.Validity
		}
		if profile.RenewBeforeText == "" {
			profile.RenewBeforeText = cert.RenewBefore
		}
		doc.Renewal.Profiles = append(doc.Renewal.Profiles, profile)
	}
	doc.Storage.CertificateDir = cert.StoreDir
	doc.Storage.TrustCAFile = cert.TrustCAFile
	doc.Storage.IssuerFile = cert.Issuer.CertificateFile
	distribution := input.Distribution
	doc.Server.Auth = distribution.Auth
	doc.Server.Listen = distribution.Listen
	doc.Server.TLS.Certificate = distribution.TLS.CertificateFile
	doc.Server.TLS.PrivateKey = distribution.TLS.PrivateKeyFile
	doc.Server.TLS.ClientCA = distribution.TLS.ClientCA
	doc.Server.Refresh = distribution.RefreshInterval
	if doc.Server.Refresh == "" {
		doc.Server.Refresh = "5s"
	}
	doc.Server.RevocationFile = distribution.RevocationFile
	doc.Server.RevocationRefresh = distribution.RevocationRefresh
	doc.Clients = distribution.Clients
	return doc, nil
}

func rejectLegacyServerKeys(keys map[string]any) error {
	var found []string
	for key := range keys {
		if _, legacy := legacyServerKeys[key]; legacy {
			found = append(found, key)
		}
	}
	if _, isList := keys["certificates"].([]any); isList {
		return fmt.Errorf("top-level certificates is a CertPouch v1 list; move each entry to certificates.targets (see docs/SPEC/02-configuration.md)")
	}
	if len(found) == 0 {
		return nil
	}
	slices.Sort(found)
	key := found[0]
	return fmt.Errorf("top-level %s is a CertPouch v1 key; move it to %s (see docs/SPEC/02-configuration.md)", key, legacyServerKeys[key])
}

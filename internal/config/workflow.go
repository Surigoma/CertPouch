package config

// workflowDocument is the operator-facing configuration grouped by task.
type workflowDocument struct {
	Inventory struct {
		CacheFile      string `yaml:"cache_file"`
		FallbackPrefix string `yaml:"fallback_prefix"`
		MaxAge         string `yaml:"max_age"`
		PollInterval   string `yaml:"poll_interval"`
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
		Validity    string `yaml:"validity"`
		RenewBefore string `yaml:"renew_before"`
		TrustCAFile string `yaml:"trust_ca_file"`
		StoreDir    string `yaml:"store_dir"`
	} `yaml:"certificates"`
	Distribution struct {
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

func decodeWorkflowServer(body []byte) (serverDocument, error) {
	var input workflowDocument
	var doc serverDocument
	if err := decodeYAML(body, &input); err != nil {
		return doc, err
	}
	doc.DNSInventory = &inventoryDocument{
		CacheFile: input.Inventory.CacheFile, FallbackPrefix: input.Inventory.FallbackPrefix,
		Zone: input.DNS.Zone, MaxAge: input.Inventory.MaxAge,
	}
	doc.Sheets = googleSheetsDocument{
		SpreadsheetID: input.Inventory.Sheets.SpreadsheetID, Ranges: input.Inventory.Sheets.Ranges,
		ServiceAccountFile: input.Inventory.Sheets.CredentialsFile,
	}
	doc.Zones = input.DNS.Zones
	doc.Deployment = &DNSDeployment{
		PollInterval: input.Inventory.PollInterval, NSDConfig: input.DNS.NSD.ConfigFile,
		NSDInclude: input.DNS.NSD.IncludeFile, StageDir: input.DNS.NSD.StageDir,
		ZoneDir: input.DNS.NSD.ZoneDir, DNSAddress: input.DNS.NSD.DNSAddress,
	}
	cert := input.Certificates
	doc.Renewal = Renewal{
		FromInventory: cert.Issuer.CertificateFile != "", CertificateFile: cert.Issuer.CertificateFile,
		KeyFile: cert.Issuer.PrivateKeyFile, ChainFile: cert.Issuer.ChainFile,
		PassphraseFile: cert.Issuer.PassphraseFile, Validity: cert.Validity, RenewBefore: cert.RenewBefore,
	}
	doc.Storage.CertificateDir = cert.StoreDir
	doc.Storage.TrustCAFile = cert.TrustCAFile
	doc.Storage.IssuerFile = cert.Issuer.CertificateFile
	distribution := input.Distribution
	doc.Server.Listen = distribution.Listen
	doc.Server.TLS.Certificate = distribution.TLS.CertificateFile
	doc.Server.TLS.PrivateKey = distribution.TLS.PrivateKeyFile
	doc.Server.TLS.ClientCA = distribution.TLS.ClientCA
	doc.Server.PublicationRefresh = distribution.RefreshInterval
	if doc.Server.PublicationRefresh == "" {
		doc.Server.PublicationRefresh = "5s"
	}
	doc.Server.InventoryRefresh = doc.Server.PublicationRefresh
	doc.Server.RevocationFile = distribution.RevocationFile
	doc.Server.RevocationRefresh = distribution.RevocationRefresh
	doc.Clients = distribution.Clients
	return doc, nil
}

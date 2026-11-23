package config

import (
	"fmt"
	"path/filepath"

	"github.com/Surigoma/CertPouch/internal/sheets"
)

type SheetsFetch struct {
	CacheFile      string
	FallbackPrefix string
	Zone           string
	SpreadsheetID  string
	Ranges         []string
	Credentials    sheets.Credentials
}

func LoadSheetsFetch(path string) (SheetsFetch, error) {
	var doc struct {
		Inventory inventoryDocument `yaml:"dns_inventory"`
		Sheets    struct {
			SpreadsheetID      string   `yaml:"spreadsheet_id"`
			Ranges             []string `yaml:"ranges"`
			ServiceAccountFile string   `yaml:"service_account_file"`
		} `yaml:"google_sheets"`
	}
	raw, err := readFile(path, false, 1<<20)
	if err != nil {
		return SheetsFetch{}, fmt.Errorf("read Sheets fetch configuration: %w", err)
	}
	if err := decodeYAML(raw, &doc); err != nil {
		return SheetsFetch{}, err
	}
	if !filepath.IsAbs(doc.Inventory.CacheFile) || len(doc.Inventory.CSVFiles) != 0 ||
		!filepath.IsAbs(doc.Sheets.ServiceAccountFile) {
		return SheetsFetch{}, fmt.Errorf("Sheets fetch requires absolute cache and service account paths")
	}
	secret, err := readFile(doc.Sheets.ServiceAccountFile, true, 64<<10)
	if err != nil {
		return SheetsFetch{}, fmt.Errorf("read Sheets service account file: %w", err)
	}
	defer clear(secret)
	credentials, err := sheets.ParseCredentials(secret)
	if err != nil {
		return SheetsFetch{}, err
	}
	return SheetsFetch{
		CacheFile: doc.Inventory.CacheFile, FallbackPrefix: doc.Inventory.FallbackPrefix, Zone: doc.Inventory.Zone,
		SpreadsheetID: doc.Sheets.SpreadsheetID, Ranges: doc.Sheets.Ranges, Credentials: credentials,
	}, nil
}

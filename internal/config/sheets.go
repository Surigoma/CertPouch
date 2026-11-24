package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Surigoma/CertPouch/internal/dns"
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

type sheetsFetchDocument struct {
	Inventory inventoryDocument `yaml:"dns_inventory"`
	Sheets    struct {
		SpreadsheetID      string   `yaml:"spreadsheet_id"`
		Ranges             []string `yaml:"ranges"`
		ServiceAccountFile string   `yaml:"service_account_file"`
	} `yaml:"google_sheets"`
}

func loadSheetsFetchDocument(path string) (sheetsFetchDocument, error) {
	raw, err := readFile(path, false, 1<<20)
	if err != nil {
		return sheetsFetchDocument{}, fmt.Errorf("read Sheets fetch configuration: %w", err)
	}
	var doc sheetsFetchDocument
	if err := decodeYAML(raw, &doc); err != nil {
		return sheetsFetchDocument{}, err
	}
	if !filepath.IsAbs(doc.Inventory.CacheFile) || len(doc.Inventory.CSVFiles) != 0 {
		return sheetsFetchDocument{}, fmt.Errorf("Sheets fetch requires an absolute cache_file and does not accept csv_files")
	}
	return doc, nil
}

func LoadSheetsFetch(path string) (SheetsFetch, error) {
	doc, err := loadSheetsFetchDocument(path)
	if err != nil {
		return SheetsFetch{}, err
	}
	if !filepath.IsAbs(doc.Sheets.ServiceAccountFile) {
		return SheetsFetch{}, fmt.Errorf("Sheets fetch requires an absolute service account path")
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
	if err := sheets.ValidateRequest(doc.Sheets.SpreadsheetID, doc.Sheets.Ranges); err != nil {
		return SheetsFetch{}, err
	}
	return SheetsFetch{
		CacheFile: doc.Inventory.CacheFile, FallbackPrefix: doc.Inventory.FallbackPrefix, Zone: doc.Inventory.Zone,
		SpreadsheetID: doc.Sheets.SpreadsheetID, Ranges: doc.Sheets.Ranges, Credentials: credentials,
	}, nil
}

type SheetsCacheStatus struct {
	State     string     `json:"state"`
	FetchedAt *time.Time `json:"last_successful_fetch,omitempty"`
	MaxAge    string     `json:"max_age,omitempty"`
}

// ReadSheetsCacheStatus does not load the service account.
func ReadSheetsCacheStatus(path string, now time.Time) (SheetsCacheStatus, error) {
	doc, err := loadSheetsFetchDocument(path)
	if err != nil {
		return SheetsCacheStatus{}, err
	}
	age, err := doc.Inventory.freshnessMaxAge()
	if err != nil {
		return SheetsCacheStatus{}, err
	}
	status := SheetsCacheStatus{State: "invalid", MaxAge: doc.Inventory.MaxAge}
	raw, err := readFile(doc.Inventory.CacheFile, false, sheets.MaxSnapshotBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			status.State = "missing"
		}
		return status, nil
	}
	freshness, err := verifyInventoryCache(doc.Inventory.CacheFile, raw, now, age)
	switch {
	case errors.Is(err, dns.ErrStaleCache):
		status.State, status.FetchedAt = "stale", &freshness.FetchedAt
	case errors.Is(err, os.ErrNotExist):
		status.State = "untracked"
	case err == nil:
		status.State, status.FetchedAt = "verified", &freshness.FetchedAt
	}
	return status, nil
}

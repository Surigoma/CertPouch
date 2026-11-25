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

type googleSheetsDocument struct {
	SpreadsheetID      string   `yaml:"spreadsheet_id"`
	Ranges             []string `yaml:"ranges"`
	ServiceAccountFile string   `yaml:"service_account_file"`
}

func loadSheetsFetchDocument(path string) (serverDocument, error) {
	raw, err := readFile(path, false, 1<<20)
	if err != nil {
		return serverDocument{}, fmt.Errorf("read server configuration: %w", err)
	}
	doc, err := decodeServer(raw)
	if err != nil {
		return serverDocument{}, err
	}
	if doc.DNSInventory == nil || !filepath.IsAbs(doc.DNSInventory.CacheFile) || len(doc.DNSInventory.CSVFiles) != 0 {
		return serverDocument{}, fmt.Errorf("Sheets fetch requires an absolute cache_file and does not accept csv_files")
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
		CacheFile: doc.DNSInventory.CacheFile, FallbackPrefix: doc.DNSInventory.FallbackPrefix, Zone: doc.DNSInventory.Zone,
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
	age, err := doc.DNSInventory.freshnessMaxAge()
	if err != nil {
		return SheetsCacheStatus{}, err
	}
	status := SheetsCacheStatus{State: "invalid", MaxAge: doc.DNSInventory.MaxAge}
	raw, err := readFile(doc.DNSInventory.CacheFile, false, sheets.MaxSnapshotBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			status.State = "missing"
		}
		return status, nil
	}
	freshness, err := verifyInventoryCache(doc.DNSInventory.CacheFile, raw, now, age)
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

package config

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/Surigoma/CertPouch/internal/dns"
)

type inventoryDocument struct {
	CacheFile      string   `yaml:"cache_file"`
	CSVFiles       []string `yaml:"csv_files"`
	FallbackPrefix string   `yaml:"fallback_prefix"`
	Zone           string   `yaml:"zone"`
	MaxAge         string   `yaml:"max_age"`
}

func (doc inventoryDocument) load() (dns.Inventory, error) {
	return doc.loadWithExpiry(nil)
}

func (doc inventoryDocument) loadWithExpiry(expires *time.Time) (dns.Inventory, error) {
	const maxInventoryBytes = 16 << 20
	if (doc.CacheFile == "") == (len(doc.CSVFiles) == 0) {
		return dns.Inventory{}, fmt.Errorf("configure exactly one of dns_inventory.cache_file or dns_inventory.csv_files")
	}
	maxAge, err := doc.freshnessMaxAge()
	if err != nil {
		return dns.Inventory{}, err
	}
	if doc.CacheFile != "" {
		if !filepath.IsAbs(doc.CacheFile) {
			return dns.Inventory{}, fmt.Errorf("dns_inventory.cache_file must be absolute")
		}
		raw, err := readFile(doc.CacheFile, false, maxInventoryBytes)
		if err != nil {
			return dns.Inventory{}, fmt.Errorf("read DNS cache: %w", err)
		}
		if maxAge != 0 {
			state, err := verifyInventoryCache(doc.CacheFile, raw, time.Now(), maxAge)
			if err != nil {
				return dns.Inventory{}, err
			}
			if expires != nil {
				*expires = state.FetchedAt.Add(maxAge)
			}
		}
		return dns.ReadLegacyCache(bytes.NewReader(raw), doc.FallbackPrefix, doc.Zone)
	}
	readers := make([]io.Reader, 0, len(doc.CSVFiles))
	remaining := int64(maxInventoryBytes)
	seen := make(map[string]bool)
	for i, path := range doc.CSVFiles {
		if !filepath.IsAbs(path) || seen[filepath.Clean(path)] {
			return dns.Inventory{}, fmt.Errorf("dns_inventory.csv_files[%d] must be a unique absolute path", i)
		}
		seen[filepath.Clean(path)] = true
		raw, err := readFile(path, false, remaining)
		if err != nil {
			return dns.Inventory{}, fmt.Errorf("read DNS CSV source %d: %w", i+1, err)
		}
		remaining -= int64(len(raw))
		readers = append(readers, bytes.NewReader(raw))
	}
	return dns.ReadLegacyCSV(readers, doc.FallbackPrefix, doc.Zone)
}

func (doc inventoryDocument) freshnessMaxAge() (time.Duration, error) {
	if doc.MaxAge == "" {
		return 0, nil
	}
	maxAge, err := time.ParseDuration(doc.MaxAge)
	if err != nil || maxAge < time.Second || doc.CacheFile == "" {
		return 0, fmt.Errorf("dns_inventory.max_age requires a cache and a duration of at least one second")
	}
	return maxAge, nil
}

func verifyInventoryCache(path string, raw []byte, now time.Time, maxAge time.Duration) (dns.CacheFreshness, error) {
	record, err := readFile(path+dns.CacheFreshnessSuffix, false, 4096)
	if err != nil {
		return dns.CacheFreshness{}, fmt.Errorf("read DNS cache fetch record: %w", err)
	}
	return dns.VerifyCacheFreshness(raw, record, now, maxAge)
}

package config

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Surigoma/CertPouch/internal/dns"
)

type inventoryDocument struct {
	CacheFile      string   `yaml:"cache_file"`
	CSVFiles       []string `yaml:"csv_files"`
	FallbackPrefix string   `yaml:"fallback_prefix"`
	Zone           string   `yaml:"zone"`
}

func (doc inventoryDocument) load() (dns.Inventory, error) {
	const maxInventoryBytes = 16 << 20
	if (doc.CacheFile == "") == (len(doc.CSVFiles) == 0) {
		return dns.Inventory{}, fmt.Errorf("configure exactly one of dns_inventory.cache_file or dns_inventory.csv_files")
	}
	if doc.CacheFile != "" {
		if !filepath.IsAbs(doc.CacheFile) {
			return dns.Inventory{}, fmt.Errorf("dns_inventory.cache_file must be absolute")
		}
		raw, err := readFile(doc.CacheFile, false, maxInventoryBytes)
		if err != nil {
			return dns.Inventory{}, fmt.Errorf("read DNS cache: %w", err)
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

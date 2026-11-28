package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/dns"
	"github.com/Surigoma/CertPouch/internal/sheets"
)

func runDNSFetch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dns fetch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to Google Sheets fetch YAML configuration")
	status := flags.Bool("status", false, "show local snapshot freshness without contacting Google")
	if code, ok := parseFlags(flags, args, stdout, stderr); !ok {
		return code
	}
	if *path == "" || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if *status {
		record, err := config.ReadSheetsCacheStatus(*path, time.Now)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if err := json.NewEncoder(stdout).Encode(record); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if record.State != "verified" {
			return 1
		}
		return 0
	}
	cfg, err := config.LoadSheetsFetch(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if cfg.ConfirmDNSBeforeCache {
		fmt.Fprintln(stderr, "dns fetch is disabled because certificates.issuer issues a certificate for every inventory name; use certpouch pipeline --once or --watch, which confirms NSD before updating the cache")
		return 2
	}
	hostCount := 0
	changed, err := dns.RefreshCache(ctx, cfg.CacheFile, sheets.MaxSnapshotBytes, func(ctx context.Context) ([]byte, error) {
		raw, inventory, err := fetchSheetsInventory(ctx, cfg)
		if err != nil {
			return nil, err
		}
		hostCount = inventory.HostCount()
		if hostCount == 0 {
			return nil, fmt.Errorf("refusing to replace the DNS cache with an empty inventory")
		}
		return raw, nil
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	state := "unchanged"
	if changed {
		state = "updated"
	}
	if _, err := fmt.Fprintf(stdout, "%s\t%d hosts\n", state, hostCount); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func fetchSheetsInventory(ctx context.Context, cfg config.SheetsFetch) ([]byte, dns.Inventory, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	body, err := sheets.NewClient().Fetch(ctx, cfg.Credentials, cfg.SpreadsheetID, cfg.Ranges)
	if err != nil {
		return nil, dns.Inventory{}, err
	}
	inventory, err := dns.ReadLegacyCache(bytes.NewReader(body), cfg.FallbackPrefix, cfg.Zone)
	return body, inventory, err
}

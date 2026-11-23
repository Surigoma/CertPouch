package main

import (
	"bytes"
	"context"
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
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *path == "" || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	cfg, err := config.LoadSheetsFetch(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	client := sheets.NewClient()
	hostCount := 0
	changed, err := dns.RefreshCache(ctx, cfg.CacheFile, sheets.MaxSnapshotBytes, func(ctx context.Context) ([]byte, error) {
		raw, err := client.Fetch(ctx, cfg.Credentials, cfg.SpreadsheetID, cfg.Ranges)
		if err != nil {
			return nil, err
		}
		inventory, err := dns.ReadLegacyCache(bytes.NewReader(raw), cfg.FallbackPrefix, cfg.Zone)
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

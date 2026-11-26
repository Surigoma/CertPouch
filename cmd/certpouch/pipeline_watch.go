package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/dns"
	"github.com/Surigoma/CertPouch/internal/sheets"
)

func watchPipeline(ctx context.Context, path string, deployment config.DNSDeployment, once bool, out, diagnostic io.Writer) int {
	interval := 3 * time.Minute
	var err error
	if deployment.PollInterval != "" {
		interval, err = time.ParseDuration(deployment.PollInterval)
	}
	if runtime.GOOS != "linux" || err != nil || interval < time.Second ||
		!filepath.IsAbs(deployment.StageDir) || !filepath.IsAbs(deployment.ZoneDir) || !filepath.IsAbs(deployment.NSDConfig) {
		fmt.Fprintln(diagnostic, "watch requires Linux, a valid poll interval, and absolute deployment paths")
		return 2
	}
	if _, err := dns.ReadManagedSerials(deployment.ZoneDir); err != nil && deployment.NSDInclude == "" {
		fmt.Fprintf(diagnostic, "read initial managed DNS zones: %v\n", err)
		return 2
	}
	address := deployment.DNSAddress
	if address == "" {
		address = "127.0.0.1:53"
	}
	endpoint, err := parseNSDEndpoint(address)
	if err != nil {
		fmt.Fprintln(diagnostic, err)
		return 2
	}
	control, err := newNSDCommands(deployment.NSDConfig, endpoint)
	if err != nil {
		fmt.Fprintln(diagnostic, err)
		return 1
	}
	failures := 0
	startup := deployment.NSDInclude != ""
	if once {
		if err := syncPipeline(ctx, path, deployment, control, &startup, out); err != nil {
			fmt.Fprintln(diagnostic, err)
			return 1
		}
		return 0
	}
	for ctx.Err() == nil {
		delay := interval
		if err := syncPipeline(ctx, path, deployment, control, &startup, out); err != nil {
			fmt.Fprintln(diagnostic, err)
			failures = min(failures+1, 4)
			for i := 0; i < failures && delay < 15*time.Minute; i++ {
				delay = min(2*delay, 15*time.Minute)
			}
		} else {
			failures = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return 0
}

func syncPipeline(ctx context.Context, path string, deployment config.DNSDeployment, control nsdCommands, startup *bool, out io.Writer) error {
	fetch, err := config.LoadSheetsFetch(path)
	if err != nil {
		return err
	}
	var inventory dns.Inventory
	var generation string
	_, err = dns.RefreshCacheAfter(ctx, fetch.CacheFile, sheets.MaxSnapshotBytes, func(ctx context.Context) ([]byte, error) {
		body, parsed, err := fetchSheetsInventory(ctx, fetch)
		inventory = parsed
		return body, err
	}, func(ctx context.Context) error {
		serialDir := deployment.ZoneDir
		if *startup {
			if _, err := os.Lstat(filepath.Join(serialDir, "current")); os.IsNotExist(err) {
				serialDir = ""
			}
		}
		zones, err := config.RenderFetchedDNSZones(path, serialDir, inventory, *startup)
		if err != nil {
			return err
		}
		candidate, err := stageDNSZones(ctx, deployment.StageDir, zones)
		if err != nil {
			return err
		}
		defer func() {
			for _, zone := range zones {
				_ = os.Remove(filepath.Join(candidate, zone.Name+".zone"))
			}
			_ = os.Remove(candidate)
		}()
		if *startup {
			if err := prepareNSD(ctx, deployment, candidate, control); err != nil {
				return err
			}
			*startup = false
		}
		generation, err = dns.Activate(ctx, deployment.ZoneDir, candidate, dns.ActivationOptions{}, control)
		if err != nil {
			return fmt.Errorf("NSD activation failed: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	_, reportErr := fmt.Fprintln(out, generation)
	server, renewal, err := config.LoadRenewalInputs(path)
	if err != nil {
		return errors.Join(reportErr, err)
	}
	if err := renewCertificates(ctx, server, renewal, false, out); err != nil {
		return errors.Join(reportErr, fmt.Errorf("certificate renewal failed after DNS activation: %w", err))
	}
	return reportErr
}

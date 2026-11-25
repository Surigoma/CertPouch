package main

import (
	"context"
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

func watchPipeline(ctx context.Context, path string, deployment config.DNSDeployment, out, diagnostic io.Writer) int {
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
	_, err = dns.RefreshCache(ctx, fetch.CacheFile, sheets.MaxSnapshotBytes, func(ctx context.Context) ([]byte, error) {
		body, inventory, err := fetchSheetsInventory(ctx, fetch)
		if err != nil {
			return nil, err
		}
		serialDir := deployment.ZoneDir
		if *startup {
			if _, err := os.Lstat(filepath.Join(serialDir, "current")); os.IsNotExist(err) {
				serialDir = ""
			}
		}
		zones, err := config.RenderFetchedDNSZones(path, serialDir, inventory, *startup)
		if err != nil {
			return nil, err
		}
		candidate, err := stageDNSZones(ctx, deployment.StageDir, zones)
		if err != nil {
			return nil, err
		}
		defer func() {
			for _, zone := range zones {
				_ = os.Remove(filepath.Join(candidate, zone.Name+".zone"))
			}
			_ = os.Remove(candidate)
		}()
		if *startup {
			if err := prepareNSD(ctx, deployment, candidate, control); err != nil {
				return nil, err
			}
			*startup = false
		}
		generation, err := dns.Activate(ctx, deployment.ZoneDir, candidate, dns.ActivationOptions{}, control)
		if err != nil {
			return nil, fmt.Errorf("NSD activation failed: %w", err)
		}
		if _, err := fmt.Fprintln(out, generation); err != nil {
			return nil, err
		}
		return body, nil
	})
	if err != nil {
		return err
	}
	server, err := config.LoadServer(path)
	if err != nil {
		return err
	}
	renewal, err := config.LoadRenewal(path)
	if err != nil {
		return err
	}
	if err := renewCertificates(ctx, server, renewal, false, out); err != nil {
		return fmt.Errorf("certificate renewal failed after DNS activation: %w", err)
	}
	return nil
}

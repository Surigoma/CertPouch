package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/dns"
)

func runDNSSync(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dns sync", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to DNS YAML configuration")
	dryRun := flags.Bool("dry-run", false, "render candidate zones to stdout")
	stageDir := flags.String("stage-dir", "", "parent directory for a new nsd-checkzone-validated candidate")
	serialFrom := flags.String("serial-from", "", "advance the serials of the current managed DNS snapshot")
	serialFromBaseline := flags.String("serial-from-baseline", "", "advance the serials of a staged DNS snapshot")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *path == "" || flags.NArg() != 0 || (*dryRun == (*stageDir != "")) ||
		(*stageDir != "" && !filepath.IsAbs(*stageDir)) ||
		(*serialFromBaseline != "" && (*serialFrom != "" || !filepath.IsAbs(*serialFromBaseline))) {
		flags.Usage()
		return 2
	}
	zones, err := config.LoadDNSZones(*path, *serialFrom, *serialFromBaseline)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *dryRun {
		for _, z := range zones {
			if _, err := fmt.Fprintf(stdout, "; zone: %s\n%s\n", z.Name, z.Contents); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		return 0
	}
	staged, err := stageDNSZones(ctx, *stageDir, zones)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := fmt.Fprintln(stdout, staged); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func stageDNSZones(ctx context.Context, parent string, zones []dns.Zone) (string, error) {
	checker, err := exec.LookPath("nsd-checkzone")
	if err != nil {
		return "", fmt.Errorf("locate nsd-checkzone: %w", err)
	}
	dir, err := os.MkdirTemp(parent, "certpouch-zones-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			for _, zone := range zones {
				_ = os.Remove(filepath.Join(dir, zone.Name+".zone"))
			}
			_ = os.Remove(dir)
		}
	}()
	for _, zone := range zones {
		file := filepath.Join(dir, zone.Name+".zone")
		if err := os.WriteFile(file, zone.Contents, 0600); err != nil {
			return "", err
		}
		check, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := exec.CommandContext(check, checker, zone.Name, file).Run()
		cancel()
		if err != nil {
			return "", fmt.Errorf("nsd-checkzone rejected %s: %w", zone.Name, err)
		}
	}
	ok = true
	return dir, nil
}

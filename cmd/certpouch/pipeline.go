package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Surigoma/CertPouch/internal/config"
)

func runPipeline(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to server.yaml")
	candidate := flags.String("apply", "", "explicit candidate directory to apply to NSD")
	watch := flags.Bool("watch", false, "periodically fetch, apply, and renew")
	once := flags.Bool("once", false, "fetch, apply, and renew once")
	check := flags.Bool("check", false, "check local synchronization prerequisites without applying changes")
	if code, ok := parseFlags(flags, args, stdout, stderr); !ok {
		return code
	}
	modes := 0
	for _, selected := range []bool{*watch, *once, *check, *candidate != ""} {
		if selected {
			modes++
		}
	}
	if *path == "" || flags.NArg() != 0 || modes > 1 {
		flags.Usage()
		return 2
	}
	deployment, err := config.LoadDNSDeployment(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *watch || *once {
		return watchPipeline(ctx, *path, deployment, *once, stdout, stderr)
	}
	if *check {
		if err := checkPipeline(ctx, *path, deployment); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "Local synchronization prerequisites are valid.")
		return 0
	}
	if *candidate != "" {
		apply := []string{"--candidate", *candidate, "--zone-dir", deployment.ZoneDir,
			"--nsd-config", deployment.NSDConfig}
		if deployment.DNSAddress != "" {
			apply = append(apply, "--dns-address", deployment.DNSAddress)
		}
		return runDNSApply(ctx, apply, stdout, stderr)
	}
	if !filepath.IsAbs(deployment.StageDir) {
		fmt.Fprintln(stderr, "dns.nsd.stage_dir must be an absolute path")
		return 2
	}
	if code := runDNSFetch(ctx, []string{"--config", *path}, stderr, stderr); code != 0 {
		return code
	}
	sync := []string{"--config", *path, "--stage-dir", deployment.StageDir}
	if deployment.ZoneDir != "" {
		sync = append(sync, "--serial-from", deployment.ZoneDir)
	}
	return runDNSSync(ctx, sync, stdout, stderr)
}

func checkPipeline(ctx context.Context, path string, deployment config.DNSDeployment) error {
	fetch, err := config.LoadSheetsFetch(path)
	if err != nil {
		return err
	}
	for _, field := range []struct{ key, dir string }{
		{"dns.nsd.stage_dir", deployment.StageDir},
		{"dns.nsd.zone_dir", deployment.ZoneDir},
		{"the parent of inventory.cache_file", filepath.Dir(fetch.CacheFile)},
	} {
		info, err := os.Stat(field.dir)
		if !filepath.IsAbs(field.dir) || err != nil || !info.IsDir() {
			return fmt.Errorf("%s must be an existing absolute directory: %q", field.key, field.dir)
		}
	}
	if !filepath.IsAbs(deployment.NSDConfig) {
		return fmt.Errorf("dns.nsd.config_file must be an absolute path")
	}
	if deployment.NSDInclude != "" {
		if !filepath.IsAbs(deployment.NSDInclude) || filepath.Clean(deployment.NSDInclude) == filepath.Clean(deployment.NSDConfig) {
			return fmt.Errorf("dns.nsd.include_file must be an absolute path different from dns.nsd.config_file")
		}
		if _, err := readManagedNSDInclude(deployment.NSDInclude); err != nil {
			return err
		}
	}
	address := deployment.DNSAddress
	if address == "" {
		address = "127.0.0.1:53"
	}
	endpoint, err := parseNSDEndpoint(address)
	if err != nil {
		return err
	}
	if _, err := newNSDCommands(deployment.NSDConfig, endpoint); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "nsd-checkconf", deployment.NSDConfig).Run(); err != nil {
		return fmt.Errorf("validate NSD configuration: %w", err)
	}
	return nil
}

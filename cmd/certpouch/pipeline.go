package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Surigoma/CertPouch/internal/config"
)

func runPipeline(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to server.yaml")
	candidate := flags.String("apply", "", "explicit candidate directory to apply to NSD")
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
	deployment, err := config.LoadDNSDeployment(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *candidate != "" {
		apply := []string{"--candidate", *candidate, "--zone-dir", deployment.ZoneDir,
			"--nsd-config", deployment.NSDConfig, "--max-removed-records", "0"}
		if deployment.DNSAddress != "" {
			apply = append(apply, "--dns-address", deployment.DNSAddress)
		}
		return runDNSApply(ctx, apply, stdout, stderr)
	}
	if !filepath.IsAbs(deployment.StageDir) {
		fmt.Fprintln(stderr, "pipeline.dns.deployment.stage_dir must be absolute")
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

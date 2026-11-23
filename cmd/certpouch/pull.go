package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Surigoma/CertPouch/internal/client"
	"github.com/Surigoma/CertPouch/internal/config"
)

func runPull(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("pull", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to the client YAML configuration")
	dryRun := flags.Bool("dry-run", false, "preview certificate changes")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *path == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Usage: certpouch pull --dry-run --config PATH")
		return 2
	}
	if !*dryRun {
		fmt.Fprintln(stderr, "certificate installation is not implemented; use --dry-run")
		return 1
	}
	cfg, err := config.LoadClient(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	remote, err := client.New(cfg.URL, cfg.ID, cfg.Token, cfg.TLSRoots)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer remote.CloseIdleConnections()
	inventory, err := remote.Inventory(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	plan, err := client.Plan(cfg.Targets, inventory, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, item := range plan {
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.Name, item.Action, item.Version); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return 0
}

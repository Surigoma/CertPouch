package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Surigoma/CertPouch/internal/client"
	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/deployment"
	"github.com/Surigoma/CertPouch/internal/distribution"
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
		fmt.Fprintln(stderr, "Usage: certpouch pull [--dry-run] --config PATH")
		return 2
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
	targets := make(map[string]config.Target, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets[target.Name] = target
	}
	for _, item := range plan {
		if !*dryRun {
			item, err = applyTarget(ctx, remote, targets[item.Name], inventory)
			if err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", item.Name, err)
				return 1
			}
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.Name, item.Action, item.Version); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return 0
}

func applyTarget(ctx context.Context, remote *client.Client, target config.Target, inventory []distribution.Metadata) (item client.PlannedCertificate, err error) {
	item.Name = target.Name
	opts, apply, err := deployment.Configure(target.Destination, target.Owner, target.Group,
		target.CertificateMode, target.PrivateKeyMode, target.Unit, target.Action)
	if err != nil {
		return item, err
	}
	session, err := deployment.Begin(opts)
	if err != nil {
		return item, err
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	plan, err := client.Plan([]config.Target{target}, inventory, time.Now())
	if err != nil {
		return item, err
	}
	item = plan[0]
	if item.Action == "unchanged" {
		return item, nil
	}
	for _, metadata := range inventory {
		if metadata.Name != target.Name {
			continue
		}
		bundle, err := remote.Fetch(ctx, metadata, target.Policy)
		if err != nil {
			return item, err
		}
		return item, session.Install(ctx, metadata.Version, bundle, apply)
	}
	return item, fmt.Errorf("selected certificate is missing")
}

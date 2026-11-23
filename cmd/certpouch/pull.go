package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Surigoma/CertPouch/internal/client"
	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/deployment"
	"github.com/Surigoma/CertPouch/internal/distribution"
	"github.com/Surigoma/CertPouch/internal/pki"
	"github.com/Surigoma/CertPouch/internal/storage"
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
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	if !*dryRun {
		local, err := config.LoadLocalClient(*path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		for _, target := range local.Targets {
			if _, err := os.Lstat(filepath.Join(target.Destination, ".pull-lock")); os.IsNotExist(err) {
				continue
			}
			session, _, err := beginTarget(ctx, target)
			if err == nil {
				err = session.Close()
			}
			if err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", target.Name, err)
				return 1
			}
		}
	}
	cfg, err := config.LoadClient(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	remote, err := client.NewWithCertificate(cfg.URL, cfg.ID, cfg.Token, cfg.TLSRoots, cfg.TLSCertificate)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer remote.CloseIdleConnections()
	inventory, err := remote.Inventory(ctx)
	if err != nil {
		logger.Error("pull failed", "client_id", cfg.ID, "operation", "pull", "result", "failed", "error_category", "inventory")
		fmt.Fprintln(stderr, err)
		return 1
	}
	plan, err := client.Plan(cfg.Targets, inventory, time.Now())
	if err != nil {
		logger.Error("pull planning failed", "client_id", cfg.ID, "operation", "pull", "result", "failed", "error_category", "planning")
		fmt.Fprintln(stderr, err)
		return 1
	}
	targets := make(map[string]config.Target, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets[target.Name] = target
	}
	for _, item := range plan {
		if !*dryRun {
			item, err = applyTarget(ctx, remote, targets[item.Name], inventory, logger.With("client_id", cfg.ID))
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

func applyTarget(ctx context.Context, remote *client.Client, target config.Target, inventory []distribution.Metadata, logger *slog.Logger) (item client.PlannedCertificate, err error) {
	item.Name = target.Name
	session, apply, err := beginTarget(ctx, target)
	if err != nil {
		logger.Error("pull failed", "certificate_name", target.Name, "operation", "pull", "result", "failed", "error_category", "recovery")
		return item, err
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	category := "planning"
	defer func() {
		if err != nil {
			logger.Error("pull failed", "certificate_name", target.Name, "operation", "pull", "result", "failed", "error_category", category)
			err = errors.Join(err, deployment.RecordSyncFailure(target.StateFile, category))
		}
	}()
	plan, err := client.Plan([]config.Target{target}, inventory, time.Now())
	if err != nil {
		return item, err
	}
	item = plan[0]
	for _, metadata := range inventory {
		if metadata.Name != target.Name {
			continue
		}
		if item.Action != "unchanged" {
			category = "download"
			bundle, err := remote.Fetch(ctx, metadata, target.Policy)
			if err != nil {
				return item, err
			}
			category = "deployment"
			if err := session.Install(ctx, metadata.Version, bundle, apply); err != nil {
				return item, err
			}
		}
		category = "state"
		if err := deployment.RecordSync(target.StateFile, deployment.SyncState{
			Version: metadata.Version, Fingerprint: metadata.Fingerprint, SyncedAt: time.Now().UTC(),
		}); err != nil {
			return item, fmt.Errorf("record successful synchronization: %w", err)
		}
		return item, nil
	}
	return item, fmt.Errorf("selected certificate is missing")
}

func beginTarget(ctx context.Context, target config.Target) (*deployment.Session, func(context.Context) error, error) {
	opts, apply, err := deployment.Configure(target.Destination, target.Owner, target.Group,
		target.CertificateMode, target.PrivateKeyMode, target.Unit, target.Action)
	if err != nil {
		return nil, nil, err
	}
	if target.TLSAddress != "" {
		timeout := target.TLSVerifyTimeout
		if timeout == 0 {
			timeout = deployment.DefaultTLSProbeTimeout
		}
		probe, err := deployment.NewTLSProbe(target.TLSAddress, target.TLSServerName, target.Policy.Roots, timeout)
		if err != nil {
			return nil, nil, err
		}
		opts.RecoveryTimeout = deployment.SystemdTimeout + timeout
		systemd := apply
		apply = func(ctx context.Context) error {
			_, bundle, present, err := storage.LoadDeployment(target.Destination)
			if err != nil {
				return fmt.Errorf("load active generation for TLS verification: %w", err)
			}
			if !present {
				return fmt.Errorf("active generation is missing for TLS verification")
			}
			leaf, err := pki.DescribeLeaf(bundle.CertificatePEM)
			if err != nil {
				return err
			}
			if err := systemd(ctx); err != nil {
				return err
			}
			return probe.Wait(ctx, leaf.Fingerprint)
		}
	}
	session, err := deployment.BeginRecovering(ctx, opts, apply)
	return session, apply, err
}

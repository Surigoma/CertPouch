package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/distribution"
	"github.com/Surigoma/CertPouch/internal/pki"
	"github.com/Surigoma/CertPouch/internal/storage"
)

func runRenew(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cert renew", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serverPath := flags.String("config", "", "path to server YAML configuration")
	renewalPath := flags.String("renewal-config", "", "legacy path to a separate renewal configuration")
	preview := flags.Bool("dry-run", false, "show due certificates without opening the issuer key")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *serverPath == "" || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if !*preview && runtime.GOOS != "linux" {
		fmt.Fprintln(stderr, "certificate renewal requires Linux")
		return 1
	}
	if *renewalPath == "" {
		*renewalPath = *serverPath
	}
	server, err := config.LoadServer(*serverPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	renewal, err := config.LoadRenewal(*renewalPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := renewCertificates(ctx, server, renewal, *preview, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func renewCertificates(ctx context.Context, server config.Server, cfg config.Renewal, preview bool, out io.Writer) error {
	policies := make(map[string]pki.Policy, len(cfg.Profiles))
	profiles := slices.Clone(cfg.Profiles)
	slices.SortFunc(profiles, func(a, b config.RenewalProfile) int { return strings.Compare(a.Name, b.Name) })
	for _, profile := range profiles {
		policy, ok := server.Policies[profile.Name]
		if !ok {
			return fmt.Errorf("renewal profile %q is not configured on the server", profile.Name)
		}
		policies[profile.Name] = policy
	}
	published, err := storage.Load(server.CertificateDir, policies)
	if err != nil {
		return err
	}
	active := make(map[string]distribution.Publication, len(published))
	for _, generation := range published {
		active[generation.Name] = generation
	}
	now := time.Now().UTC()
	type decision struct {
		profile config.RenewalProfile
		action  string
	}
	var decisions []decision
	for _, profile := range profiles {
		action := "issue"
		if previous, ok := active[profile.Name]; ok {
			due, err := pki.RenewalDue(previous.Bundle, previous.Policy, now, profile.RenewBefore)
			if err != nil {
				return fmt.Errorf("%s: %w", profile.Name, err)
			}
			action = "unchanged"
			if due {
				action = "renew"
			}
		}
		decisions = append(decisions, decision{profile, action})
	}
	var issuer *pki.Issuer
	for _, d := range decisions {
		if err := ctx.Err(); err != nil {
			return err
		}
		profile := d.profile
		version := active[profile.Name].Version
		if !preview && d.action != "unchanged" {
			if issuer == nil {
				issuer, err = cfg.LoadIssuer(ctx)
				if err != nil {
					return err
				}
			}
			instant := time.Now().UTC()
			policy := policies[profile.Name]
			bundle, err := issuer.IssueServer(profile.Name, policy, instant, profile.Validity)
			if err != nil {
				return fmt.Errorf("%s: %w", profile.Name, err)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			version, err = storage.PublishIfCurrent(server.CertificateDir, profile.Name, policy, bundle, instant, version)
			if err != nil {
				return fmt.Errorf("%s: %w", profile.Name, err)
			}
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\n", profile.Name, d.action, version); err != nil {
			return err
		}
	}
	return nil
}

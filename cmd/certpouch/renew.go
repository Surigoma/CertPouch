package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/pki"
	"github.com/Surigoma/CertPouch/internal/storage"
)

func runRenew(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cert renew", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serverPath := flags.String("config", "", "path to server.yaml")
	preview := flags.Bool("dry-run", false, "show due certificates without opening the issuer key")
	if code, ok := parseFlags(flags, args, stdout, stderr); !ok {
		return code
	}
	if *serverPath == "" || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if !*preview && runtime.GOOS != "linux" {
		fmt.Fprintln(stderr, "certificate renewal requires Linux")
		return 1
	}
	server, renewal, err := config.LoadRenewalInputs(*serverPath)
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
	profiles := slices.Clone(cfg.Profiles)
	slices.SortFunc(profiles, func(a, b config.RenewalProfile) int { return strings.Compare(a.Name, b.Name) })
	if _, err := storage.Load(server.CertificateDir, nil); err != nil {
		return err
	}
	now := time.Now().UTC()
	var failures error
	var issuer *pki.Issuer
	for _, profile := range profiles {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		name := profile.Name
		policy, ok := server.Policies[name]
		if !ok {
			failures = errors.Join(failures, fmt.Errorf("renewal profile %q is not configured on the server", name))
			continue
		}
		action, version, err := planRenewal(server.CertificateDir, profile, policy, now)
		if err != nil {
			failures = errors.Join(failures, fmt.Errorf("%s: %w", name, err))
			continue
		}
		if !preview && action != "unchanged" {
			if issuer == nil {
				issuer, err = cfg.LoadIssuer(ctx)
				if err != nil {
					return errors.Join(failures, err)
				}
			}
			instant := time.Now().UTC()
			bundle, err := issuer.IssueServer(name, policy, instant, profile.Validity)
			if err == nil {
				if err := ctx.Err(); err != nil {
					return errors.Join(failures, err)
				}
				version, err = storage.PublishIfCurrent(server.CertificateDir, name, policy, bundle, instant, version)
			}
			if err != nil {
				failures = errors.Join(failures, fmt.Errorf("%s: %w", name, err))
				continue
			}
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\n", name, action, version); err != nil {
			return errors.Join(failures, err)
		}
	}
	return failures
}

func planRenewal(directory string, profile config.RenewalProfile, policy pki.Policy, now time.Time) (string, string, error) {
	published, err := storage.Load(directory, map[string]pki.Policy{profile.Name: policy})
	if err != nil {
		return "", "", err
	}
	if len(published) == 0 {
		return "issue", "", nil
	}
	previous := published[0]
	due, err := pki.RenewalDue(previous.Bundle, policy, now, profile.RenewBefore)
	if err != nil {
		return "", "", err
	}
	if due {
		return "renew", previous.Version, nil
	}
	return "unchanged", previous.Version, nil
}

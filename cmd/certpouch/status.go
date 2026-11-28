package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/deployment"
	"github.com/Surigoma/CertPouch/internal/pki"
	"github.com/Surigoma/CertPouch/internal/storage"
)

type certificateStatus struct {
	Name        string     `json:"name"`
	State       string     `json:"state"`
	Version     string     `json:"version,omitempty"`
	Subject     string     `json:"subject,omitempty"`
	Issuer      string     `json:"issuer,omitempty"`
	DNS         []string   `json:"dns_sans,omitempty"`
	IPs         []string   `json:"ip_sans,omitempty"`
	Expires     *time.Time `json:"not_after,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	LastSync    *time.Time `json:"last_successful_sync,omitempty"`
	LastError   string     `json:"last_error_category,omitempty"`
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to the client YAML configuration")
	if code, ok := parseFlags(flags, args, stdout, stderr); !ok {
		return code
	}
	if *path == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Usage: certpouch status --config PATH")
		return 2
	}
	cfg, err := config.LoadLocalClient(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	sort.Slice(cfg.Targets, func(i, j int) bool { return cfg.Targets[i].Name < cfg.Targets[j].Name })
	code := 0
	results := make([]certificateStatus, 0, len(cfg.Targets))
	now := time.Now()
	for _, target := range cfg.Targets {
		status := inspectStatus(target, now)
		if status.State != "installed" || status.LastError != "" {
			code = 1
		}
		results = append(results, status)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(results); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return code
}

func inspectStatus(target config.Target, now time.Time) certificateStatus {
	result := certificateStatus{Name: target.Name, State: "missing"}
	state, stateErr := deployment.ReadSync(target.StateFile)
	if stateErr == nil {
		result.LastError = state.LastError
	}
	lock := filepath.Join(target.Destination, ".pull-lock")
	if _, err := os.Lstat(filepath.Join(lock, "recovery")); err == nil {
		result.State = "recovery_pending"
		return result
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		result.State = "locked"
		return result
	}
	version, bundle, present, err := storage.LoadDeployment(target.Destination)
	if err != nil {
		result.State = "invalid"
		return result
	}
	if !present {
		return result
	}
	result.Version = version
	description, err := pki.DescribeLeaf(bundle.CertificatePEM)
	if err != nil {
		result.State = "invalid"
		return result
	}
	leaf := description.Leaf
	result.Subject, result.Issuer = leaf.Subject.String(), leaf.Issuer.String()
	result.DNS = leaf.DNSNames
	for _, ip := range leaf.IPAddresses {
		result.IPs = append(result.IPs, ip.String())
	}
	expiry := leaf.NotAfter.UTC()
	result.Expires, result.Fingerprint = &expiry, description.Fingerprint
	if stateErr == nil && state.Version == version && state.Fingerprint == result.Fingerprint {
		result.LastSync = &state.SyncedAt
	}
	if !now.Before(expiry) {
		result.State = "expired"
	} else if _, err := pki.Validate(bundle, target.Policy, now); err != nil {
		result.State = "invalid"
	} else if (stateErr != nil && !os.IsNotExist(stateErr)) || (stateErr == nil && state.Version != "" && result.LastSync == nil) {
		result.State = "state_mismatch"
	} else {
		result.State = "installed"
	}
	return result
}

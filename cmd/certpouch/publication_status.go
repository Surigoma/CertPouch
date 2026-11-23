package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/pki"
	"github.com/Surigoma/CertPouch/internal/storage"
)

type publicationStatus struct {
	Name        string     `json:"name"`
	Version     string     `json:"version,omitempty"`
	State       string     `json:"state"`
	NotBefore   *time.Time `json:"not_before,omitempty"`
	NotAfter    *time.Time `json:"not_after,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
}

func runPublicationStatus(args []string, stdout, stderr io.Writer, now time.Time) int {
	flags := flag.NewFlagSet("cert status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to server YAML configuration")
	threshold := flags.Duration("expires-within", 30*24*time.Hour, "warn if a certificate expires within this duration")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *path == "" || *threshold < 0 || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	directory, names, err := config.PublicationLocations(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	slices.Sort(names)
	for i, name := range names {
		if !storage.ValidPublicationName(name) || (i > 0 && name == names[i-1]) {
			fmt.Fprintln(stderr, "invalid or duplicate configured certificate name")
			return 2
		}
	}
	code := 0
	report := make([]publicationStatus, 0, len(names))
	for _, name := range names {
		item := inspectPublication(directory, name, now, *threshold)
		if item.State != "valid" {
			code = 1
		}
		report = append(report, item)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return code
}

func inspectPublication(directory, name string, now time.Time, threshold time.Duration) publicationStatus {
	version, body, err := storage.ReadPublicLeaf(directory, name)
	item := publicationStatus{Name: name, Version: version, State: "unreadable"}
	if err != nil {
		return item
	}
	if version == "" {
		item.State = "missing"
		return item
	}
	description, err := pki.DescribeLeaf(body)
	if err != nil {
		item.State = "invalid"
		return item
	}
	start, end := description.Leaf.NotBefore.UTC(), description.Leaf.NotAfter.UTC()
	item.NotBefore, item.NotAfter, item.Fingerprint = &start, &end, description.Fingerprint
	switch {
	case description.Leaf.IsCA || !start.Before(end):
		item.State = "invalid"
	case now.Before(start):
		item.State = "not_yet_valid"
	case !now.Before(end):
		item.State = "expired"
	case !end.After(now.Add(threshold)):
		item.State = "expiring"
	default:
		item.State = "valid"
	}
	return item
}

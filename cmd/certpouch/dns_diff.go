package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Surigoma/CertPouch/internal/dns"
)

func runDNSDiff(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dns diff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	candidate := flags.String("candidate", "", "candidate snapshot directory from dns sync")
	root := flags.String("zone-dir", "", "managed snapshot directory used by NSD")
	baseline := flags.String("baseline", "", "staged snapshot to compare before NSD initialization")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*candidate) || (*root == "") == (*baseline == "") ||
		(*root != "" && !filepath.IsAbs(*root)) || (*baseline != "" && !filepath.IsAbs(*baseline)) {
		flags.Usage()
		return 2
	}
	var changes []dns.ZoneChange
	var err error
	if *baseline != "" {
		changes, err = dns.DiffCandidates(*baseline, *candidate)
	} else {
		changes, err = dns.DiffSnapshots(*root, *candidate)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(changes); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/Surigoma/CertPouch/internal/update"
)

// Set to the stable tag by scripts/package-release.sh for published releases.
var releaseVersion = "dev"

func runUpdate(ctx context.Context, args []string, out, diagnostic io.Writer) int {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(diagnostic)
	check := flags.Bool("check", false, "compare with the latest stable GitHub release without modifying the executable")
	if code, ok := parseFlags(flags, args, out, diagnostic); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	operation := update.Apply
	if *check {
		operation = update.Latest
	}
	tag, changed, err := operation(ctx, releaseVersion)
	if err != nil {
		fmt.Fprintln(diagnostic, err)
		return 1
	}
	switch {
	case !changed:
		fmt.Fprintf(out, "installed %s, latest stable %s; no update\n", releaseVersion, tag)
	case *check:
		fmt.Fprintf(out, "update available: %s -> %s\n", releaseVersion, tag)
	default:
		fmt.Fprintf(out, "updated CertPouch: %s -> %s\n", releaseVersion, tag)
	}
	return 0
}

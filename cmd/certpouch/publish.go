package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/storage"
)

func runPublish(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cert publish", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to server YAML configuration")
	name := flags.String("name", "", "configured certificate name")
	certificate := flags.String("certificate", "", "leaf certificate PEM file")
	chain := flags.String("chain", "", "ordered intermediate CA PEM file")
	key := flags.String("private-key", "", "leaf private key PEM file")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *path == "" || *name == "" || *certificate == "" || *key == "" || flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	cfg, err := config.LoadServer(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	policy, ok := cfg.Policies[*name]
	if !ok {
		fmt.Fprintln(stderr, "certificate name is not configured")
		return 2
	}
	bundle, err := storage.ReadPublicationFiles(*certificate, *chain, *key)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	version, err := storage.Publish(cfg.CertificateDir, *name, policy, bundle, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := fmt.Fprintf(stdout, "%s\t%s\n", *name, version); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

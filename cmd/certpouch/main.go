package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/distribution"
	"github.com/Surigoma/CertPouch/internal/server"
	"github.com/Surigoma/CertPouch/internal/storage"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, commandHelp)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "pipeline":
		return runPipeline(ctx, rest, os.Stdout, stderr)
	case "configure":
		return runConfigure(rest, os.Stdin, os.Stdout, stderr)
	case "init":
		fmt.Fprintln(stderr, "Use certpouch configure server|client --help.")
		return 2
	case "dns", "cert":
		return runCommandGroup(ctx, args[0], rest, stderr)
	case "pull":
		return runPull(ctx, rest, os.Stdout, stderr)
	case "status":
		return runStatus(rest, os.Stdout, stderr)
	case "server":
		return runServer(ctx, rest, stderr)
	case "--help", "-h":
		if len(rest) == 0 {
			fmt.Fprint(stderr, commandHelp)
			return 0
		}
	}
	fmt.Fprint(stderr, commandHelp)
	return 2
}

func runCommandGroup(ctx context.Context, group string, args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, commandHelp)
		return 2
	}
	rest := args[1:]
	switch group + " " + args[0] {
	case "dns sync":
		return runDNSSync(ctx, rest, os.Stdout, stderr)
	case "dns fetch":
		return runDNSFetch(ctx, rest, os.Stdout, stderr)
	case "dns apply":
		return runDNSApply(ctx, rest, os.Stdout, stderr)
	case "dns diff":
		return runDNSDiff(rest, os.Stdout, stderr)
	case "cert publish":
		return runPublish(rest, os.Stdout, stderr)
	case "cert renew":
		return runRenew(ctx, rest, os.Stdout, stderr)
	case "cert status":
		return runPublicationStatus(rest, os.Stdout, stderr, time.Now())
	}
	if len(rest) == 0 && (args[0] == "--help" || args[0] == "-h") {
		switch group {
		case "dns":
			fmt.Fprintln(stderr, "Usage: certpouch dns fetch|sync|diff|apply --help")
		case "cert":
			fmt.Fprintln(stderr, "Usage: certpouch cert publish|renew|status --help")
		}
		return 0
	}
	fmt.Fprint(stderr, commandHelp)
	return 2
}

func runServer(ctx context.Context, args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to the server YAML configuration")
	check := flags.Bool("check-config", false, "validate configuration, TLS files, and published certificates without listening")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *path == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Usage: certpouch server --config PATH")
		return 2
	}
	cfg, err := config.LoadServer(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	publications, err := storage.Load(cfg.CertificateDir, cfg.Policies)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	var revocations *distribution.RevocationPolicy
	if cfg.RevocationFile != "" {
		entries, err := config.LoadRevocations(cfg.RevocationFile)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		revocations = &distribution.RevocationPolicy{}
		revocations.Replace(entries)
	}
	handler, err := server.NewPublicationHandlerWithRevocations(publications, cfg.Clients, logger, revocations)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *check {
		if _, err := tls.LoadX509KeyPair(cfg.TLSCertificate, cfg.TLSPrivateKey); err != nil {
			fmt.Fprintf(stderr, "check server TLS key pair: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "server configuration valid (%d published certificates)\n", len(publications))
		return 0
	}
	refreshContext, stopRefresh := context.WithCancel(ctx)
	defer stopRefresh()
	if cfg.PublicationRefresh != 0 {
		go handler.Watch(refreshContext, cfg.PublicationRefresh, func() ([]distribution.Publication, error) {
			if cfg.InventoryCertificates {
				current, err := config.LoadServer(*path)
				if err != nil {
					return nil, err
				}
				return storage.Load(current.CertificateDir, current.Policies)
			}
			return storage.Load(cfg.CertificateDir, cfg.Policies)
		})
	}
	if cfg.InventoryRefresh != 0 {
		loadClients := cfg.ReloadInventoryClients
		if cfg.InventoryCertificates {
			loadClients = func() ([]distribution.Client, error) {
				current, err := config.LoadServer(*path)
				return current.Clients, err
			}
		}
		go handler.WatchInventory(refreshContext, cfg.InventoryRefresh, loadClients)
	}
	if revocations != nil {
		go handler.WatchRevocations(refreshContext, cfg.RevocationRefresh, func() (distribution.Revocations, error) {
			return config.LoadRevocations(cfg.RevocationFile)
		})
	}
	if err := server.Run(ctx, cfg.Listen, cfg.TLSCertificate, cfg.TLSPrivateKey, cfg.TLSClientCAs, handler); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

const commandHelp = `Usage: certpouch <command> [options]

Setup and regular operation:
  configure dns|server|client   Generate initial configuration
  pipeline --watch              Synchronize Sheets, NSD, and certificates
  server                        Serve certificates over HTTPS
  pull                          Retrieve and deploy certificates
  status                        Inspect local deployment state

Manual operations and diagnostics:
  pipeline                      Stage or explicitly apply a DNS candidate
  dns fetch|sync|diff|apply     Run individual DNS operations
  cert publish|renew|status     Manage and inspect server certificates

Run certpouch <command> --help for options.
`

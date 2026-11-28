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
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches a command. Requested help goes to stdout with exit code 0;
// usage shown because of a missing or unknown command goes to stderr with 2.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, commandHelp)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "--version":
		if len(rest) == 0 {
			fmt.Fprintln(stdout, releaseVersion)
			return 0
		}
	case "help":
		if len(rest) == 0 {
			fmt.Fprint(stdout, commandHelp)
			return 0
		}
		if rest[0] == "help" || isHelp(rest[0]) {
			break
		}
		return run(ctx, append(append([]string(nil), rest...), "--help"), stdout, stderr)
	case "update":
		return runUpdate(ctx, rest, stdout, stderr)
	case "pipeline":
		return runPipeline(ctx, rest, stdout, stderr)
	case "configure":
		return runConfigure(rest, os.Stdin, stdout, stderr)
	case "init":
		fmt.Fprintln(stderr, "Use certpouch configure server|client --help.")
		return 2
	case "dns", "cert":
		return runCommandGroup(ctx, args[0], rest, stdout, stderr)
	case "pull":
		return runPull(ctx, rest, stdout, stderr)
	case "status":
		return runStatus(rest, stdout, stderr)
	case "server":
		return runServer(ctx, rest, stdout, stderr)
	default:
		if isHelp(args[0]) && len(rest) == 0 {
			fmt.Fprint(stdout, commandHelp)
			return 0
		}
	}
	fmt.Fprint(stderr, commandHelp)
	return 2
}

var groupHelp = map[string]string{
	"dns":  "Usage: certpouch dns fetch|sync|diff|apply [options]\n\nRun certpouch dns <command> --help for options.\n",
	"cert": "Usage: certpouch cert publish|renew|status [options]\n\nRun certpouch cert <command> --help for options.\n",
}

func runCommandGroup(ctx context.Context, group string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, groupHelp[group])
		return 2
	}
	rest := args[1:]
	switch group + " " + args[0] {
	case "dns sync":
		return runDNSSync(ctx, rest, stdout, stderr)
	case "dns fetch":
		return runDNSFetch(ctx, rest, stdout, stderr)
	case "dns apply":
		return runDNSApply(ctx, rest, stdout, stderr)
	case "dns diff":
		return runDNSDiff(rest, stdout, stderr)
	case "cert publish":
		return runPublish(rest, stdout, stderr)
	case "cert renew":
		return runRenew(ctx, rest, stdout, stderr)
	case "cert status":
		return runPublicationStatus(rest, stdout, stderr, time.Now())
	}
	if len(rest) == 0 && isHelp(args[0]) {
		fmt.Fprint(stdout, groupHelp[group])
		return 0
	}
	fmt.Fprint(stderr, groupHelp[group])
	return 2
}

func runServer(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to the server YAML configuration")
	check := flags.Bool("check-config", false, "validate configuration, TLS files, and published certificates without listening")
	if code, ok := parseFlags(flags, args, stdout, stderr); !ok {
		return code
	}
	if *path == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Usage: certpouch server --config PATH [--check-config]")
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
	if cfg.InventoryCertificates {
		go handler.WatchInventoryPublications(refreshContext, min(cfg.PublicationRefresh, cfg.InventoryRefresh), func() ([]distribution.Publication, []distribution.Client, error) {
			policies, clients, err := cfg.ReloadInventoryPublications()
			if err != nil {
				return nil, nil, err
			}
			publications, err := storage.Load(cfg.CertificateDir, policies)
			return publications, clients, err
		})
	} else {
		if cfg.PublicationRefresh != 0 {
			go handler.Watch(refreshContext, cfg.PublicationRefresh, func() ([]distribution.Publication, error) {
				return storage.Load(cfg.CertificateDir, cfg.Policies)
			})
		}
		if cfg.InventoryRefresh != 0 {
			go handler.WatchInventory(refreshContext, cfg.InventoryRefresh, cfg.ReloadInventoryClients)
		}
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
  update                        Check for or install a stable GitHub release
  --version                     Show the executable version
  pipeline                      Stage or explicitly apply a DNS candidate
  dns fetch|sync|diff|apply     Run individual DNS operations
  cert publish|renew|status     Manage and inspect server certificates

Run certpouch <command> --help or certpouch help <command> for options.
`

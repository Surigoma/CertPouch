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
	if len(args) >= 2 && args[0] == "dns" && args[1] == "sync" {
		return runDNSSync(ctx, args[2:], os.Stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "dns" && args[1] == "fetch" {
		return runDNSFetch(ctx, args[2:], os.Stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "dns" && args[1] == "apply" {
		return runDNSApply(ctx, args[2:], os.Stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "dns" && args[1] == "diff" {
		return runDNSDiff(args[2:], os.Stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "cert" && args[1] == "publish" {
		return runPublish(args[2:], os.Stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "cert" && args[1] == "renew" {
		return runRenew(ctx, args[2:], os.Stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "cert" && args[1] == "status" {
		return runPublicationStatus(args[2:], os.Stdout, stderr, time.Now())
	}
	if len(args) > 0 && args[0] == "pull" {
		return runPull(ctx, args[1:], os.Stdout, stderr)
	}
	if len(args) > 0 && args[0] == "status" {
		return runStatus(args[1:], os.Stdout, stderr)
	}
	if len(args) == 0 || args[0] != "server" {
		fmt.Fprintln(stderr, "Usage: certpouch server --config PATH\n       certpouch pull [--dry-run] --config PATH\n       certpouch status --config PATH\n       certpouch cert publish --help\n       certpouch cert renew --help\n       certpouch cert status --help\n       certpouch dns fetch --help\n       certpouch dns sync --help\n       certpouch dns apply --help\n       certpouch dns diff --help")
		if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			return 0
		}
		return 2
	}
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to the server YAML configuration")
	check := flags.Bool("check-config", false, "validate configuration, TLS files, and published certificates without listening")
	if err := flags.Parse(args[1:]); err != nil {
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
	handler, err := server.NewPublicationHandler(publications, cfg.Clients, logger)
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
			return storage.Load(cfg.CertificateDir, cfg.Policies)
		})
	}
	if err := server.Run(ctx, cfg.Listen, cfg.TLSCertificate, cfg.TLSPrivateKey, handler); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

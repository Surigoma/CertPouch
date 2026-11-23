package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Surigoma/CertPouch/internal/dns"
	"golang.org/x/net/dns/dnsmessage"
)

func runDNSApply(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dns apply", flag.ContinueOnError)
	flags.SetOutput(stderr)
	candidate := flags.String("candidate", "", "validated stage directory from dns sync")
	root := flags.String("zone-dir", "", "managed snapshot directory used by NSD")
	conf := flags.String("nsd-config", "", "absolute path to nsd.conf for NSD control")
	address := flags.String("dns-address", "127.0.0.1:53", "address of the authoritative NSD listener for SOA verification")
	initialize := flags.Bool("initialize", false, "prepare the first snapshot without contacting NSD")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*candidate) || !filepath.IsAbs(*root) ||
		(!*initialize && !filepath.IsAbs(*conf)) {
		flags.Usage()
		return 2
	}
	checker, err := exec.LookPath("nsd-checkzone")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	control := nsdCommands{checker: checker, config: *conf}
	if !*initialize {
		endpoint, err := netip.ParseAddrPort(*address)
		if err != nil || endpoint.Port() == 0 || !endpoint.Addr().Unmap().IsLoopback() {
			fmt.Fprintln(stderr, "--dns-address must be a loopback IP and port")
			return 2
		}
		control.address = endpoint.String()
		control.controller, err = exec.LookPath("nsd-control")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	generation, err := dns.Activate(ctx, *root, *candidate, *initialize, control)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := fmt.Fprintln(stdout, generation); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

type nsdCommands struct {
	checker, controller, config, address string
}

func (c nsdCommands) Check(ctx context.Context, zone, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, c.checker, zone, path).Run(); err != nil {
		return fmt.Errorf("nsd-checkzone rejected %s: %w", zone, err)
	}
	return nil
}

func (c nsdCommands) Reload(ctx context.Context, zone string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, c.controller, "-c", c.config, "reload", zone).Run(); err != nil {
		return fmt.Errorf("queue NSD reload for %s: %w", zone, err)
	}
	return nil
}

func (c nsdCommands) ServedSerial(ctx context.Context, zone string) (uint32, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	name, err := dnsmessage.NewName(zone + ".")
	if err != nil {
		return 0, err
	}
	var id [2]byte
	if _, err := rand.Read(id[:]); err != nil {
		return 0, err
	}
	question := dnsmessage.Question{Name: name, Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET}
	request := dnsmessage.Message{Header: dnsmessage.Header{ID: binary.BigEndian.Uint16(id[:])}, Questions: []dnsmessage.Question{question}}
	packet, err := request.Pack()
	if err != nil {
		return 0, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.address)
	if err != nil {
		return 0, fmt.Errorf("connect to authoritative DNS for %s: %w", zone, err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(packet)))
	if _, err := conn.Write(append(frame, packet...)); err != nil {
		return 0, err
	}
	if _, err := io.ReadFull(conn, id[:]); err != nil {
		return 0, err
	}
	responseBytes := make([]byte, binary.BigEndian.Uint16(id[:]))
	if _, err := io.ReadFull(conn, responseBytes); err != nil {
		return 0, err
	}
	var response dnsmessage.Message
	if err := response.Unpack(responseBytes); err != nil {
		return 0, err
	}
	if !response.Response || !response.Authoritative || response.Truncated || response.ID != request.ID ||
		response.RCode != dnsmessage.RCodeSuccess || len(response.Questions) != 1 ||
		response.Questions[0] != question || len(response.Answers) != 1 {
		return 0, fmt.Errorf("DNS listener did not return an authoritative SOA response for %s", zone)
	}
	answer := response.Answers[0]
	soa, ok := answer.Body.(*dnsmessage.SOAResource)
	if !ok || answer.Header.Name != name || answer.Header.Class != dnsmessage.ClassINET {
		return 0, fmt.Errorf("DNS listener did not return an exact SOA for %s", zone)
	}
	return soa.Serial, nil
}

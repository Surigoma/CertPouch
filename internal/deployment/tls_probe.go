package deployment

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// TLSProbe checks the service endpoint itself after applying a generation.
type TLSProbe struct {
	address string
	config  *tls.Config
	timeout time.Duration
}

const DefaultTLSProbeTimeout = 15 * time.Second

func NewTLSProbe(address, serverName string, roots *x509.CertPool, timeout time.Duration) (*TLSProbe, error) {
	host, port, err := net.SplitHostPort(address)
	number, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || host == "" || portErr != nil || number == 0 || roots == nil ||
		serverName == "" || strings.ContainsAny(serverName, "*/\\ \t\r\n") {
		return nil, fmt.Errorf("TLS verification requires a host:port address, a server name, and trusted roots")
	}
	if timeout == 0 {
		timeout = DefaultTLSProbeTimeout
	}
	if timeout < time.Second || timeout > 2*time.Minute {
		return nil, fmt.Errorf("TLS verification timeout must be between 1s and 2m")
	}
	return &TLSProbe{
		address: address,
		config:  &tls.Config{ServerName: serverName, RootCAs: roots.Clone(), MinVersion: tls.VersionTLS12},
		timeout: timeout,
	}, nil
}

func (p *TLSProbe) Wait(ctx context.Context, expected string) error {
	if !fingerPattern.MatchString(expected) {
		return fmt.Errorf("invalid expected TLS fingerprint")
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("service TLS did not serve the expected certificate: %w", errors.Join(err, last))
		}
		attempt, cancelAttempt := context.WithTimeout(ctx, 2*time.Second)
		last = p.verify(attempt, expected)
		cancelAttempt()
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func (p *TLSProbe) verify(ctx context.Context, expected string) error {
	dialer := tls.Dialer{Config: p.config}
	conn, err := dialer.DialContext(ctx, "tcp", p.address)
	if err != nil {
		return err
	}
	defer conn.Close()
	peers := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(peers) == 0 {
		return fmt.Errorf("service returned no TLS certificate")
	}
	digest := sha256.Sum256(peers[0].Raw)
	if hex.EncodeToString(digest[:]) != expected {
		return fmt.Errorf("service returned a different leaf certificate")
	}
	return nil
}

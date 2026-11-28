package client

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/distribution"
	"github.com/Surigoma/CertPouch/internal/pki"
)

func TestInventoryAndDownloadGuards(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	now := time.Now().UTC()
	metadata := distribution.Metadata{
		Name: "web.example.internal", Version: strings.Repeat("a", 32),
		Fingerprint: strings.Repeat("b", 64), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		ArtifactSHA256: make(map[string]string),
	}
	artifacts := make(map[string]string)
	for _, name := range artifactNames {
		artifacts[name] = name
		digest := sha256.Sum256([]byte(name))
		metadata.ArtifactSHA256[name] = hex.EncodeToString(digest[:])
	}
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect was followed")
	}))
	defer other.Close()
	var mode = make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("X-CertPouch-Client-ID") != "test-host" {
			t.Error("client credentials were not supplied")
		}
		switch <-mode {
		case "list":
			json.NewEncoder(w).Encode(map[string][]distribution.Metadata{"certificates": {metadata}})
		case "redirect":
			http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
		case "large":
			w.Write(bytes.Repeat([]byte{' '}, distribution.MaxDownloadBytes+1))
		case "generation":
			changed := metadata
			changed.Version = strings.Repeat("c", 32)
			json.NewEncoder(w).Encode(distribution.Download{Metadata: changed, Artifacts: artifacts})
		case "tampered":
			if r.URL.Path != inventoryPath+"/"+metadata.Name+"/download" || r.URL.Query().Get("version") != metadata.Version {
				t.Error("download did not pin the requested generation")
			}
			json.NewEncoder(w).Encode(distribution.Download{Metadata: metadata, Artifacts: map[string]string{
				"certificate.pem": "tampered", "chain.pem": artifacts["chain.pem"], "private-key.pem": artifacts["private-key.pem"],
			}})
		case "invalid PEM":
			json.NewEncoder(w).Encode(distribution.Download{Metadata: metadata, Artifacts: artifacts})
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	c, err := New(server.URL, "test-host", token, roots)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	ctx := context.Background()
	mode <- "list"
	inventory, err := c.Inventory(ctx)
	if err != nil || len(inventory) != 1 || inventory[0].Version != metadata.Version {
		t.Fatalf("inventory request failed: %v", err)
	}
	for _, tc := range []struct{ mode, want string }{
		{"redirect", "HTTP 307"}, {"large", "exceeds size limit"},
		{"generation", "selected generation"}, {"tampered", "checksum mismatch"}, {"invalid PEM", "validate downloaded certificate"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			mode <- tc.mode
			_, err := c.Fetch(ctx, inventory[0], pki.Policy{})
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), token) {
				t.Fatalf("expected safe %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestIPAuthenticationOverHTTPS(t *testing.T) {
	handler, err := distribution.NewHandler(nil, []distribution.Client{{
		ID: "ip-client", Enabled: true, Auth: distribution.AuthIP,
		AllowedIPs:          []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		AllowedCertificates: []string{"web.example.internal"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(handler)
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c, err := NewWithAuthentication(srv.URL, "ip-client", "", roots, nil, distribution.AuthIP)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if _, err := c.Inventory(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestIPAuthenticationWithoutClientID(t *testing.T) {
	handler, err := distribution.NewHandler(nil, []distribution.Client{{
		Enabled: true, Auth: distribution.AuthIP, Implicit: true,
		CertificateIPs: map[string][]netip.Prefix{"web.example.internal": {netip.MustParsePrefix("127.0.0.1/32")}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawID bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawID = r.Header["X-Certpouch-Client-Id"]
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c, err := NewWithAuthentication(srv.URL, "", "", roots, nil, distribution.AuthIP)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if _, err := c.Inventory(t.Context()); err != nil || sawID {
		t.Fatalf("inventory without a client ID: err=%v, sent ID header=%v", err, sawID)
	}
	if _, err := NewWithAuthentication(srv.URL, "", strings.Repeat("A", 43), roots, nil, distribution.AuthToken); err == nil {
		t.Fatal("accepted a token client without an ID")
	}
}

func TestMTLSAuthenticationWithoutToken(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(certificate)
	handler, err := distribution.NewHandler(nil, []distribution.Client{{
		ID: "mtls-client", Enabled: true, Auth: distribution.AuthMTLS,
		AllowedIPs:              []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		ClientCertificateHashes: [][sha256.Size]byte{sha256.Sum256(der)},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots}
	srv.StartTLS()
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	pair := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	c, err := NewWithAuthentication(srv.URL, "mtls-client", "", roots, pair, distribution.AuthMTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if _, err := c.Inventory(t.Context()); err != nil {
		t.Fatal(err)
	}
}

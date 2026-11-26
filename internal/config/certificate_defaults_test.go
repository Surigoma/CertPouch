package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

func writeTestIssuer(t *testing.T) string {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	issuer := filepath.Join(t.TempDir(), "issuer.pem")
	if err := os.WriteFile(issuer, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return issuer
}

func TestCertificatePolicyDefaults(t *testing.T) {
	issuer := writeTestIssuer(t)
	roots := x509.NewCertPool()
	doc := certificateDocument{Name: "web.example.internal"}
	policy, err := doc.policy(roots, issuer)
	if err != nil || policy.Issuer == nil || len(policy.DNS) != 1 || policy.DNS[0] != doc.Name {
		t.Fatalf("unexpected defaults: %+v, %v", policy, err)
	}
	doc.IPs = []string{"192.0.2.10"}
	policy, err = doc.policy(roots, issuer)
	if err != nil || len(policy.DNS) != 0 || len(policy.IPs) != 1 {
		t.Fatalf("explicit IP policy changed: %+v, %v", policy, err)
	}
	doc.IPs, doc.Name = nil, "logical_id"
	if _, err := doc.policy(roots, issuer); err == nil {
		t.Fatal("derived SAN from a non-hostname")
	}
}

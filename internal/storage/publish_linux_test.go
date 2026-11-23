package storage

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/pki"
)

func TestPublishPreservesCurrentOnValidationFailure(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	key := func() *rsa.PrivateKey {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	rootKey, leafKey := key(), key()
	issue := func(template, parent *x509.Certificate, public, signer *rsa.PrivateKey) *x509.Certificate {
		der, err := x509.CreateCertificate(rand.Reader, template, parent, &public.PublicKey, signer)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	root := issue(rootTemplate, rootTemplate, rootKey, rootKey)
	roots := x509.NewCertPool()
	roots.AddCert(root)
	const name = "web.example.internal"
	policy := pki.Policy{Roots: roots, Issuer: root, DNS: []string{name}}
	leaf := issue(&x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{name}, NotBefore: now.Add(-time.Minute),
		NotAfter: now.Add(time.Minute), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, root, leafKey, rootKey)
	bundle := pki.Bundle{
		CertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}),
		PrivateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}),
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	first, err := Publish(dir, name, policy, bundle, now)
	if err != nil {
		t.Fatal(err)
	}
	bad := bundle
	bad.PrivateKeyPEM = []byte("wrong key")
	if _, err := Publish(dir, name, policy, bad, now); err == nil {
		t.Fatal("invalid private key was accepted")
	}
	target, err := os.Readlink(filepath.Join(dir, name, "current"))
	if err != nil || target != filepath.Join("versions", first) {
		t.Fatalf("invalid update changed current: %q, %v", target, err)
	}
	second, err := Publish(dir, name, policy, bundle, now)
	if err != nil || second == first {
		t.Fatalf("second publication did not create a generation: %q, %v", second, err)
	}
	current, err := Load(dir, map[string]pki.Policy{name: policy})
	if err != nil || len(current) != 1 || current[0].Version != second {
		t.Fatalf("storage did not expose the new generation: %v, %v", len(current), err)
	}
	stat, err := os.Stat(filepath.Join(dir, name, "versions", first, "private-key.pem"))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("previous generation or key permissions changed: %v, %v", stat, err)
	}
}

package pki

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestIssueAndRenewServerCertificates(t *testing.T) {
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test signing CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	issuer, err := NewIssuer(certPEM, keyPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	const host, alias = "node.example.internal", "web.example.internal"
	const validity = 8 * time.Hour
	ip := netip.MustParseAddr("192.0.2.10")
	hostPolicy := Policy{Issuer: ca, Roots: roots, DNS: []string{host}, IPs: []netip.Addr{ip}}
	aliasPolicy := Policy{Issuer: ca, Roots: roots, DNS: []string{alias}}
	hostBundle, err := issuer.IssueServer(host, hostPolicy, now, validity)
	if err != nil {
		t.Fatal(err)
	}
	aliasBundle, err := issuer.IssueServer(alias, aliasPolicy, now, validity)
	if err != nil {
		t.Fatal(err)
	}
	hostLeaf, err := Validate(hostBundle, hostPolicy, now)
	if err != nil {
		t.Fatal(err)
	}
	aliasLeaf, err := Validate(aliasBundle, aliasPolicy, now)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(hostBundle.PrivateKeyPEM, aliasBundle.PrivateKeyPEM) ||
		bytes.Equal(hostBundle.PrivateKeyPEM, keyPEM) || bytes.Equal(aliasBundle.PrivateKeyPEM, keyPEM) ||
		hostLeaf.Leaf.SerialNumber.Cmp(aliasLeaf.Leaf.SerialNumber) == 0 {
		t.Fatal("issuance reused a private key or certificate serial")
	}
	if len(hostBundle.ChainPEM) != 0 || len(aliasBundle.ChainPEM) != 0 {
		t.Fatal("self-signed CA was included in a leaf chain")
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		due  bool
	}{
		{"before renewal window", now.Add(6*time.Hour - time.Second), false},
		{"at renewal threshold", now.Add(6 * time.Hour), true},
		{"expired", now.Add(9 * time.Hour), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			due, err := RenewalDue(hostBundle, hostPolicy, tc.at, 2*time.Hour)
			if err != nil || due != tc.due {
				t.Fatalf("RenewalDue = %v, %v, want %v", due, err, tc.due)
			}
		})
	}
	mismatched := hostBundle
	mismatched.PrivateKeyPEM = aliasBundle.PrivateKeyPEM
	if _, err := RenewalDue(mismatched, hostPolicy, now.Add(validity), time.Hour); err == nil {
		t.Fatal("renewal accepted an expired bundle with a mismatched key")
	}
	if _, err := NewIssuer(certPEM, aliasBundle.PrivateKeyPEM, nil); err == nil {
		t.Fatal("accepted a signing key that did not match the CA")
	}
	for _, tc := range []struct {
		name      string
		policy    Policy
		validity  time.Duration
		wantError string
	}{
		{"missing issuer", Policy{Roots: roots, DNS: []string{host}}, validity, "signing CA does not match"},
		{"untrusted issuer", Policy{Issuer: ca, Roots: x509.NewCertPool(), DNS: []string{host}}, validity, "issuer chain"},
		{"outlives CA", hostPolicy, 25 * time.Hour, "outside the signing CA validity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := issuer.IssueServer(host, tc.policy, now, tc.validity); err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("IssueServer error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	makeKey := func() *rsa.PrivateKey {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	issue := func(template, issuer *x509.Certificate, key, signer *rsa.PrivateKey) *x509.Certificate {
		der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, signer)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	certPEM := func(cert *x509.Certificate) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	}
	keyPEM := func(key *rsa.PrivateKey) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	}
	caTemplate := func(serial int64, name string) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(2 * time.Hour),
			BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign,
		}
	}
	rootKey, issuerKey, leafKey := makeKey(), makeKey(), makeKey()
	rootTemplate := caTemplate(1, "Test root")
	root := issue(rootTemplate, rootTemplate, rootKey, rootKey)
	issuer := issue(caTemplate(2, "Test intermediate"), root, issuerKey, rootKey)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "web.example.internal"},
		DNSNames: []string{"web.example.internal"}, NotBefore: now.Add(-time.Minute),
		NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leaf := issue(leafTemplate, issuer, leafKey, issuerKey)
	roots := x509.NewCertPool()
	roots.AddCert(root)
	policy := Policy{Roots: roots, Issuer: issuer, DNS: []string{"WEB.example.internal."}}
	bundle := Bundle{certPEM(leaf), certPEM(issuer), keyPEM(leafKey)}
	got, err := Validate(bundle, policy, now)
	want := sha256.Sum256(leaf.Raw)
	if err != nil || got.Fingerprint != hex.EncodeToString(want[:]) {
		t.Fatalf("valid bundle: fingerprint = %q, err = %v", got.Fingerprint, err)
	}

	noEKU := *leafTemplate
	noEKU.ExtKeyUsage = nil
	withoutEKU := certPEM(issue(&noEKU, issuer, leafKey, issuerKey))
	for _, tc := range []struct {
		name  string
		alter func(*Bundle, *Policy)
		at    time.Time
	}{
		{"wrong private key", func(b *Bundle, _ *Policy) { b.PrivateKeyPEM = keyPEM(issuerKey) }, now},
		{"wrong SAN", func(_ *Bundle, p *Policy) { p.DNS = []string{"other.example.internal"} }, now},
		{"wrong issuer", func(_ *Bundle, p *Policy) { p.Issuer = root }, now},
		{"untrusted root", func(_ *Bundle, p *Policy) { p.Roots = x509.NewCertPool() }, now},
		{"missing intermediate", func(b *Bundle, _ *Policy) { b.ChainPEM = nil }, now},
		{"missing EKU", func(b *Bundle, _ *Policy) { b.CertificatePEM = withoutEKU }, now},
		{"extra PEM data", func(b *Bundle, _ *Policy) { b.CertificatePEM = append([]byte("garbage\n"), b.CertificatePEM...) }, now},
		{"expired", func(*Bundle, *Policy) {}, leaf.NotAfter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, p := bundle, policy
			tc.alter(&b, &p)
			if _, err := Validate(b, p, tc.at); err == nil {
				t.Fatal("invalid bundle accepted")
			}
		})
	}
}

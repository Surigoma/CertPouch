// Package pki validates certificate bundles before publication or deployment.
package pki

import (
	"bytes"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/netip"
	"time"
)

type Bundle struct {
	CertificatePEM []byte
	ChainPEM       []byte
	PrivateKeyPEM  []byte
}

type Policy struct {
	Roots  *x509.CertPool
	Issuer *x509.Certificate
	DNS    []string
	IPs    []netip.Addr
}

type Validated struct {
	Leaf        *x509.Certificate
	Fingerprint string
}

// DescribeLeaf parses public leaf metadata without asserting trust or validity.
func DescribeLeaf(data []byte) (Validated, error) {
	certs, err := parseCertificates(data)
	if err != nil || len(certs) != 1 {
		return Validated{}, fmt.Errorf("expected one PEM leaf certificate")
	}
	return describe(certs[0]), nil
}

func describe(leaf *x509.Certificate) Validated {
	fingerprint := sha256.Sum256(leaf.Raw)
	return Validated{Leaf: leaf, Fingerprint: hex.EncodeToString(fingerprint[:])}
}

// Validate checks the bundle against independently configured trust and names.
func Validate(bundle Bundle, policy Policy, now time.Time) (Validated, error) {
	if policy.Roots == nil || policy.Issuer == nil || now.IsZero() ||
		len(policy.DNS)+len(policy.IPs) == 0 {
		return Validated{}, fmt.Errorf("incomplete certificate validation policy")
	}
	certs, err := parseCertificates(bundle.CertificatePEM)
	if err != nil || len(certs) != 1 {
		return Validated{}, fmt.Errorf("expected one PEM leaf certificate")
	}
	leaf := certs[0]
	key, err := parseKey(bundle.PrivateKeyPEM)
	if err != nil {
		return Validated{}, err
	}
	if leaf.IsCA || !key.PublicKey.Equal(leaf.PublicKey) {
		return Validated{}, fmt.Errorf("expected a server certificate matching the private key")
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return Validated{}, fmt.Errorf("certificate is outside its validity period")
	}
	serverAuth := false
	for _, usage := range leaf.ExtKeyUsage {
		serverAuth = serverAuth || usage == x509.ExtKeyUsageServerAuth
	}
	if !serverAuth {
		return Validated{}, fmt.Errorf("certificate lacks server authentication EKU")
	}
	if !matchesNames(leaf, policy) {
		return Validated{}, fmt.Errorf("certificate SANs do not match policy")
	}
	if !issuedBy(leaf, policy.Issuer) {
		return Validated{}, fmt.Errorf("certificate does not match expected issuer")
	}

	chain, err := parseCertificates(bundle.ChainPEM)
	if err != nil {
		return Validated{}, fmt.Errorf("invalid intermediate certificate PEM")
	}
	if len(chain) > 0 && !chain[0].Equal(policy.Issuer) {
		return Validated{}, fmt.Errorf("chain does not begin with the expected issuer")
	}
	intermediates := x509.NewCertPool()
	child := leaf
	for _, parent := range chain {
		if !issuedBy(child, parent) || issuedBy(parent, parent) {
			return Validated{}, fmt.Errorf("invalid intermediate ordering or root included in chain")
		}
		intermediates.AddCert(parent)
		child = parent
	}
	paths, err := leaf.Verify(x509.VerifyOptions{
		Roots: policy.Roots, Intermediates: intermediates, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return Validated{}, fmt.Errorf("certificate chain is not trusted or valid")
	}
	for _, path := range paths {
		if len(path) != len(chain)+2 || !path[1].Equal(policy.Issuer) {
			continue
		}
		matches := true
		for i, intermediate := range chain {
			matches = matches && intermediate.Equal(path[i+1])
		}
		if matches {
			return describe(leaf), nil
		}
	}
	return Validated{}, fmt.Errorf("verified chain does not match the supplied intermediate chain and issuer")
}

func issuedBy(child, parent *x509.Certificate) bool {
	return bytes.Equal(child.RawIssuer, parent.RawSubject) && child.CheckSignatureFrom(parent) == nil
}

// decodePEM requires the next non-whitespace line to start a PEM block.
// pem.Decode alone can skip malformed blocks and unrelated leading text.
func decodePEM(data []byte) (*pem.Block, []byte) {
	data = bytes.TrimSpace(data)
	if !bytes.HasPrefix(data, []byte("-----BEGIN ")) {
		return nil, nil
	}
	block, rest := pem.Decode(data)
	if block == nil || len(block.Headers) != 0 {
		return nil, nil
	}
	consumed := data[:len(data)-len(rest)]
	if bytes.Count(consumed, []byte("-----BEGIN ")) != 1 {
		return nil, nil
	}
	return block, rest
}

func parseCertificates(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for len(bytes.TrimSpace(data)) != 0 {
		block, rest := decodePEM(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("expected a certificate PEM block")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
		data = rest
	}
	return certs, nil
}

func parseKey(data []byte) (*rsa.PrivateKey, error) {
	block, rest := decodePEM(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("expected exactly one private key PEM block")
	}
	var key *rsa.PrivateKey
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var parsed any
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		key, _ = parsed.(*rsa.PrivateKey)
	}
	if err != nil || key == nil || key.N.BitLen() < 2048 {
		return nil, fmt.Errorf("expected a valid RSA private key of at least 2048 bits")
	}
	return key, nil
}

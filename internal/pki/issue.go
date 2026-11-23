package pki

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// Issuer signs with an operator-provided CA. Its key never enters a leaf bundle.
type Issuer struct {
	certificate *x509.Certificate
	key         *rsa.PrivateKey
	chain       []byte
}

func NewIssuer(certificatePEM, privateKeyPEM, chainPEM []byte) (*Issuer, error) {
	certs, err := parseCertificates(certificatePEM)
	if err != nil || len(certs) != 1 || !certs[0].IsCA || certs[0].KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, fmt.Errorf("expected one CA certificate authorized to sign certificates")
	}
	key, err := parseKey(privateKeyPEM)
	if err != nil || !key.PublicKey.Equal(certs[0].PublicKey) {
		return nil, fmt.Errorf("CA private key does not match the signing certificate")
	}
	chain, err := parseCertificates(chainPEM)
	if err != nil {
		return nil, fmt.Errorf("invalid CA chain PEM")
	}
	selfSigned := issuedBy(certs[0], certs[0])
	if (selfSigned && len(chain) != 0) || (!selfSigned && (len(chain) == 0 || !chain[0].Equal(certs[0]))) {
		return nil, fmt.Errorf("intermediate chain must begin with the signing CA and omit the root")
	}
	return &Issuer{certificate: certs[0], key: key, chain: append([]byte(nil), chainPEM...)}, nil
}

// IssueServer issues a new RSA leaf with a distinct key and a random serial.
// The configured policy is checked before the bundle can be published.
func (i *Issuer) IssueServer(name string, policy Policy, now time.Time, validity time.Duration) (Bundle, error) {
	now = now.UTC().Truncate(time.Second)
	if policy.Issuer == nil || !i.certificate.Equal(policy.Issuer) {
		return Bundle{}, fmt.Errorf("signing CA does not match the configured certificate issuer")
	}
	if validity < time.Second || now.Before(i.certificate.NotBefore) ||
		!now.Before(i.certificate.NotAfter) || now.Add(validity).After(i.certificate.NotAfter) {
		return Bundle{}, fmt.Errorf("requested validity falls outside the signing CA validity period")
	}
	if err := validateIssuerChain(i.certificate, i.chain, policy.Roots, now, now.Add(validity)); err != nil {
		return Bundle{}, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return Bundle{}, fmt.Errorf("generate leaf key: %w", err)
	}
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, new(big.Int).Sub(limit, big.NewInt(1)))
	if err != nil {
		return Bundle{}, fmt.Errorf("generate certificate serial: %w", err)
	}
	serial.Add(serial, big.NewInt(1))
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name},
		NotBefore: now, NotAfter: now.Add(validity),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              append([]string(nil), policy.DNS...),
	}
	for _, address := range policy.IPs {
		template.IPAddresses = append(template.IPAddresses, net.IP(address.AsSlice()))
	}
	der, err := x509.CreateCertificate(rand.Reader, template, i.certificate, &key.PublicKey, i.key)
	if err != nil {
		return Bundle{}, fmt.Errorf("sign leaf certificate: %w", err)
	}
	bundle := Bundle{
		CertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		ChainPEM:       append([]byte(nil), i.chain...),
		PrivateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	}
	if _, err := Validate(bundle, policy, now); err != nil {
		return Bundle{}, fmt.Errorf("validate newly issued certificate: %w", err)
	}
	return bundle, nil
}

func validateIssuerChain(issuer *x509.Certificate, chainPEM []byte, roots *x509.CertPool, at ...time.Time) error {
	if roots == nil {
		return fmt.Errorf("issuer trust roots are required")
	}
	chain, err := parseCertificates(chainPEM)
	if err != nil {
		return err
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range chain {
		if !certificate.Equal(issuer) {
			intermediates.AddCert(certificate)
		}
	}
	for _, instant := range at {
		_, err = issuer.Verify(x509.VerifyOptions{
			Roots: roots, Intermediates: intermediates, CurrentTime: instant,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		if err != nil {
			return fmt.Errorf("issuer chain cannot cover the requested validity")
		}
	}
	return nil
}

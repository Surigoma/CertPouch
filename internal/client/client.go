// Package client retrieves and validates bundles from a CertPouch server.
package client

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Surigoma/CertPouch/internal/distribution"
	"github.com/Surigoma/CertPouch/internal/pki"
)

var (
	namePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	versionPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const inventoryPath = "/api/v1/certificates"

type Client struct {
	origin url.URL
	id     string
	token  string
	http   *http.Client
}

func New(origin, id, token string, roots *x509.CertPool) (*Client, error) {
	return NewWithCertificate(origin, id, token, roots, nil)
}

func NewWithCertificate(origin, id, token string, roots *x509.CertPool, certificate *tls.Certificate) (*Client, error) {
	return NewWithAuthentication(origin, id, token, roots, certificate, distribution.AuthToken)
}

func NewWithAuthentication(origin, id, token string, roots *x509.CertPool, certificate *tls.Certificate, auth distribution.AuthMode) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.EscapedPath() != "" && u.EscapedPath() != "/") || u.RawQuery != "" ||
		u.ForceQuery || strings.Contains(origin, "#") {
		return nil, fmt.Errorf("expected an HTTPS server origin")
	}
	// An ip client may omit its ID and rely on the server's inventory IP match.
	if roots == nil || !auth.Valid() || !(namePattern.MatchString(id) || (id == "" && auth == distribution.AuthIP)) {
		return nil, fmt.Errorf("invalid client configuration")
	}
	if auth.UsesToken() {
		if len(token) > 1024 || strings.ContainsAny(token, "\r\n") {
			return nil, fmt.Errorf("invalid client token")
		}
		raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
		if err != nil || len(raw) < 32 {
			return nil, fmt.Errorf("invalid client token")
		}
	} else if token != "" || (auth == distribution.AuthIP && certificate != nil) ||
		(auth == distribution.AuthMTLS && certificate == nil) {
		return nil, fmt.Errorf("invalid credentials for auth %s", auth)
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots.Clone()},
	}
	if certificate != nil {
		if len(certificate.Certificate) == 0 || certificate.PrivateKey == nil {
			return nil, fmt.Errorf("invalid mTLS client key pair")
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{*certificate}
	}
	return &Client{
		origin: *u, id: id, token: token,
		http: &http.Client{
			Transport: transport, Timeout: 60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *Client) CloseIdleConnections() {
	c.http.CloseIdleConnections()
}

func (c *Client) Inventory(ctx context.Context) ([]distribution.Metadata, error) {
	var response struct {
		Certificates []distribution.Metadata `json:"certificates"`
	}
	if err := c.get(ctx, inventoryPath, "", &response); err != nil {
		return nil, err
	}
	if response.Certificates == nil {
		return nil, fmt.Errorf("missing certificate inventory")
	}
	seen := make(map[string]bool, len(response.Certificates))
	for _, item := range response.Certificates {
		if !validMetadata(item) || seen[item.Name] {
			return nil, fmt.Errorf("invalid or duplicate inventory entry")
		}
		seen[item.Name] = true
	}
	return response.Certificates, nil
}

// Fetch verifies a fixed generation against the inventory and local trust.
func (c *Client) Fetch(ctx context.Context, expected distribution.Metadata, policy pki.Policy) (pki.Bundle, error) {
	if !validMetadata(expected) {
		return pki.Bundle{}, fmt.Errorf("invalid expected certificate metadata")
	}
	var response distribution.Download
	path := inventoryPath + "/" + expected.Name + "/download"
	if err := c.get(ctx, path, url.Values{"version": {expected.Version}}.Encode(), &response); err != nil {
		return pki.Bundle{}, err
	}
	actual := response.Metadata
	if !validMetadata(actual) || actual.Name != expected.Name || actual.Version != expected.Version ||
		actual.Fingerprint != expected.Fingerprint || !maps.Equal(actual.ArtifactSHA256, expected.ArtifactSHA256) {
		return pki.Bundle{}, fmt.Errorf("download metadata does not match the selected generation")
	}
	if len(response.Artifacts) != len(artifactNames) {
		return pki.Bundle{}, fmt.Errorf("unexpected certificate artifacts")
	}
	for _, name := range artifactNames {
		data, ok := response.Artifacts[name]
		digest := sha256.Sum256([]byte(data))
		if !ok || hex.EncodeToString(digest[:]) != expected.ArtifactSHA256[name] {
			return pki.Bundle{}, fmt.Errorf("certificate artifact checksum mismatch")
		}
	}
	bundle := pki.Bundle{
		CertificatePEM: []byte(response.Artifacts["certificate.pem"]),
		ChainPEM:       []byte(response.Artifacts["chain.pem"]),
		PrivateKeyPEM:  []byte(response.Artifacts["private-key.pem"]),
	}
	validated, err := pki.Validate(bundle, policy, time.Now())
	if err != nil {
		return pki.Bundle{}, fmt.Errorf("validate downloaded certificate: %w", err)
	}
	if validated.Fingerprint != expected.Fingerprint {
		return pki.Bundle{}, fmt.Errorf("leaf certificate fingerprint mismatch")
	}
	return bundle, nil
}

var artifactNames = [...]string{"certificate.pem", "chain.pem", "private-key.pem"}

func validMetadata(item distribution.Metadata) bool {
	if !namePattern.MatchString(item.Name) || !versionPattern.MatchString(item.Version) ||
		!digestPattern.MatchString(item.Fingerprint) || !item.NotBefore.Before(item.NotAfter) ||
		len(item.ArtifactSHA256) != len(artifactNames) {
		return false
	}
	for _, name := range artifactNames {
		if !digestPattern.MatchString(item.ArtifactSHA256[name]) {
			return false
		}
	}
	return true
}

func (c *Client) get(ctx context.Context, path, query string, target any) error {
	u := c.origin
	u.Path, u.RawPath, u.RawQuery = path, "", query
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("create distribution request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.id != "" {
		req.Header.Set("X-CertPouch-Client-ID", c.id)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("distribution HTTPS request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("distribution request returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, distribution.MaxDownloadBytes+1))
	if err != nil {
		return fmt.Errorf("read distribution response: %w", err)
	}
	if len(body) > distribution.MaxDownloadBytes {
		return fmt.Errorf("distribution response exceeds size limit")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("invalid distribution JSON response")
	}
	return nil
}

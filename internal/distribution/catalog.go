package distribution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/Surigoma/CertPouch/internal/pki"
)

const MaxDownloadBytes = 4 << 20

var (
	namePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	versionPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type Metadata struct {
	Name           string            `json:"name"`
	Version        string            `json:"version"`
	Fingerprint    string            `json:"fingerprint"`
	NotBefore      time.Time         `json:"not_before"`
	NotAfter       time.Time         `json:"not_after"`
	Subject        string            `json:"subject"`
	DNSSANs        []string          `json:"dns_sans"`
	IPSANs         []string          `json:"ip_sans"`
	Issuer         string            `json:"issuer"`
	ArtifactSHA256 map[string]string `json:"artifact_sha256"`
}

type Download struct {
	Metadata  Metadata          `json:"metadata"`
	Artifacts map[string]string `json:"artifacts"`
}

type Publication struct {
	Name    string
	Version string
	Current bool
	Bundle  pki.Bundle
	Policy  pki.Policy
}

type generation struct {
	metadata Metadata
	body     []byte
}

type catalog struct {
	current  map[string]*generation
	versions map[string]map[string]*generation
}

func buildCatalog(publications []Publication, now time.Time) (catalog, error) {
	c := catalog{current: make(map[string]*generation), versions: make(map[string]map[string]*generation)}
	for _, publication := range publications {
		name, version := publication.Name, publication.Version
		if !namePattern.MatchString(name) || !versionPattern.MatchString(version) {
			return catalog{}, fmt.Errorf("invalid publication name or version")
		}
		if c.versions[name][version] != nil || (publication.Current && c.current[name] != nil) {
			return catalog{}, fmt.Errorf("duplicate publication version or current generation")
		}
		entry, err := buildGeneration(publication, now)
		if err != nil {
			return catalog{}, fmt.Errorf("validate publication %s: %w", name, err)
		}
		if c.versions[name] == nil {
			c.versions[name] = make(map[string]*generation)
		}
		c.versions[name][version] = entry
		if publication.Current {
			c.current[name] = entry
		}
	}
	return c, nil
}

func buildGeneration(publication Publication, now time.Time) (*generation, error) {
	bundle := publication.Bundle
	if len(bundle.CertificatePEM)+len(bundle.ChainPEM)+len(bundle.PrivateKeyPEM) > MaxDownloadBytes {
		return nil, fmt.Errorf("publication exceeds download limit")
	}
	validated, err := pki.Validate(bundle, publication.Policy, now)
	if err != nil {
		return nil, err
	}
	leaf := validated.Leaf
	metadata := Metadata{
		Name: publication.Name, Version: publication.Version, Fingerprint: validated.Fingerprint,
		NotBefore: leaf.NotBefore.UTC(), NotAfter: leaf.NotAfter.UTC(), Subject: leaf.Subject.String(),
		DNSSANs: append([]string{}, leaf.DNSNames...), IPSANs: []string{}, Issuer: leaf.Issuer.String(),
		ArtifactSHA256: make(map[string]string),
	}
	for _, ip := range leaf.IPAddresses {
		metadata.IPSANs = append(metadata.IPSANs, ip.String())
	}
	artifacts := map[string]string{
		"certificate.pem": string(bundle.CertificatePEM),
		"chain.pem":       string(bundle.ChainPEM),
		"private-key.pem": string(bundle.PrivateKeyPEM),
	}
	for name, data := range artifacts {
		digest := sha256.Sum256([]byte(data))
		metadata.ArtifactSHA256[name] = hex.EncodeToString(digest[:])
	}
	body, err := json.Marshal(Download{Metadata: metadata, Artifacts: artifacts})
	if err != nil {
		return nil, fmt.Errorf("encode publication: %w", err)
	}
	if len(body) > MaxDownloadBytes {
		return nil, fmt.Errorf("encoded publication exceeds download limit")
	}
	return &generation{metadata: metadata, body: body}, nil
}

func (g *generation) available(now time.Time) bool {
	return g != nil && !now.Before(g.metadata.NotBefore) && now.Before(g.metadata.NotAfter)
}

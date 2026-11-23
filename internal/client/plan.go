package client

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/distribution"
	"github.com/Surigoma/CertPouch/internal/pki"
	"github.com/Surigoma/CertPouch/internal/storage"
)

type PlannedCertificate struct {
	Name    string
	Version string
	Action  string
}

// Plan compares the local targets to authorized metadata without downloading keys.
func Plan(targets []config.Target, inventory []distribution.Metadata, now time.Time) ([]PlannedCertificate, error) {
	byName := make(map[string]distribution.Metadata, len(inventory))
	for _, entry := range inventory {
		byName[entry.Name] = entry
	}
	plan := make([]PlannedCertificate, 0, len(targets))
	for _, target := range targets {
		remote, ok := byName[target.Name]
		if !ok {
			return nil, fmt.Errorf("configured certificate %q is absent from the authorized inventory", target.Name)
		}
		if now.Before(remote.NotBefore) || !now.Before(remote.NotAfter) {
			return nil, fmt.Errorf("remote certificate %q is outside its validity period", target.Name)
		}
		version, bundle, present, err := storage.LoadDeployment(target.Destination)
		if err != nil {
			return nil, fmt.Errorf("inspect local certificate %q: %w", target.Name, err)
		}
		action := "install"
		if present {
			action = "update"
			if version == remote.Version {
				if !artifactHashesMatch(bundle, remote.ArtifactSHA256) {
					return nil, fmt.Errorf("current generation of %q differs from the published artifacts for the same version; restore it from a verified backup or publish a new generation", target.Name)
				}
				validated, err := pki.Validate(bundle, target.Policy, now)
				if err != nil || validated.Fingerprint != remote.Fingerprint {
					return nil, fmt.Errorf("current generation of %q failed local certificate validation", target.Name)
				}
				action = "unchanged"
			}
		}
		plan = append(plan, PlannedCertificate{Name: target.Name, Version: remote.Version, Action: action})
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].Name < plan[j].Name })
	return plan, nil
}

func artifactHashesMatch(bundle pki.Bundle, expected map[string]string) bool {
	for name, data := range map[string][]byte{
		"certificate.pem": bundle.CertificatePEM, "chain.pem": bundle.ChainPEM,
		"private-key.pem": bundle.PrivateKeyPEM,
	} {
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != expected[name] {
			return false
		}
	}
	return true
}

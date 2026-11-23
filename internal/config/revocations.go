package config

import (
	"fmt"
	"regexp"

	"github.com/Surigoma/CertPouch/internal/distribution"
)

var revocationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func LoadRevocations(path string) (distribution.Revocations, error) {
	body, err := readFile(path, false, 1<<20)
	if err != nil {
		return distribution.Revocations{}, fmt.Errorf("read revocation list: %w", err)
	}
	var document struct {
		ClientIDs    []string `yaml:"client_ids"`
		Certificates []string `yaml:"client_certificate_sha256"`
	}
	if err := decodeYAML(body, &document); err != nil {
		return distribution.Revocations{}, fmt.Errorf("decode revocation list: %w", err)
	}
	if document.ClientIDs == nil || document.Certificates == nil {
		return distribution.Revocations{}, fmt.Errorf("revocation list requires explicit client_ids and client_certificate_sha256 arrays")
	}
	ids := make(map[string]bool)
	for i, id := range document.ClientIDs {
		if !revocationID.MatchString(id) || ids[id] {
			return distribution.Revocations{}, fmt.Errorf("invalid or duplicate revoked client ID at index %d", i)
		}
		ids[id] = true
	}
	hashes, err := parseCertificateHashes(document.Certificates)
	if err != nil {
		return distribution.Revocations{}, fmt.Errorf("invalid revocation list: %w", err)
	}
	return distribution.Revocations{ClientIDs: document.ClientIDs, CertificateHashes: hashes}, nil
}

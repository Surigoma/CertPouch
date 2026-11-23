// Package storage reads certificate generations from a managed directory.
package storage

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/Surigoma/CertPouch/internal/distribution"
	"github.com/Surigoma/CertPouch/internal/pki"
)

var (
	namePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	versionPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// LoadDeployment reads the generation managed beneath a single destination.
func LoadDeployment(destination string) (string, pki.Bundle, bool, error) {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return "", pki.Bundle{}, false, nil
	}
	if err != nil {
		return "", pki.Bundle{}, false, err
	}
	if !filepath.IsAbs(destination) || !info.IsDir() {
		return "", pki.Bundle{}, false, fmt.Errorf("invalid deployment destination")
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return "", pki.Bundle{}, false, err
	}
	defer root.Close()
	entry, present, err := loadCurrent(root, ".", pki.Policy{}, true)
	return entry.Version, entry.Bundle, present, err
}

// Load snapshots the current generation of each configured certificate.
// A missing current link means the certificate has not yet been published.
func Load(directory string, policies map[string]pki.Policy) ([]distribution.Publication, error) {
	root, err := openPublicationDirectory(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	publications := make([]distribution.Publication, 0, len(policies))
	for name, policy := range policies {
		if !ValidPublicationName(name) {
			return nil, fmt.Errorf("invalid certificate name")
		}
		publication, present, err := loadCurrent(root, name, policy, false)
		if err != nil {
			return nil, fmt.Errorf("load certificate %s: %w", name, err)
		}
		if present {
			publications = append(publications, publication)
		}
	}
	slices.SortFunc(publications, func(a, b distribution.Publication) int { return strings.Compare(a.Name, b.Name) })
	return publications, nil
}

func ValidPublicationName(name string) bool {
	return namePattern.MatchString(name) && filepath.IsLocal(name)
}

// ReadPublicLeaf opens only the public certificate of the current generation.
func ReadPublicLeaf(directory, name string) (string, []byte, error) {
	if !ValidPublicationName(name) {
		return "", nil, fmt.Errorf("invalid certificate name")
	}
	root, err := openPublicationDirectory(directory)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	generation, version, err := openCurrent(root, name)
	if err != nil || generation == nil {
		return version, nil, err
	}
	defer generation.Close()
	body, err := readArtifact(generation, "certificate.pem", false, false, distribution.MaxDownloadBytes)
	return version, body, err
}

func openPublicationDirectory(directory string) (*os.Root, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("certificate directory must be absolute")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect certificate directory: %w", err)
	}
	if !info.IsDir() || !privatePermissions(info) {
		return nil, fmt.Errorf("certificate directory must be a private regular directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open certificate directory: %w", err)
	}
	return root, nil
}

func loadCurrent(root *os.Root, name string, policy pki.Policy, deployment bool) (distribution.Publication, bool, error) {
	generationRoot, version, err := openCurrent(root, name)
	if err != nil || generationRoot == nil {
		return distribution.Publication{}, false, err
	}
	defer generationRoot.Close()
	bundle, err := readBundle(generationRoot, deployment)
	if err != nil {
		return distribution.Publication{}, false, err
	}
	return distribution.Publication{Name: name, Version: version, Current: true, Bundle: bundle, Policy: policy}, true, nil
}

func openCurrent(root *os.Root, name string) (*os.Root, string, error) {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("certificate location must be a directory")
	}
	current := filepath.Join(name, "current")
	target, err := root.Readlink(current)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read current link: %w", err)
	}
	version, ok := strings.CutPrefix(filepath.ToSlash(target), "versions/")
	if !ok || !versionPattern.MatchString(version) {
		return nil, "", fmt.Errorf("current must reference a managed generation")
	}
	versions := filepath.Join(name, "versions")
	generation := filepath.Join(versions, version)
	for _, path := range []string{versions, generation} {
		info, err := root.Lstat(path)
		if err != nil || !info.IsDir() {
			return nil, version, fmt.Errorf("generation path must contain regular directories")
		}
	}
	generationRoot, err := root.OpenRoot(generation)
	if err != nil {
		return nil, version, fmt.Errorf("open generation: %w", err)
	}
	return generationRoot, version, nil
}

func readBundle(root *os.Root, deployment bool) (pki.Bundle, error) {
	var bundle pki.Bundle
	remaining := distribution.MaxDownloadBytes
	for _, artifact := range []struct {
		name   string
		secret bool
		target *[]byte
	}{
		{"certificate.pem", false, &bundle.CertificatePEM},
		{"chain.pem", false, &bundle.ChainPEM},
		{"private-key.pem", true, &bundle.PrivateKeyPEM},
	} {
		body, err := readArtifact(root, artifact.name, artifact.secret, deployment, remaining)
		if err != nil {
			return pki.Bundle{}, fmt.Errorf("read %s: %w", artifact.name, err)
		}
		*artifact.target = body
		remaining -= len(body)
	}
	if deployment {
		cert, chain := bundle.CertificatePEM, bundle.ChainPEM
		fullchain, err := readArtifact(root, "fullchain.pem", false, true, len(cert)+1+len(chain))
		if err != nil {
			return pki.Bundle{}, fmt.Errorf("read fullchain.pem: %w", err)
		}
		if len(fullchain) != len(cert)+1+len(chain) || !bytes.Equal(fullchain[:len(cert)], cert) ||
			fullchain[len(cert)] != '\n' || !bytes.Equal(fullchain[len(cert)+1:], chain) {
			return pki.Bundle{}, fmt.Errorf("fullchain.pem does not match the installed certificate and chain")
		}
	}
	return bundle, nil
}

func readArtifact(root *os.Root, name string, secret, deployment bool, limit int) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	groupReadable := deployment && info.Mode().Perm() == 0640
	if !info.Mode().IsRegular() || (secret && !privatePermissions(info) && !groupReadable) {
		return nil, fmt.Errorf("invalid artifact type or permissions")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("artifact changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("generation exceeds size limit")
	}
	return body, nil
}

func privatePermissions(info os.FileInfo) bool {
	// Windows ACLs are not represented by Go's Unix permission bits.
	return runtime.GOOS == "windows" || info.Mode().Perm()&0077 == 0
}

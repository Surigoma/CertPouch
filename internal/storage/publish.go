package storage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Surigoma/CertPouch/internal/distribution"
	"github.com/Surigoma/CertPouch/internal/pki"
)

// ReadPublicationFiles reads operator-supplied artifacts without following
// artifact symlinks. An empty chain path represents a directly signed leaf.
func ReadPublicationFiles(certificate, chain, key string) (pki.Bundle, error) {
	var bundle pki.Bundle
	limit := distribution.MaxDownloadBytes
	for _, input := range []struct {
		path   string
		secret bool
		target *[]byte
	}{{certificate, false, &bundle.CertificatePEM}, {chain, false, &bundle.ChainPEM}, {key, true, &bundle.PrivateKeyPEM}} {
		if input.target == &bundle.ChainPEM && input.path == "" {
			continue
		}
		if input.path == "" {
			return pki.Bundle{}, fmt.Errorf("certificate and private key paths are required")
		}
		root, err := os.OpenRoot(filepath.Dir(input.path))
		if err != nil {
			return pki.Bundle{}, err
		}
		data, readErr := readArtifact(root, filepath.Base(input.path), input.secret, false, limit)
		if err := errors.Join(readErr, root.Close()); err != nil {
			return pki.Bundle{}, fmt.Errorf("read publication artifact: %w", err)
		}
		*input.target = data
		limit -= len(data)
	}
	return bundle, nil
}

// Publish validates and commits an immutable generation before switching current.
// The server reads the new generation the next time it starts.
func Publish(directory, name string, policy pki.Policy, bundle pki.Bundle, now time.Time) (version string, err error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("certificate publication requires Linux")
	}
	if !filepath.IsAbs(directory) || !namePattern.MatchString(name) || !filepath.IsLocal(name) {
		return "", fmt.Errorf("invalid storage directory or certificate name")
	}
	versionBytes := make([]byte, 16)
	if _, err := rand.Read(versionBytes); err != nil {
		return "", err
	}
	version = hex.EncodeToString(versionBytes)
	if err := distribution.CheckPublication(distribution.Publication{
		Name: name, Version: version, Bundle: bundle, Policy: policy, Current: true,
	}, now); err != nil {
		return "", fmt.Errorf("validate publication: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || !privatePermissions(info) {
		return "", fmt.Errorf("certificate directory must be a private regular directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := ensurePrivateDir(root, name); err != nil {
		return "", err
	}
	cert, err := root.OpenRoot(name)
	if err != nil {
		return "", err
	}
	defer cert.Close()
	const lock = ".publish-lock"
	if err := cert.Mkdir(lock, 0700); err != nil {
		return "", fmt.Errorf("acquire publication lock: %w", err)
	}
	defer func() { err = errors.Join(err, cert.Remove(lock)) }()
	if current, err := cert.Readlink("current"); err == nil {
		id, ok := strings.CutPrefix(filepath.ToSlash(current), "versions/")
		if !ok || !versionPattern.MatchString(id) {
			return "", fmt.Errorf("current does not reference a managed generation")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := ensurePrivateDir(cert, "versions"); err != nil {
		return "", err
	}
	staged := filepath.Join("versions", ".pending-"+version)
	final := filepath.Join("versions", version)
	if err := cert.Mkdir(staged, 0700); err != nil {
		return "", err
	}
	defer func() {
		for _, file := range []string{"certificate.pem", "chain.pem", "private-key.pem"} {
			_ = cert.Remove(filepath.Join(staged, file))
		}
		_ = cert.Remove(staged)
	}()
	for _, artifact := range []struct {
		name string
		body []byte
	}{{"certificate.pem", bundle.CertificatePEM}, {"chain.pem", bundle.ChainPEM}, {"private-key.pem", bundle.PrivateKeyPEM}} {
		if err := writePublication(cert, filepath.Join(staged, artifact.name), artifact.body); err != nil {
			return "", err
		}
	}
	if err := syncRootDir(cert, staged); err != nil {
		return "", err
	}
	if _, err := cert.Lstat(final); !os.IsNotExist(err) {
		return "", fmt.Errorf("cannot allocate unique generation")
	}
	if err := cert.Rename(staged, final); err != nil {
		return "", err
	}
	if err := syncRootDir(cert, "versions"); err != nil {
		return "", err
	}
	link := filepath.Join(lock, "next")
	if err := cert.Symlink(final, link); err != nil {
		return "", err
	}
	if err := cert.Rename(link, "current"); err != nil {
		return "", errors.Join(err, cert.Remove(link))
	}
	if err := syncRootDir(cert, "."); err != nil {
		return version, fmt.Errorf("current switched but directory sync failed: %w", err)
	}
	return version, nil
}

func ensurePrivateDir(root *os.Root, path string) error {
	if err := root.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || !privatePermissions(info) {
		return fmt.Errorf("managed storage path must be a private regular directory")
	}
	return syncRootDir(root, ".")
}

func writePublication(root *os.Root, path string, data []byte) error {
	file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

func syncRootDir(root *os.Root, path string) error {
	dir, err := root.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

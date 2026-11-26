package storage

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
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
	t.Run("reject unsafe publication locations", func(t *testing.T) {
		for _, tc := range []struct {
			name, storePath, want string
			setup                 func(*testing.T, string)
		}{
			{name: "relative store", storePath: "relative", want: "invalid storage directory"},
			{name: "missing store", storePath: filepath.Join(dir, "missing"), want: "no such file"},
			{name: "public store", want: "private regular directory", setup: func(t *testing.T, path string) {
				if err := os.Chmod(path, 0755); err != nil {
					t.Fatal(err)
				}
			}},
			{name: "already locked", want: "acquire publication lock", setup: func(t *testing.T, path string) {
				if err := os.MkdirAll(filepath.Join(path, name, ".publish-lock"), 0700); err != nil {
					t.Fatal(err)
				}
			}},
			{name: "invalid current", want: "current does not reference a managed generation", setup: func(t *testing.T, path string) {
				subdir := filepath.Join(path, name)
				if err := os.Mkdir(subdir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("unmanaged", filepath.Join(subdir, "current")); err != nil {
					t.Fatal(err)
				}
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				path := tc.storePath
				if tc.setup != nil {
					path = t.TempDir()
					if err := os.Chmod(path, 0700); err != nil {
						t.Fatal(err)
					}
					tc.setup(t, path)
				}
				if _, err := Publish(path, name, policy, bundle, now); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("Publish error = %v, want %q", err, tc.want)
				}
				if _, err := os.Lstat(filepath.Join(path, name, "versions")); !os.IsNotExist(err) {
					t.Fatalf("rejected publication created a generation: %v", err)
				}
			})
		}
		if _, err := PublishIfCurrent(dir, name, policy, bundle, now, "invalid"); err == nil || !strings.Contains(err.Error(), "invalid expected generation") {
			t.Fatalf("invalid expected generation error = %v", err)
		}
	})
	t.Run("separate issuer keeps server ownership", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("running as root is required to verify ownership transfer")
		}
		account, err := user.Lookup("nobody")
		if err != nil {
			t.Fatal(err)
		}
		uid, uidErr := strconv.Atoi(account.Uid)
		gid, gidErr := strconv.Atoi(account.Gid)
		if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 {
			t.Fatal("non-root server user required")
		}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, uid, gid); err != nil {
			t.Fatal(err)
		}
		version, err := Publish(dir, name, policy, bundle, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{name, filepath.Join(name, "versions", version), filepath.Join(name, "versions", version, "private-key.pem")} {
			info, err := os.Stat(filepath.Join(dir, path))
			if err != nil {
				t.Fatal(err)
			}
			owner, err := publicationOwnership(info)
			if err != nil || owner != (storeOwnership{uid, gid}) || info.Mode().Perm()&0077 != 0 {
				t.Fatalf("publication ownership or permissions mismatch for %s: %+v, %v", path, owner, err)
			}
		}
	})
}

func TestReadPublicationFilesRejectsUnsafeArtifacts(t *testing.T) {
	dir := t.TempDir()
	leaf, key := filepath.Join(dir, "certificate.pem"), filepath.Join(dir, "private-key.pem")
	for _, path := range []string{leaf, key} {
		if err := os.WriteFile(path, []byte("test artifact"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "key-link")
	if err := os.Symlink(key, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(key, 0644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, leaf, key string }{
		{"missing key argument", leaf, ""},
		{"public private key", leaf, key},
		{"symlink artifact", leaf, link},
		{"missing artifact directory", filepath.Join(dir, "absent", "leaf.pem"), key},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle, err := ReadPublicationFiles(tc.leaf, "", tc.key)
			if err == nil || len(bundle.CertificatePEM) != 0 || len(bundle.PrivateKeyPEM) != 0 {
				t.Fatalf("unsafe artifacts returned partial data, error=%v", err)
			}
		})
	}
}

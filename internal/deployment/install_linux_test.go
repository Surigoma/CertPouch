package deployment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Surigoma/CertPouch/internal/pki"
)

func TestInstallRollback(t *testing.T) {
	dest := t.TempDir()
	options := Options{Destination: dest, UID: -1, GID: -1, CertificateMode: 0644, PrivateKeyMode: 0600}
	bundle := pki.Bundle{CertificatePEM: []byte("leaf"), PrivateKeyPEM: []byte("private")}
	old, next := strings.Repeat("a", 32), strings.Repeat("b", 32)
	s, err := Begin(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Begin(options); err == nil {
		t.Fatal("concurrent deployment acquired the same destination")
	}
	if err := s.Install(t.Context(), old, bundle, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "current", "private-key.pem"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private key permissions: %v, %v", info, err)
	}
	s, err = Begin(options)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	failed := errors.New("reload failed")
	err = s.Install(t.Context(), next, bundle, func(context.Context) error {
		target, err := currentTarget(dest)
		if err != nil {
			return err
		}
		seen = append(seen, filepath.Base(target))
		if target == filepath.Join("versions", next) {
			return failed
		}
		return nil
	})
	if !errors.Is(err, failed) || len(seen) != 2 || seen[0] != next || seen[1] != old {
		t.Fatalf("expected switch and rollback, got %v, %v", seen, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "versions", next)); !os.IsNotExist(err) {
		t.Fatalf("failed generation must be retryable: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInitialFailurePreservesRecoveryMarker(t *testing.T) {
	dest := t.TempDir()
	options := Options{Destination: dest, UID: -1, GID: -1, CertificateMode: 0644, PrivateKeyMode: 0600}
	s, err := Begin(options)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Install(t.Context(), strings.Repeat("a", 32), pki.Bundle{}, func(context.Context) error {
		return errors.New("initial activation failed")
	})
	if err == nil {
		t.Fatal("failed service activation reported success")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if current, err := currentTarget(dest); err != nil || current != "" {
		t.Fatalf("failed initial generation is still current: %s, %v", current, err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".pull-lock", "recovery")); err != nil {
		t.Fatal(err)
	}
	if _, err := Begin(options); err == nil {
		t.Fatal("subsequent pull should require manual recovery")
	}
}

func TestInterruptedDeploymentRecoversOnNextPull(t *testing.T) {
	dest := t.TempDir()
	options := Options{Destination: dest, UID: -1, GID: -1, CertificateMode: 0644, PrivateKeyMode: 0600}
	s, err := Begin(options)
	if err != nil {
		t.Fatal(err)
	}
	old, next := strings.Repeat("a", 32), strings.Repeat("b", 32)
	bundle := pki.Bundle{CertificatePEM: []byte("leaf"), PrivateKeyPEM: []byte("private")}
	apply := func(context.Context) error { return nil }
	if err := s.Install(t.Context(), old, bundle, apply); err != nil {
		t.Fatal(err)
	}
	if err := s.stage(next, bundle); err != nil {
		t.Fatal(err)
	}
	oldTarget, nextTarget := filepath.Join("versions", old), filepath.Join("versions", next)
	if err := writeSynced(filepath.Join(s.lock, "recovery"), []byte(oldTarget+"\n"+nextTarget+"\n"), 0600, -1, -1); err != nil {
		t.Fatal(err)
	}
	if err := switchCurrent(dest, s.lock, nextTarget); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginRecovering(t.Context(), options, apply); err == nil {
		t.Fatal("recovery ran while the deployment guard was held")
	}
	s.preserve = true
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = BeginRecovering(t.Context(), options, func(context.Context) error {
		target, err := currentTarget(dest)
		if err != nil || target != oldTarget {
			return errors.New("previous generation was not restored before reapplying service")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, nextTarget)); !os.IsNotExist(err) {
		t.Fatalf("interrupted generation is still present: %v", err)
	}
	if err := s.Install(t.Context(), next, bundle, apply); err != nil {
		t.Fatalf("could not retry recovered generation: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

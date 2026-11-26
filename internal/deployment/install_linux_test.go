package deployment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Surigoma/CertPouch/internal/pki"
)

func TestConfigurePlacementWithoutService(t *testing.T) {
	dest := t.TempDir()
	if err := os.Chmod(dest, 0700); err != nil {
		t.Fatal(err)
	}
	options, apply, err := Configure(dest, "", "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	session, err := BeginRecovering(t.Context(), options, apply)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	version := strings.Repeat("a", 32)
	bundle := pki.Bundle{CertificatePEM: []byte("leaf"), PrivateKeyPEM: []byte("private")}
	if err := session.Install(t.Context(), version, bundle, apply); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(dest, "current")); err != nil || target != "versions/"+version {
		t.Fatalf("current = %q, %v", target, err)
	}
	if _, _, err := Configure(dest, "", "", "", "", "", "reload"); err == nil {
		t.Fatal("accepted incomplete service configuration")
	}
}

func TestInstallRollback(t *testing.T) {
	mask := syscall.Umask(0077)
	defer syscall.Umask(mask)
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
	versions, err := os.Stat(filepath.Join(dest, "versions"))
	if err != nil || versions.Mode().Perm() != 0750 {
		t.Fatalf("service group cannot traverse versions directory under the timer umask: %v, %v", versions, err)
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

func TestRecoveryRejectsUnsafeInterruptedState(t *testing.T) {
	old := filepath.Join("versions", strings.Repeat("a", 32))
	next := filepath.Join("versions", strings.Repeat("b", 32))
	for _, tc := range []struct {
		name, record, active, want string
		permission                 os.FileMode
	}{
		{"incomplete first install", "\n" + next + "\n", next, "no previous generation", 0600},
		{"invalid path", old + "\n../outside\n", old, "invalid recovery record", 0600},
		{"public transaction record", old + "\n" + next + "\n", old, "invalid recovery record", 0644},
		{"unexpected current generation", old + "\n" + next + "\n", "versions/" + strings.Repeat("c", 32), "does not match recovery record", 0600},
		{"lost previous generation", old + "\n" + next + "\n", next, "previous generation", 0600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := t.TempDir()
			lock := filepath.Join(dest, ".pull-lock")
			if err := os.MkdirAll(filepath.Join(dest, old), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(lock, 0700); err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(lock, "recovery")
			if err := os.WriteFile(record, []byte(tc.record), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(record, tc.permission); err != nil {
				t.Fatal(err)
			}
			current := filepath.Join(dest, "current")
			if err := os.Symlink(tc.active, current); err != nil {
				t.Fatal(err)
			}
			opts := Options{Destination: dest, UID: -1, GID: -1, CertificateMode: 0644, PrivateKeyMode: 0600}
			called := false
			_, err := BeginRecovering(t.Context(), opts, func(context.Context) error { called = true; return nil })
			if err == nil || !strings.Contains(err.Error(), tc.want) || called {
				t.Fatalf("unsafe recovery: err=%v, activation=%v", err, called)
			}
			if body, err := os.ReadFile(record); err != nil || string(body) != tc.record {
				t.Fatalf("recovery record was lost or rewritten: %q, %v", body, err)
			}
			if got, err := os.Readlink(current); err != nil || got != tc.active {
				t.Fatalf("unsafe recovery changed current: %q, %v", got, err)
			}
		})
	}
}

func TestBeginRecoveringClearsEmptyTransaction(t *testing.T) {
	dest := t.TempDir()
	if err := os.Mkdir(filepath.Join(dest, ".pull-lock"), 0700); err != nil {
		t.Fatal(err)
	}
	opts := Options{Destination: dest, UID: -1, GID: -1, CertificateMode: 0644, PrivateKeyMode: 0600}
	s, err := BeginRecovering(t.Context(), opts, func(context.Context) error {
		t.Error("empty transaction activated a service")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".pull-lock")); !os.IsNotExist(err) {
		t.Fatalf("empty transaction still exists: %v", err)
	}
}

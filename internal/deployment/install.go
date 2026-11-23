// Package deployment installs a verified certificate generation and rolls back
// the active link when the consuming service fails to apply it.
package deployment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Surigoma/CertPouch/internal/pki"
)

var versionPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var fingerPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Options struct {
	Destination                     string
	UID, GID                        int
	CertificateMode, PrivateKeyMode os.FileMode
	RecoveryTimeout                 time.Duration
}

type Session struct {
	options  Options
	lock     string
	preserve bool
	guard    *os.File
}

// Begin serializes writers and leaves a recovery marker if the process dies.
// Destination must be provisioned by the operator.
func Begin(options Options) (*Session, error) {
	return begin(context.Background(), options, nil)
}

// BeginRecovering restores a trustworthy interrupted transaction before opening
// a fresh session. The callback reapplies the locally configured service.
func BeginRecovering(ctx context.Context, options Options, apply func(context.Context) error) (*Session, error) {
	if apply == nil {
		return nil, fmt.Errorf("recovery requires a service operation")
	}
	return begin(ctx, options, apply)
}

func begin(ctx context.Context, options Options, apply func(context.Context) error) (*Session, error) {
	if !filepath.IsAbs(options.Destination) || filepath.Dir(options.Destination) == options.Destination {
		return nil, fmt.Errorf("invalid deployment destination")
	}
	if err := checkDirectory(options.Destination); err != nil {
		return nil, err
	}
	lock := filepath.Join(options.Destination, ".pull-lock")
	guard, err := lockDestination(options.Destination)
	if err != nil {
		return nil, err
	}
	s := &Session{options: options, lock: lock, guard: guard}
	if _, err := os.Lstat(lock); err == nil && apply != nil {
		if err := s.recover(ctx, apply); err != nil {
			return nil, errors.Join(fmt.Errorf("recover interrupted deployment: %w", err), guard.Close())
		}
	}
	if err := os.Mkdir(lock, 0700); err != nil {
		return nil, errors.Join(fmt.Errorf("open deployment transaction: %w", err), guard.Close())
	}
	if err := syncDir(options.Destination); err != nil {
		return nil, errors.Join(err, os.Remove(lock), guard.Close())
	}
	return s, nil
}

func (s *Session) Close() (err error) {
	defer func() { err = errors.Join(err, s.guard.Close()) }()
	if s.preserve {
		return nil
	}
	if err := os.Remove(s.lock); err != nil {
		return err
	}
	return syncDir(s.options.Destination)
}

// Install expects a bundle already verified against the client policy.
func (s *Session) Install(ctx context.Context, version string, bundle pki.Bundle, apply func(context.Context) error) error {
	if !versionPattern.MatchString(version) || apply == nil {
		return fmt.Errorf("invalid version or missing service operation")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dest := s.options.Destination
	old, err := currentTarget(dest)
	if err != nil {
		return err
	}
	next := filepath.Join("versions", version)
	if _, err := os.Lstat(filepath.Join(dest, next)); !os.IsNotExist(err) {
		if err != nil {
			return err
		}
		return fmt.Errorf("version %s already exists; investigate before retrying", version)
	}
	if err := writeSynced(filepath.Join(s.lock, "recovery"), []byte(old+"\n"+next+"\n"), 0600, -1, -1); err != nil {
		return errors.Join(err, removeIfPresent(filepath.Join(s.lock, "recovery")))
	}
	if err := syncDir(s.lock); err != nil {
		return errors.Join(err, removeIfPresent(filepath.Join(s.lock, "recovery")))
	}
	s.preserve = true
	if err := s.stage(version, bundle); err != nil {
		return err
	}

	err = switchCurrent(dest, s.lock, next)
	if err == nil {
		err = apply(ctx)
	}
	if err != nil {
		if rollback := switchCurrent(dest, s.lock, old); rollback != nil {
			return errors.Join(err, fmt.Errorf("restore previous link: %w", rollback))
		}
		if old == "" {
			return fmt.Errorf("initial service activation failed; current removed, manual service recovery required: %w", err)
		}
		restore, cancel := s.recoveryContext(ctx)
		defer cancel()
		if rollback := apply(restore); rollback != nil {
			return errors.Join(err, fmt.Errorf("reapply previous generation: %w", rollback))
		}
		if cleanup := removeGeneration(dest, next); cleanup != nil {
			return errors.Join(err, cleanup)
		}
	}
	if cleanup := os.Remove(filepath.Join(s.lock, "recovery")); cleanup != nil {
		return errors.Join(err, cleanup)
	}
	s.preserve = false
	return err
}

func (s *Session) recoveryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := s.options.RecoveryTimeout
	if timeout <= 0 {
		timeout = SystemdTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func (s *Session) stage(version string, bundle pki.Bundle) error {
	opts := s.options
	versions := filepath.Join(opts.Destination, "versions")
	if err := os.Mkdir(versions, 0750); err == nil {
		if err := os.Chown(versions, -1, opts.GID); err != nil {
			return err
		}
		// The systemd pull unit uses UMask=0077. Set the intended
		// traversal permissions explicitly after assigning the group.
		if err := os.Chmod(versions, 0750); err != nil {
			return err
		}
		if err := syncDir(opts.Destination); err != nil {
			return err
		}
	} else if !os.IsExist(err) {
		return err
	}
	if err := checkDirectory(versions); err != nil {
		return err
	}
	final := filepath.Join(versions, version)
	if _, err := os.Lstat(final); !os.IsNotExist(err) {
		if err != nil {
			return err
		}
		return fmt.Errorf("version %s already exists; investigate before retrying", version)
	}
	tmp, err := os.MkdirTemp(versions, ".pending-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	fullChain := append(append([]byte{}, bundle.CertificatePEM...), '\n')
	fullChain = append(fullChain, bundle.ChainPEM...)
	for _, artifact := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"certificate.pem", bundle.CertificatePEM, opts.CertificateMode},
		{"chain.pem", bundle.ChainPEM, opts.CertificateMode},
		{"fullchain.pem", fullChain, opts.CertificateMode},
		{"private-key.pem", bundle.PrivateKeyPEM, opts.PrivateKeyMode},
	} {
		if err := writeSynced(filepath.Join(tmp, artifact.name), artifact.data, artifact.mode, opts.UID, opts.GID); err != nil {
			return fmt.Errorf("write %s: %w", artifact.name, err)
		}
	}
	if err := os.Chown(tmp, -1, opts.GID); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0750); err != nil {
		return err
	}
	if err := syncDir(tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return syncDir(versions)
}

func currentTarget(dest string) (string, error) {
	target, err := os.Readlink(filepath.Join(dest, "current"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !managedTarget(target) {
		return "", fmt.Errorf("current link points outside managed generations")
	}
	return target, nil
}

func managedTarget(target string) bool {
	return versionPattern.MatchString(filepath.Base(target)) && filepath.Join("versions", filepath.Base(target)) == target
}

func switchCurrent(dest, lock, target string) error {
	current := filepath.Join(dest, "current")
	if target == "" {
		if err := removeIfPresent(current); err != nil {
			return err
		}
	} else {
		temp := filepath.Join(lock, "next")
		if err := os.Symlink(target, temp); err != nil {
			return err
		}
		if err := os.Rename(temp, current); err != nil {
			return errors.Join(err, os.Remove(temp))
		}
	}
	return syncDir(dest)
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func checkDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("deployment directory must be a regular directory writable only by its owner")
	}
	return nil
}

func writeSynced(path string, data []byte, mode os.FileMode, uid, gid int) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if uid != -1 || gid != -1 {
		if err = file.Chown(uid, gid); err != nil {
			return err
		}
	}
	if err = file.Chmod(mode); err != nil {
		return err
	}
	return file.Sync()
}

func syncDir(path string) (err error) {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	return dir.Sync()
}

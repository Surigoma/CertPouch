package deployment

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (s *Session) recover(ctx context.Context, apply func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkDirectory(s.lock); err != nil {
		return err
	}
	dest := s.options.Destination
	record := filepath.Join(s.lock, "recovery")
	old, next, err := recoveryTargets(record)
	if os.IsNotExist(err) {
		// An empty directory was left before staging or after committing.
		if err := os.Remove(s.lock); err != nil {
			return fmt.Errorf("inspect orphaned deployment transaction: %w", err)
		}
		return syncDir(dest)
	}
	if err != nil {
		return err
	}
	if old == "" {
		return fmt.Errorf("interrupted first deployment has no previous generation; manual recovery required")
	}
	current, err := currentTarget(dest)
	if err != nil || (current != old && current != next) {
		return fmt.Errorf("active generation does not match recovery record; manual recovery required")
	}
	if err := verifyRecoveryFiles(dest, old); err != nil {
		return err
	}
	temp := filepath.Join(s.lock, "next")
	if info, err := os.Lstat(temp); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("unexpected temporary recovery file")
		}
		if err := os.Remove(temp); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := switchCurrent(dest, s.lock, old); err != nil {
		return err
	}
	restore, cancel := s.recoveryContext(ctx)
	defer cancel()
	if err := apply(restore); err != nil {
		return fmt.Errorf("reapply previous generation: %w", err)
	}
	if err := removeGeneration(dest, next); err != nil {
		return err
	}
	if err := os.Remove(record); err != nil {
		return err
	}
	if err := os.Remove(s.lock); err != nil {
		return err
	}
	return syncDir(dest)
}

func recoveryTargets(path string) (string, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", "", fmt.Errorf("invalid recovery record")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", "", fmt.Errorf("recovery record changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, 256))
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) != 3 || lines[2] != "" || lines[0] == lines[1] ||
		(lines[0] != "" && !managedTarget(lines[0])) || !managedTarget(lines[1]) {
		return "", "", fmt.Errorf("invalid recovery record")
	}
	return lines[0], lines[1], nil
}

var generationFiles = [...]string{"certificate.pem", "chain.pem", "fullchain.pem", "private-key.pem"}

func verifyRecoveryFiles(dest, target string) error {
	if err := checkDirectory(filepath.Join(dest, "versions")); err != nil {
		return err
	}
	dir := filepath.Join(dest, target)
	if err := checkDirectory(dir); err != nil {
		return err
	}
	for _, name := range generationFiles {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 ||
			(name == "private-key.pem" && info.Mode().Perm()&^0640 != 0) {
			return fmt.Errorf("previous generation is incomplete or unsafe: %s", name)
		}
	}
	return nil
}

func removeGeneration(dest, target string) error {
	if !managedTarget(target) {
		return fmt.Errorf("invalid generation path")
	}
	versions := filepath.Join(dest, "versions")
	if err := checkDirectory(versions); err != nil {
		return err
	}
	dir := filepath.Join(dest, target)
	if err := checkDirectory(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	for _, name := range generationFiles {
		if err := removeIfPresent(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	if err := os.Remove(dir); err != nil {
		return err
	}
	return syncDir(versions)
}

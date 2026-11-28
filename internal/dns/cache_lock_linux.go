//go:build linux

package dns

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const cacheCommitLockSuffix = ".commit-lock"

// WithCacheRead keeps a snapshot and its freshness record on the same
// side of a cooperating writer's commit.
func WithCacheRead(path string, read func() error) error {
	lockPath := filepath.Clean(path) + cacheCommitLockSuffix
	lock, err := openCacheCommitLock(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		// Legacy caches have no lock. Check again after reading so the
		// first refresh cannot introduce a lock mid-read unnoticed.
		readErr := read()
		lock, err = openCacheCommitLock(lockPath)
		if errors.Is(err, os.ErrNotExist) {
			return readErr
		}
	}
	if err != nil {
		return fmt.Errorf("open DNS cache commit lock: %w", err)
	}
	defer lock.Close()
	if err := flockCache(lock, syscall.LOCK_SH); err != nil {
		return fmt.Errorf("lock DNS cache for reading: %w", err)
	}
	return read()
}

func openCacheCommitLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		file.Close()
		return nil, fmt.Errorf("DNS cache commit lock must be a regular file protected from other writers")
	}
	return file, nil
}

func prepareCacheCommitLock(path string) (*os.File, error) {
	path += cacheCommitLockSuffix
	file, err := openCacheCommitLock(path)
	if !errors.Is(err, os.ErrNotExist) {
		return file, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".cache-lock-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(temp.Name())
	if err := errors.Join(temp.Chmod(0644), temp.Close()); err != nil {
		return nil, err
	}
	if err := os.Link(temp.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return openCacheCommitLock(path)
}

func flockCache(file *os.File, mode int) error {
	for {
		err := syscall.Flock(int(file.Fd()), mode)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// Keep the guard inode in place so every writer locks the same file.
// The kernel releases the lock when the process exits.
func lockDNSWriter(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		err = fmt.Errorf("DNS writer guard must be a private regular file")
	}
	if err == nil {
		err = flockCache(file, syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

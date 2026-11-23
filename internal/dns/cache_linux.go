package dns

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// RefreshCache snapshots a complete validated response and replaces the old
// cache only when the bytes differ. One local writer runs at a time.
func RefreshCache(ctx context.Context, path string, limit int, fetch func(context.Context) ([]byte, error)) (changed bool, err error) {
	if !filepath.IsAbs(path) || limit <= 0 || fetch == nil {
		return false, fmt.Errorf("cache refresh requires an absolute file path and a bounded fetch")
	}
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	if path == dir {
		return false, fmt.Errorf("cache path cannot be a filesystem root")
	}
	if err := managedDirectory(dir); err != nil {
		return false, fmt.Errorf("check cache directory: %w", err)
	}
	lock := path + ".fetch-lock"
	if err := os.Mkdir(lock, 0700); err != nil {
		return false, fmt.Errorf("acquire DNS cache refresh lock: %w", err)
	}
	defer func() { err = errors.Join(err, os.Remove(lock)) }()
	previous, old, err := readCurrentCache(path, limit)
	if err != nil {
		return false, err
	}
	body, err := fetch(ctx)
	if err != nil {
		return false, err
	}
	if len(body) == 0 || len(body) > limit {
		return false, fmt.Errorf("invalid cache snapshot size")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if previous != nil && bytes.Equal(body, old) {
		return false, nil
	}
	file, err := os.CreateTemp(dir, ".dns-cache-")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	if previous != nil {
		stat := previous.Sys().(*syscall.Stat_t)
		created, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return false, statErr
		}
		if created.Sys().(*syscall.Stat_t).Gid != stat.Gid {
			err = file.Chown(-1, int(stat.Gid))
		}
		if err == nil {
			err = file.Chmod(previous.Mode().Perm())
		}
	}
	if err == nil {
		_, err = file.Write(body)
	}
	if err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return false, err
	}
	current, err := os.Lstat(path)
	if previous == nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("cache appeared during refresh; refusing to replace it")
		}
	} else if err != nil || !os.SameFile(previous, current) ||
		current.Size() != previous.Size() || !current.ModTime().Equal(previous.ModTime()) {
		return false, fmt.Errorf("cache changed during refresh; refusing to replace it")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return false, err
	}
	if err := syncDirectory(dir); err != nil {
		return true, fmt.Errorf("cache replaced but directory sync failed: %w", err)
	}
	return true, nil
}

func readCurrentCache(path string, limit int) (os.FileInfo, []byte, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 ||
		int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return nil, nil, fmt.Errorf("existing cache must be a regular file owned by the refresh user and not writable by other users")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, nil, fmt.Errorf("cache changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > limit {
		return nil, nil, fmt.Errorf("existing cache exceeds size limit")
	}
	return info, data, nil
}

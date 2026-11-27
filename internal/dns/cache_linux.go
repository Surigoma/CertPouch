package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// RefreshCache snapshots a complete validated response and replaces the old
// cache only when the bytes differ. One local writer runs at a time.
func RefreshCache(ctx context.Context, path string, limit int, fetch func(context.Context) ([]byte, error)) (changed bool, err error) {
	return RefreshCacheAfter(ctx, path, limit, fetch, nil)
}

// RefreshCacheAfter validates the snapshot size and cache destination before
// applying external state. A successful apply is followed by a cache commit
// even if cancellation arrives between those two operations.
func RefreshCacheAfter(ctx context.Context, path string, limit int, fetch func(context.Context) ([]byte, error), apply func(context.Context) error) (changed bool, err error) {
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
	recordPath := path + CacheFreshnessSuffix
	recordInfo, _, err := readCurrentCache(recordPath, 4096)
	if err != nil {
		return false, fmt.Errorf("check cache fetch record: %w", err)
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
	record, err := json.Marshal(newCacheFreshness(body, time.Now()))
	if err != nil {
		return false, err
	}
	changed = previous == nil || !bytes.Equal(body, old)
	var stagedCache string
	if changed {
		stagedCache, err = stageCacheFile(path, body, previous)
		if err != nil {
			return false, err
		}
		defer os.Remove(stagedCache)
	}
	stagedRecord, err := stageCacheFile(recordPath, record, previous)
	if err != nil {
		return false, fmt.Errorf("stage cache fetch record: %w", err)
	}
	defer os.Remove(stagedRecord)
	commitLock, err := prepareCacheCommitLock(path)
	if err != nil {
		return false, fmt.Errorf("prepare DNS cache commit lock: %w", err)
	}
	defer commitLock.Close()
	if err := unchangedCacheFile(path, previous); err != nil {
		return false, err
	}
	if err := unchangedCacheFile(recordPath, recordInfo); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if apply != nil {
		if err := apply(ctx); err != nil {
			return false, err
		}
	}
	if err := flockCache(commitLock, syscall.LOCK_EX); err != nil {
		return false, fmt.Errorf("lock DNS cache for commit: %w", err)
	}
	if err := unchangedCacheFile(path, previous); err != nil {
		return false, err
	}
	if err := unchangedCacheFile(recordPath, recordInfo); err != nil {
		return false, err
	}
	if changed {
		if err := os.Rename(stagedCache, path); err != nil {
			return false, err
		}
	}
	if err := os.Rename(stagedRecord, recordPath); err != nil {
		return changed, fmt.Errorf("write cache fetch record: %w", err)
	}
	if err := syncDirectory(dir); err != nil {
		return changed, fmt.Errorf("sync DNS cache directory: %w", err)
	}
	return changed, nil
}

// format supplies mode and group from the cache, including for the sidecar.
func stageCacheFile(path string, body []byte, format os.FileInfo) (name string, err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".dns-cache-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(file.Name())
		}
	}()
	if format != nil {
		stat := format.Sys().(*syscall.Stat_t)
		created, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return "", statErr
		}
		if created.Sys().(*syscall.Stat_t).Gid != stat.Gid {
			err = file.Chown(-1, int(stat.Gid))
		}
		if err == nil {
			err = file.Chmod(format.Mode().Perm())
		}
	}
	if err == nil {
		_, err = file.Write(body)
	}
	if err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return "", err
	}
	return file.Name(), nil
}

func unchangedCacheFile(path string, previous os.FileInfo) error {
	current, err := os.Lstat(path)
	if previous == nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("cache or fetch record appeared during refresh; refusing to replace it")
		}
	} else if err != nil || !os.SameFile(previous, current) ||
		current.Size() != previous.Size() || !current.ModTime().Equal(previous.ModTime()) {
		return fmt.Errorf("cache or fetch record changed during refresh; refusing to replace it")
	}
	return nil
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

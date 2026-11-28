package dns

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRefreshCacheResumesAfterWriterExits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := os.Mkdir(path+".fetch-lock", 0700); err != nil {
		t.Fatal(err)
	}
	guard, err := lockDNSWriter(path + ".fetch-guard")
	if err != nil {
		t.Fatal(err)
	}
	fetch := func(context.Context) ([]byte, error) { return []byte("snapshot"), nil }
	_, refreshErr := RefreshCache(t.Context(), path, 1024, fetch)
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(refreshErr, syscall.EWOULDBLOCK) {
		t.Fatalf("concurrent refresh returned %v", refreshErr)
	}
	if changed, err := RefreshCache(t.Context(), path, 1024, fetch); err != nil || !changed {
		t.Fatalf("refresh after writer exit: changed=%t, %v", changed, err)
	}
}

func TestCacheReadWaitsForBothFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	recordPath := path + CacheFreshnessSuffix
	now := time.Now()
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	record := func(snapshot []byte) []byte {
		t.Helper()
		data, err := json.Marshal(newCacheFreshness(snapshot, now))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	old, updated := []byte("old"), []byte("new")
	write(recordPath, record(old))
	lock, err := prepareCacheCommitLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := flockCache(lock, syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	write(path, updated)

	done := make(chan error, 1)
	go func() {
		done <- WithCacheRead(path, func() error {
			snapshot, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			record, err := os.ReadFile(recordPath)
			if err != nil {
				return err
			}
			_, err = VerifyCacheFreshness(snapshot, record, now, time.Minute)
			return err
		})
	}()
	select {
	case err := <-done:
		t.Fatalf("reader completed between the two writes: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	write(recordPath, record(updated))
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not complete after the commit")
	}
}

func TestRefreshCacheRejectsUnsafeDestinationsBeforeApply(t *testing.T) {
	for _, tc := range []struct {
		name, suffix, want string
		mode               os.FileMode
	}{
		{"cache symlink", "", "", 0},
		{"freshness sidecar symlink", CacheFreshnessSuffix, "check cache fetch record", 0},
		{"world writable cache", "", "", 0666},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "snapshot.json")
			protected := filepath.Join(dir, "original")
			if err := os.WriteFile(protected, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			unsafe := path + tc.suffix
			if tc.mode == 0 {
				if err := os.Symlink(protected, unsafe); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(unsafe, []byte("unsafe"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(unsafe, tc.mode); err != nil {
					t.Fatal(err)
				}
			}
			changed, err := RefreshCacheAfter(t.Context(), path, 1024, func(context.Context) ([]byte, error) {
				t.Error("unsafe destination passed preflight")
				return []byte("{}"), nil
			}, func(context.Context) error {
				t.Error("unsafe destination reached activation")
				return nil
			})
			if changed || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unsafe destination result = %v, %v", changed, err)
			}
			if data, err := os.ReadFile(protected); err != nil || string(data) != "original" {
				t.Fatalf("cache refresh wrote through symlink: %q, %v", data, err)
			}
			if _, err := os.Stat(path + ".fetch-lock"); !os.IsNotExist(err) {
				t.Fatalf("refresh left its lock: %v", err)
			}
		})
	}
}

func TestRefreshCacheRejectsInvalidOrCanceledFetchBeforeApply(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		cancel     bool
	}{
		{"empty", "", false},
		{"too large", "123456789", false},
		{"canceled", "{}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot.json")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			changed, err := RefreshCacheAfter(ctx, path, 8, func(context.Context) ([]byte, error) {
				if tc.cancel {
					cancel()
				}
				return []byte(tc.body), nil
			}, func(context.Context) error {
				t.Error("invalid or canceled fetch reached activation")
				return nil
			})
			if changed || err == nil || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("fetch result = %v, %v", changed, err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("invalid snapshot was published: %v", err)
			}
		})
	}
}

func TestRefreshCacheWaitsForActivation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	fetch := func(context.Context) ([]byte, error) { return []byte("{}"), nil }
	failure := errors.New("activation failed")
	if _, err := RefreshCacheAfter(t.Context(), path, 1024, fetch, func(context.Context) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("expected activation failure: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cache published without activation: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := RefreshCacheAfter(ctx, path, 1024, fetch, func(context.Context) error {
		cancel()
		return nil
	}); err != nil {
		t.Fatalf("confirmed activation must finish cache commit: %v", err)
	}
}

func TestRefreshCachePreservesOldSnapshotOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	const old = `{"old":"snapshot"}`
	if err := os.WriteFile(path, []byte(old), 0640); err != nil {
		t.Fatal(err)
	}
	recordPath := path + CacheFreshnessSuffix
	oldRecord, err := json.Marshal(newCacheFreshness([]byte(old), time.Now().Add(-time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, oldRecord, 0640); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("snapshot did not validate")
	if changed, err := RefreshCache(t.Context(), path, 1024, func(context.Context) ([]byte, error) {
		return nil, injected
	}); changed || !errors.Is(err, injected) {
		t.Fatalf("unexpected refresh failure result: %t %v", changed, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != old {
		t.Fatalf("failed refresh changed the current cache: %v", err)
	}
	if got, err := os.ReadFile(recordPath); err != nil || string(got) != string(oldRecord) {
		t.Fatal("failed refresh changed the success record")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := RefreshCache(t.Context(), path, 1024, func(context.Context) ([]byte, error) {
		return []byte(old), nil
	}); changed || err != nil {
		t.Fatalf("unchanged cache was rewritten: %t %v", changed, err)
	}
	if unchanged, err := os.Stat(path); err != nil || !os.SameFile(info, unchanged) {
		t.Fatal("unchanged snapshot lost its original file")
	}
	freshRecord, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCacheFreshness([]byte(old), freshRecord, time.Now(), time.Minute); err != nil {
		t.Fatalf("identical successful refresh did not record a fresh timestamp: %v", err)
	}
	const next = `{"new":"snapshot"}`
	if changed, err := RefreshCache(t.Context(), path, 1024, func(ctx context.Context) ([]byte, error) {
		if _, err := RefreshCache(ctx, path, 1024, func(context.Context) ([]byte, error) {
			t.Fatal("concurrent refresh started fetching")
			return nil, nil
		}); err == nil {
			t.Fatal("concurrent refresh acquired the writer lock")
		}
		return []byte(next), nil
	}); !changed || err != nil {
		t.Fatalf("new snapshot was not stored: %t %v", changed, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != next {
		t.Fatalf("unexpected replacement cache: %q %v", got, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("refresh changed existing cache permissions: %v %v", info, err)
	}
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCacheFreshness([]byte(next), record, time.Now(), time.Minute); err != nil {
		t.Fatalf("fetch record does not match new snapshot: %v", err)
	}
}

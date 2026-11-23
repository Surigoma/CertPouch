package dns

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

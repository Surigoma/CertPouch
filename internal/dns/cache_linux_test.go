package dns

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRefreshCachePreservesOldSnapshotOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	const old = `{"old":"snapshot"}`
	if err := os.WriteFile(path, []byte(old), 0640); err != nil {
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
}

package deployment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordSyncReplacesPreviousState(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync.json")
	state := SyncState{
		Version: strings.Repeat("a", 32), Fingerprint: strings.Repeat("b", 64),
		SyncedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := RecordSync(path, state); err != nil {
		t.Fatal(err)
	}
	if err := RecordSyncFailure(path, "deployment"); err != nil {
		t.Fatal(err)
	}
	failed, err := ReadSync(path)
	if err != nil || failed.LastError != "deployment" || !failed.SyncedAt.Equal(state.SyncedAt) {
		t.Fatalf("failure should retain the previous sync: %+v, %v", failed, err)
	}
	state.SyncedAt = state.SyncedAt.Add(time.Minute)
	if err := RecordSync(path, state); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSync(path)
	if err != nil || got.LastError != "" || got.Version != state.Version || got.Fingerprint != state.Fingerprint || !got.SyncedAt.Equal(state.SyncedAt) {
		t.Fatalf("unexpected stored state: %+v, %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("unexpected state file permissions: %v, %v", info, err)
	}
}

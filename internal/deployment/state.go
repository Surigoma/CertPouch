package deployment

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type SyncState struct {
	Version     string    `json:"version"`
	Fingerprint string    `json:"fingerprint"`
	SyncedAt    time.Time `json:"synced_at"`
}

func RecordSync(path string, state SyncState) error {
	if !validSyncState(state) {
		return fmt.Errorf("invalid synchronization state")
	}
	dir := filepath.Dir(path)
	if err := checkDirectory(dir); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".sync-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(state)
	if err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

func ReadSync(path string) (SyncState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return SyncState{}, err
	}
	if !info.Mode().IsRegular() {
		return SyncState{}, fmt.Errorf("invalid synchronization state file")
	}
	file, err := os.Open(path)
	if err != nil {
		return SyncState{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return SyncState{}, fmt.Errorf("synchronization state changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return SyncState{}, err
	}
	var state SyncState
	if len(data) > 4096 || json.Unmarshal(data, &state) != nil || !validSyncState(state) {
		return SyncState{}, fmt.Errorf("invalid synchronization state")
	}
	return state, nil
}

func validSyncState(state SyncState) bool {
	return versionPattern.MatchString(state.Version) && fingerPattern.MatchString(state.Fingerprint) && !state.SyncedAt.IsZero()
}

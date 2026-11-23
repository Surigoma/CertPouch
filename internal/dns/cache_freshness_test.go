package dns

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestVerifyCacheFreshness(t *testing.T) {
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	snapshot := []byte("snapshot")
	state, err := json.Marshal(newCacheFreshness(snapshot, now.Add(-time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCacheFreshness(snapshot, state, now, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCacheFreshness(snapshot, state, now, time.Minute); err == nil || !strings.Contains(err.Error(), "maximum age") {
		t.Fatalf("cache did not expire at the configured boundary: %v", err)
	}
	if _, err := VerifyCacheFreshness([]byte("changed snapshot"), state, now, 2*time.Minute); err == nil {
		t.Fatal("accepted a changed cache with an old fetch record")
	}
	if _, err := VerifyCacheFreshness(snapshot, state, now.Add(-2*time.Minute), time.Hour); err == nil {
		t.Fatal("accepted a fetch time in the future")
	}
}

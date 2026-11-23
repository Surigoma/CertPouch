package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
)

func TestInspectStatusWithoutActiveGeneration(t *testing.T) {
	target := config.Target{Name: "web.example.internal", Destination: t.TempDir()}
	if got := inspectStatus(target, time.Now()); got.State != "missing" || got.LastSync != nil {
		t.Fatalf("unexpected initial status: %+v", got)
	}
	lock := filepath.Join(target.Destination, ".pull-lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	if got := inspectStatus(target, time.Now()); got.State != "locked" {
		t.Fatalf("expected locked status, got %+v", got)
	}
	if err := os.WriteFile(filepath.Join(lock, "recovery"), []byte("\nversions/example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := inspectStatus(target, time.Now()); got.State != "recovery_pending" {
		t.Fatalf("expected recovery status, got %+v", got)
	}
}

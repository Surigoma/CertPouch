package client

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/distribution"
)

func TestPlanOnlyIncludesConfiguredTargets(t *testing.T) {
	now := time.Now()
	target := config.Target{Name: "web.example.internal", Destination: filepath.Join(t.TempDir(), "web")}
	remote := distribution.Metadata{
		Name: target.Name, Version: "test-version", NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Minute),
	}
	extra := remote
	extra.Name = "other.example.internal"
	plan, err := Plan([]config.Target{target}, []distribution.Metadata{extra, remote}, now)
	if err != nil || len(plan) != 1 || plan[0] != (PlannedCertificate{Name: target.Name, Version: remote.Version, Action: "install"}) {
		t.Fatalf("unexpected plan: %v, %v", plan, err)
	}
	if _, err := Plan([]config.Target{target}, []distribution.Metadata{extra}, now); err == nil {
		t.Fatal("a configured certificate missing from the inventory was accepted")
	}
	if _, err := Plan([]config.Target{target}, []distribution.Metadata{remote}, remote.NotAfter); err == nil {
		t.Fatal("expired remote certificate was accepted")
	}
}

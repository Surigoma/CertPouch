package server

import (
	"errors"
	"testing"

	"github.com/Surigoma/CertPouch/internal/distribution"
)

func TestRefreshKeepsLastValidatedHandler(t *testing.T) {
	handler, err := NewPublicationHandler(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	original := handler.current.Load()
	loaderFailure := errors.New("snapshot unavailable")
	if err := handler.refresh(func() ([]distribution.Publication, error) {
		return nil, loaderFailure
	}); !errors.Is(err, loaderFailure) {
		t.Fatalf("expected load failure: %v", err)
	}
	if err := handler.refresh(func() ([]distribution.Publication, error) {
		return []distribution.Publication{{Name: "web.example.internal", Current: true}}, nil
	}); err == nil {
		t.Fatal("invalid publication replaced the validated snapshot")
	}
	if handler.current.Load() != original {
		t.Fatal("last validated snapshot was lost")
	}
}

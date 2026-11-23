package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Surigoma/CertPouch/internal/distribution"
)

func TestRefreshKeepsLastValidatedHandler(t *testing.T) {
	handler, err := NewPublicationHandler(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	original := handler.current.Load()
	requireHealth := func(status int, body string) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://example.test"+distribution.HealthPath, nil))
		if response.Code != status || !strings.Contains(response.Body.String(), body) {
			t.Fatalf("health response %d: %s", response.Code, response.Body.String())
		}
	}
	requireHealth(http.StatusOK, "\"ok\"")
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
	requireHealth(http.StatusServiceUnavailable, "\"unavailable\"")
	if err := handler.refresh(func() ([]distribution.Publication, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	requireHealth(http.StatusOK, "\"ok\"")
}

package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/distribution"
)

func TestWatchInventoryPublicationsAppliesAuthorizationOnCatalogFailure(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	client := distribution.Client{
		ID: "node", Enabled: true, TokenHash: sha256.Sum256([]byte(token)),
		AllowedIPs: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")},
	}
	handler, err := NewPublicationHandler(nil, []distribution.Client{client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	check := func(path string, want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "https://example.test"+path, nil)
		req.RemoteAddr = "192.0.2.10:12345"
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-CertPouch-Client-ID", client.ID)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("%s status = %d, want %d: %s", path, res.Code, want, res.Body)
		}
	}
	poll := func(load func() ([]distribution.Publication, []distribution.Client, error)) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		called := false
		handler.WatchInventoryPublications(ctx, time.Millisecond, func() ([]distribution.Publication, []distribution.Client, error) {
			called = true
			defer cancel()
			return load()
		})
		if !called {
			t.Fatal("inventory watcher did not poll")
		}
	}
	const inventoryPath = "/api/v1/certificates"
	check(inventoryPath, http.StatusOK)
	poll(func() ([]distribution.Publication, []distribution.Client, error) {
		return []distribution.Publication{{Name: "broken.example.internal", Current: true}}, nil, nil
	})
	check(distribution.HealthPath, http.StatusServiceUnavailable)
	check(inventoryPath, http.StatusUnauthorized)
	poll(func() ([]distribution.Publication, []distribution.Client, error) {
		return nil, []distribution.Client{client}, nil
	})
	check(distribution.HealthPath, http.StatusOK)
	check(inventoryPath, http.StatusOK)
}

func TestRefreshInventorySuspendsAccessUntilRulesRecover(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	client := distribution.Client{
		ID: "node", Enabled: true, TokenHash: sha256.Sum256([]byte(token)),
		AllowedIPs: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")},
	}
	handler, err := NewPublicationHandler(nil, []distribution.Client{client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireStatus := func(path string, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "https://example.test"+path, nil)
		request.RemoteAddr = "192.0.2.10:12345"
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-CertPouch-Client-ID", client.ID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s status = %d, want %d: %s", path, response.Code, want, response.Body)
		}
	}
	recoverRules := func() {
		t.Helper()
		if err := handler.refreshInventory(func() ([]distribution.Client, error) {
			return []distribution.Client{client}, nil
		}); err != nil {
			t.Fatal(err)
		}
		requireStatus("/api/v1/certificates", http.StatusOK)
		requireStatus(distribution.HealthPath, http.StatusOK)
	}
	requireStatus("/api/v1/certificates", http.StatusOK)
	loadErr := errors.New("cache unavailable")
	if err := handler.refreshInventory(func() ([]distribution.Client, error) {
		return []distribution.Client{client}, loadErr
	}); !errors.Is(err, loadErr) {
		t.Fatalf("refreshInventory error = %v, want %v", err, loadErr)
	}
	requireStatus("/api/v1/certificates", http.StatusUnauthorized)
	requireStatus(distribution.HealthPath, http.StatusServiceUnavailable)
	recoverRules()

	if err := handler.refreshInventory(func() ([]distribution.Client, error) {
		return []distribution.Client{client, client}, nil
	}); err == nil {
		t.Fatal("accepted duplicate client rules")
	}
	requireStatus("/api/v1/certificates", http.StatusUnauthorized)
	requireStatus(distribution.HealthPath, http.StatusServiceUnavailable)
	recoverRules()
}

func TestReplaceSnapshotUpdatesAuthorizationWithCatalog(t *testing.T) {
	handler, err := NewPublicationHandler(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	initial := handler.current.Load()
	clients := []distribution.Client{{ID: "host", AllowedIPs: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")}}}
	if err := handler.replaceSnapshot(nil, clients); err != nil {
		t.Fatal(err)
	}
	current := handler.current.Load()
	if current == initial {
		t.Fatal("authorization-only change did not replace the snapshot")
	}
	if err := handler.replaceSnapshot([]distribution.Publication{{Name: "bad.example.internal", Current: true}}, nil); err == nil {
		t.Fatal("invalid catalog was accepted")
	}
	if handler.current.Load() != current {
		t.Fatal("failed validation replaced the current snapshot")
	}
}

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

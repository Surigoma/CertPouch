package distribution

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestInventoryDeadlineRevokesCachedAuthorizationWithoutWaitingForRefresh(t *testing.T) {
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Second)
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	handler, err := newHandler(nil, []Client{{
		ID: "test", Enabled: true, TokenHash: sha256.Sum256([]byte(token)), InventoryExpiresAt: deadline,
		CertificateIPs: map[string][]netip.Prefix{"web.example.internal": {netip.MustParsePrefix("192.0.2.10/32")}},
	}}, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	request := func(path string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.TLS = &tls.ConnectionState{}
		r.RemoteAddr = "192.0.2.10:1234"
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-CertPouch-Client-ID", "test")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if code := request(inventoryPath); code != http.StatusOK {
		t.Fatalf("fresh authorization returned %d", code)
	}
	if code := request(HealthPath); code != http.StatusOK {
		t.Fatalf("fresh inventory health returned %d", code)
	}
	now = deadline
	if code := request(inventoryPath); code != http.StatusUnauthorized {
		t.Fatalf("expired authorization returned %d", code)
	}
	if code := request(HealthPath); code != http.StatusServiceUnavailable {
		t.Fatalf("expired inventory health returned %d", code)
	}
}

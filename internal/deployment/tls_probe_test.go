package deployment

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTLSProbeVerifiesPeerNameTrustAndGeneration(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a TLS probe must not make an HTTP request")
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "https://")
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	digest := sha256.Sum256(server.Certificate().Raw)
	expected := hex.EncodeToString(digest[:])
	for _, tc := range []struct {
		name, serverName, fingerprint string
		trust                         *x509.CertPool
		want                          string
	}{
		{"correct generation", "127.0.0.1", expected, roots, ""},
		{"stale generation", "127.0.0.1", strings.Repeat("0", 64), roots, "different leaf"},
		{"wrong hostname", "wrong.example.internal", expected, roots, "not wrong.example.internal"},
		{"unknown authority", "127.0.0.1", expected, x509.NewCertPool(), "unknown authority"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe, err := NewTLSProbe(address, tc.serverName, tc.trust)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if err := probe.Wait(t.Context(), tc.fingerprint); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err := probe.verify(t.Context(), tc.fingerprint); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q failure, got %v", tc.want, err)
			}
		})
	}
}

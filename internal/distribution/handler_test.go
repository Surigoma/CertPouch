package distribution

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/pki"
)

func testPublication(t *testing.T, now time.Time) Publication {
	t.Helper()
	newKey := func() *rsa.PrivateKey {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	issue := func(template, parent *x509.Certificate, key, signer *rsa.PrivateKey) *x509.Certificate {
		der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, signer)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	rootKey, leafKey := newKey(), newKey()
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	root := issue(rootTemplate, rootTemplate, rootKey, rootKey)
	const name = "web.example.internal"
	leaf := issue(&x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Minute),
	}, root, leafKey, rootKey)
	roots := x509.NewCertPool()
	roots.AddCert(root)
	return Publication{
		Name: name, Version: strings.Repeat("a", 32), Current: true,
		Policy: pki.Policy{Roots: roots, Issuer: root, DNS: []string{name}},
		Bundle: pki.Bundle{
			CertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}),
			PrivateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}),
		},
	}
}

func TestCatalogOmitsExpiredPublications(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	valid := testPublication(t, now)
	expired := testPublication(t, now.Add(-2*time.Minute))
	expired.Name = "expired.example.internal"
	publications := []Publication{expired, valid}
	h, err := newHandler(publications, nil, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if !h.catalog.current[valid.Name].available(now) || h.catalog.current[expired.Name] != nil ||
		h.catalog.versions[expired.Name][expired.Version] != nil {
		t.Fatal("catalog did not isolate the expired generation")
	}
	if err := CheckPublication(expired, now); err == nil {
		t.Fatal("accepted a new expired publication")
	}
	publications[0].Bundle.PrivateKeyPEM = valid.Bundle.PrivateKeyPEM
	if _, err := newHandler(publications, nil, nil, func() time.Time { return now }); err == nil {
		t.Fatal("treated an expired publication with a mismatched key as valid")
	}
}

func TestTokenlessDistributionAuthentication(t *testing.T) {
	now := time.Now()
	publication := testPublication(t, now)
	peer := &x509.Certificate{Raw: []byte("verified test peer")}
	source := netip.MustParsePrefix("192.0.2.10/32")
	ipClient := Client{ID: "ip-client", Enabled: true, Auth: AuthIP,
		CertificateIPs: map[string][]netip.Prefix{publication.Name: {source}}}
	mtlsClient := Client{ID: "mtls-client", Enabled: true, Auth: AuthMTLS,
		CertificateIPs: ipClient.CertificateIPs, ClientCertificateHashes: [][sha256.Size]byte{sha256.Sum256(peer.Raw)}}
	h, err := newHandler([]Publication{publication}, []Client{ipClient, mtlsClient}, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/certificates/" + publication.Name + "/download?version=" + publication.Version
	for _, tc := range []struct {
		name, id, remote string
		peer             *x509.Certificate
		verified         bool
		bearer           bool
		want             int
	}{
		{"exact IP", ipClient.ID, "192.0.2.10:1234", nil, false, false, http.StatusOK},
		{"wrong IP with forwarded correct IP", ipClient.ID, "192.0.2.11:1234", nil, false, false, http.StatusUnauthorized},
		{"unexpected token", ipClient.ID, "192.0.2.10:1234", nil, false, true, http.StatusUnauthorized},
		{"verified mTLS", mtlsClient.ID, "192.0.2.10:1234", peer, true, false, http.StatusOK},
		{"mTLS without verified chain", mtlsClient.ID, "192.0.2.10:1234", peer, false, false, http.StatusUnauthorized},
		{"mTLS with different fingerprint", mtlsClient.ID, "192.0.2.10:1234", &x509.Certificate{Raw: []byte("different")}, true, false, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.RemoteAddr = tc.remote
			r.TLS = &tls.ConnectionState{}
			if tc.peer != nil {
				r.TLS.PeerCertificates = []*x509.Certificate{tc.peer}
			}
			if tc.verified {
				r.TLS.VerifiedChains = [][]*x509.Certificate{{tc.peer}}
			}
			r.Header.Set("X-CertPouch-Client-ID", tc.id)
			r.Header.Set("X-Forwarded-For", "192.0.2.10")
			if tc.bearer {
				r.Header.Set("Authorization", "Bearer "+strings.Repeat("A", 43))
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
	ipClient.CertificateIPs = map[string][]netip.Prefix{publication.Name: {netip.MustParsePrefix("0.0.0.0/0")}}
	if _, err := h.WithClients([]Client{ipClient}); err == nil {
		t.Fatal("accepted a wildcard source for IP authentication")
	}
}

func TestImplicitInventoryIPClient(t *testing.T) {
	now := time.Now()
	publication := testPublication(t, now)
	rules := map[string][]netip.Prefix{publication.Name: {netip.MustParsePrefix("192.0.2.10/32")}}
	implicit := Client{Enabled: true, Auth: AuthIP, Implicit: true, CertificateIPs: rules}
	disabled := Client{ID: "disabled", Enabled: false, Auth: AuthIP, CertificateIPs: rules}
	h, err := newHandler([]Publication{publication}, []Client{implicit, disabled}, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/certificates/" + publication.Name + "/download?version=" + publication.Version
	for _, tc := range []struct {
		name, remote string
		ids          []string
		bearer       bool
		want         int
	}{
		{"no ID from inventory IP", "192.0.2.10:1234", nil, false, http.StatusOK},
		{"unregistered ID from inventory IP", "192.0.2.10:1234", []string{"web-server-01"}, false, http.StatusOK},
		{"no ID from other IP", "192.0.2.11:1234", nil, false, http.StatusUnauthorized},
		{"registered disabled ID stays rejected", "192.0.2.10:1234", []string{disabled.ID}, false, http.StatusUnauthorized},
		{"token is not accepted", "192.0.2.10:1234", nil, true, http.StatusUnauthorized},
		{"duplicate ID headers", "192.0.2.10:1234", []string{"a", "b"}, false, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.RemoteAddr = tc.remote
			r.TLS = &tls.ConnectionState{}
			for _, id := range tc.ids {
				r.Header.Add("X-CertPouch-Client-ID", id)
			}
			if tc.bearer {
				r.Header.Set("Authorization", "Bearer "+strings.Repeat("A", 43))
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
	withoutImplicit, err := h.WithClients([]Client{disabled})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr, r.TLS = "192.0.2.10:1234", &tls.ConnectionState{}
	w := httptest.NewRecorder()
	withoutImplicit.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("request without ID and without implicit client returned %d", w.Code)
	}
	for name, invalid := range map[string][]Client{
		"duplicate":    {implicit, implicit},
		"token":        {{Enabled: true, Auth: AuthToken, Implicit: true, CertificateIPs: rules}},
		"with ID":      {{ID: "named", Enabled: true, Auth: AuthIP, Implicit: true, CertificateIPs: rules}},
		"static rules": {{Enabled: true, Auth: AuthIP, Implicit: true, AllowedIPs: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")}, AllowedCertificates: []string{publication.Name}}},
	} {
		if _, err := h.WithClients(invalid); err == nil {
			t.Errorf("accepted invalid implicit client: %s", name)
		}
	}
}

func TestDistributionAPI(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	current := testPublication(t, now)
	old := current
	old.Version, old.Current = strings.Repeat("b", 32), false
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	client := Client{
		ID: "allowed", Enabled: true, TokenHash: sha256.Sum256([]byte(token)),
		AllowedIPs:          []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
		AllowedCertificates: []string{current.Name},
	}
	unassigned := client
	unassigned.ID, unassigned.AllowedCertificates = "unassigned", nil
	dnsClient := client
	dnsClient.ID, dnsClient.AllowedIPs, dnsClient.AllowedCertificates = "dns", nil, nil
	dnsClient.CertificateIPs = map[string][]netip.Prefix{
		current.Name:             {netip.MustParsePrefix("192.0.2.10/32"), netip.MustParsePrefix("192.0.2.12/32")},
		"other.example.internal": {netip.MustParsePrefix("192.0.2.11/32")},
	}
	disabled := client
	disabled.ID, disabled.Enabled = "disabled", false
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h, err := newHandler([]Publication{current, old}, []Client{client, unassigned, disabled, dnsClient}, logger, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/certificates"
	download := base + "/" + current.Name + "/download?version=" + old.Version
	request := func(path, id, credential, remote string, https bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = remote
		if https {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-CertPouch-Client-ID", id)
		r.Header.Set("X-Forwarded-For", "192.0.2.1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, tt := range []struct {
		name, path, id, token, remote string
		https                         bool
		status                        int
	}{
		{"http rejected", download, client.ID, token, "192.0.2.10:1234", false, 400},
		{"wrong token", download, client.ID, strings.Repeat("A", 44), "192.0.2.10:1234", true, 401},
		{"disabled", download, disabled.ID, token, "192.0.2.10:1234", true, 401},
		{"forwarded IP ignored", download, client.ID, token, "198.51.100.10:1234", true, 401},
		{"unassigned", download, unassigned.ID, token, "192.0.2.10:1234", true, 404},
		{"DNS source for a different certificate denied", download, dnsClient.ID, token, "192.0.2.11:1234", true, 404},
		{"DNS source required for metadata", base + "/" + current.Name + "/metadata", dnsClient.ID, token, "192.0.2.11:1234", true, 404},
		{"unknown version", base + "/" + current.Name + "/download?version=" + strings.Repeat("c", 32), client.ID, token, "192.0.2.10:1234", true, 409},
		{"duplicate version", download + "&version=" + old.Version, client.ID, token, "192.0.2.10:1234", true, 400},
		{"encoded separator", base + "/" + current.Name + "%2fmetadata", client.ID, token, "192.0.2.10:1234", true, 400},
		{"no trailing slash redirect", base + "/", client.ID, token, "192.0.2.10:1234", true, 400},
		{"metadata requires authentication", base + "/" + current.Name + "/metadata", "", "", "192.0.2.10:1234", true, 401},
		{"health without credentials", "/api/v1/health", "", "", "192.0.2.10:1234", true, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := request(tt.path, tt.id, tt.token, tt.remote, tt.https)
			if w.Code != tt.status || strings.Contains(w.Body.String(), "PRIVATE KEY") {
				t.Fatalf("status = %d, want %d; secret present = %t", w.Code, tt.status, strings.Contains(w.Body.String(), "PRIVATE KEY"))
			}
		})
	}
	for _, source := range []string{"192.0.2.10:1234", "192.0.2.12:1234"} {
		w := request(download, dnsClient.ID, token, source, true)
		if w.Code != http.StatusOK {
			t.Fatalf("DNS source %s denied: %d", source, w.Code)
		}
	}
	wrongSource := request(base, dnsClient.ID, token, "192.0.2.11:1234", true)
	if wrongSource.Code != 200 || strings.TrimSpace(wrongSource.Body.String()) != `{"certificates":[]}` {
		t.Fatal("DNS client saw metadata for a certificate assigned to a different IP")
	}
	post := httptest.NewRequest(http.MethodPost, "https://example.test/api/v1/health", nil)
	rejected := httptest.NewRecorder()
	h.ServeHTTP(rejected, post)
	if rejected.Code != http.StatusMethodNotAllowed || rejected.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("unexpected unsupported method response: %d", rejected.Code)
	}
	w := request(download, client.ID, token, "[::ffff:192.0.2.10]:1234", true)
	var got Download
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("download failed: status %d", w.Code)
	}
	key := got.Artifacts["private-key.pem"]
	sum := sha256.Sum256([]byte(key))
	if got.Metadata.Version != old.Version || key != string(old.Bundle.PrivateKeyPEM) ||
		got.Metadata.ArtifactSHA256["private-key.pem"] != hex.EncodeToString(sum[:]) {
		t.Fatal("download did not return the requested generation with matching digest")
	}
	w = request(base, unassigned.ID, token, "192.0.2.10:1234", true)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"certificates":[]}` {
		t.Fatal("unassigned client received inventory")
	}
	if w := request(base+"/"+current.Name+"/metadata", client.ID, token, "192.0.2.10:1234", true); w.Code != 200 {
		t.Fatalf("metadata request returned %d", w.Code)
	}
	if w := request(base+"/"+token+"/metadata", client.ID, token, "192.0.2.10:1234", true); w.Code != 404 {
		t.Fatalf("unknown certificate returned %d", w.Code)
	}
	if strings.Contains(logs.String(), token) || strings.Contains(logs.String(), "PRIVATE KEY") {
		t.Fatal("distribution logs exposed secret material")
	}
	type auditEvent struct{ Operation, Client, Certificate, Result, Category string }
	events := make(map[auditEvent]bool)
	for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]string
		if err := json.Unmarshal([]byte(line), &entry); err != nil || entry["time"] == "" {
			t.Fatalf("invalid audit event: %v", err)
		}
		events[auditEvent{entry["operation"], entry["client_id"], entry["certificate_name"], entry["result"], entry["error_category"]}] = true
	}
	for _, want := range []auditEvent{
		{"authenticate", "", "", "denied", "authentication"},
		{"inventory", unassigned.ID, "", "ok", ""},
		{"metadata", client.ID, current.Name, "ok", ""},
		{"metadata", client.ID, "", "denied", "certificate_unavailable"},
		{"download", client.ID, current.Name, "ok", ""},
		{"download", unassigned.ID, current.Name, "denied", "certificate_unavailable"},
		{"download", client.ID, current.Name, "denied", "version_unavailable"},
	} {
		if !events[want] {
			t.Errorf("missing audit event %+v", want)
		}
	}
	loopback := client
	loopback.AllowedIPs = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	tlsHandler, err := newHandler([]Publication{current}, []Client{loopback}, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	tlsServer := httptest.NewTLSServer(tlsHandler)
	req, err := http.NewRequest(http.MethodGet, tlsServer.URL+base, nil)
	if err != nil {
		tlsServer.Close()
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-CertPouch-Client-ID", client.ID)
	resp, err := tlsServer.Client().Do(req)
	if err != nil {
		tlsServer.Close()
		t.Fatal(err)
	}
	var inventory struct {
		Certificates []Metadata `json:"certificates"`
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&inventory)
	resp.Body.Close()
	tlsServer.Close()
	if resp.StatusCode != http.StatusOK || decodeErr != nil ||
		len(inventory.Certificates) != 1 || inventory.Certificates[0].Version != current.Version {
		t.Fatal("HTTPS inventory did not return the authorized current generation")
	}
	now = now.Add(time.Minute)
	if w := request(download, client.ID, token, "192.0.2.10:1234", true); w.Code != 409 {
		t.Fatalf("expired generation returned status %d", w.Code)
	}
}

package sheets

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	sheetsapi "google.golang.org/api/sheets/v4"
)

func TestFetchReadOnlySnapshot(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "test@example.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":   tokenEndpoint, "private_key_id": "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := ParseCredentials(raw)
	if err != nil {
		t.Fatal(err)
	}
	token := "test-oauth-token"
	const first = "'Office West'!A2:G512"
	const second = "Other!A2:G100"
	valid := `{"spreadsheetId":"example","valueRanges":[{"range":"'Office West'!A2:G512","majorDimension":"ROWS","values":[["","192.0.2","10","app"]]},{"range":"Other!A2:G100","majorDimension":"ROWS","values":[["","198.51.100","11","other"]]}]}`
	replies := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if r.Method != http.MethodPost || r.ParseForm() != nil ||
				r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				t.Error("unexpected OAuth request")
				w.WriteHeader(400)
				return
			}
			assertion := strings.Split(r.Form.Get("assertion"), ".")
			if len(assertion) != 3 {
				t.Error("assertion is malformed")
				w.WriteHeader(400)
				return
			}
			signature, err := base64.RawURLEncoding.DecodeString(assertion[2])
			hash := sha256.Sum256([]byte(assertion[0] + "." + assertion[1]))
			if err != nil || rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], signature) != nil {
				t.Error("assertion signature failed verification")
			}
			claims, err := base64.RawURLEncoding.DecodeString(assertion[1])
			var decoded map[string]any
			if err != nil || json.Unmarshal(claims, &decoded) != nil || decoded["scope"] != sheetsapi.SpreadsheetsReadonlyScope ||
				decoded["aud"] != serverTokenURL(r) {
				t.Error("unexpected OAuth assertion claims")
			}
			if expiry, ok := decoded["exp"].(float64); !ok || expiry <= float64(time.Now().Unix()) ||
				expiry > float64(time.Now().Add(time.Hour+time.Minute).Unix()) {
				t.Error("unexpected OAuth assertion expiry")
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": token, "token_type": "Bearer"})
		case "/v4/spreadsheets/example/values:batchGet":
			query := r.URL.Query()
			ranges := query["ranges"]
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+token ||
				query.Get("majorDimension") != "ROWS" || query.Get("valueRenderOption") != "FORMATTED_VALUE" ||
				len(ranges) != 2 || ranges[0] != first || ranges[1] != second {
				t.Error("invalid Sheets API request")
			}
			switch body := <-replies; body {
			case "redirect":
				http.Redirect(w, r, "/redirect", http.StatusTemporaryRedirect)
			default:
				w.Write([]byte(body))
			}
		default:
			t.Error("unexpected request or redirect")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := newClient(server.Client().Transport, server.URL+"/token", server.URL)
	replies <- valid
	output, err := client.Fetch(t.Context(), credentials, "example", []string{first, second})
	var before, after any
	if err != nil || json.Unmarshal([]byte(valid), &before) != nil || json.Unmarshal(output, &after) != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("fetch did not return the validated snapshot: %v", err)
	}
	replies <- strings.Replace(valid, `,"values":[["","198.51.100","11","other"]]`, "", 1)
	if _, err := client.Fetch(t.Context(), credentials, "example", []string{first, second}); err != nil {
		t.Fatalf("fetch rejected a returned range with no rows: %v", err)
	}
	replies <- strings.Replace(valid, `,"majorDimension":"ROWS","values":[["","198.51.100","11","other"]]`, "", 1)
	if _, err := client.Fetch(t.Context(), credentials, "example", []string{first, second}); err != nil {
		t.Fatalf("fetch rejected a returned empty range with no dimension: %v", err)
	}
	for _, test := range []struct{ name, body string }{
		{"wrong spreadsheet", strings.Replace(valid, `"spreadsheetId":"example"`, `"spreadsheetId":"other"`, 1)},
		{"incomplete", `{"spreadsheetId":"example","valueRanges":[]}`},
		{"null range", strings.Replace(valid, `{"range":"Other!A2:G100","majorDimension":"ROWS","values":[["","198.51.100","11","other"]]}`, "null", 1)},
		{"wrong range", strings.Replace(valid, "Other!A2:G100", "Other!B2:G100", 1)},
		{"non-text value", strings.Replace(valid, `"198.51.100","11"`, `"198.51.100",11`, 1)},
		{"oversized", strings.Repeat(" ", MaxSnapshotBytes+1)},
		{"redirect", "redirect"},
	} {
		t.Run(test.name, func(t *testing.T) {
			replies <- test.body
			if _, err := client.Fetch(t.Context(), credentials, "example", []string{first, second}); err == nil ||
				strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "Office West") {
				t.Fatalf("invalid response was accepted or sensitive data exposed: %v", err)
			}
		})
	}
}

func serverTokenURL(r *http.Request) string { return "https://" + r.Host + "/token" }

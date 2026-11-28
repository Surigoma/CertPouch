package update

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testResponse(status int, content string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(content)), ContentLength: int64(len(content))}
}

func TestLatestRequiresNewerStableRelease(t *testing.T) {
	for _, tc := range []struct {
		current, response string
		newer, rejected   bool
	}{
		{"v0.2.1", `{"tag_name":"v0.10.0"}`, true, false},
		{"v0.10.0", `{"tag_name":"v0.2.1"}`, false, false},
		{"v0.2.1", `{"tag_name":"v0.2.1"}`, false, false},
		{"v0.2.1", `{"tag_name":"v1.0.0","draft":true}`, false, true},
		{"v0.2.1", `{"tag_name":"v1.0.0","prerelease":true}`, false, true},
		{"v0.2.1", `{"tag_name":"v1.0.0-rc.1"}`, false, true},
		{"dev", "", false, true},
	} {
		calls := 0
		g := github{client: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != latestURL || r.Header.Get("Authorization") != "" {
				t.Fatalf("unexpected request to %s", r.URL)
			}
			return testResponse(http.StatusOK, tc.response), nil
		})}}
		if tag, got, err := g.latest(t.Context(), tc.current); got != tc.newer || (err != nil) != tc.rejected {
			t.Errorf("current %s, response %q: tag=%s, newer=%t, err=%v", tc.current, tc.response, tag, got, err)
		}
		if tc.current == "dev" && calls != 0 {
			t.Error("untagged executable contacted GitHub")
		}
	}
}

func TestArchiveChecksumRejectsMissingInvalidAndDuplicateEntries(t *testing.T) {
	const name = "certpouch-linux-amd64.tar.gz"
	hash := strings.Repeat("a", 64)
	valid := fmt.Sprintf("%s  %s\n", hash, name)
	if got, err := archiveChecksum(strings.NewReader(valid), name); got != hash || err != nil {
		t.Fatalf("valid checksum = %s, %v", got, err)
	}
	for _, text := range []string{"", valid + valid, fmt.Sprintf("bad  %s\n", name)} {
		if _, err := archiveChecksum(strings.NewReader(text), name); err == nil {
			t.Errorf("accepted invalid checksum file %q", text)
		}
	}
}

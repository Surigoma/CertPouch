package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/version"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	repository       = "Surigoma/CertPouch"
	latestURL        = "https://api.github.com/repos/" + repository + "/releases/latest"
	downloadURL      = "https://github.com/" + repository + "/releases/download/"
	maxMetadataBytes = 1 << 20
	maxArchiveBytes  = 128 << 20
)

var stableTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type github struct {
	client *http.Client
}

func githubClient() github {
	return github{client: &http.Client{
		Timeout: 90 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.User != nil ||
				(req.URL.Port() != "" && req.URL.Port() != "443") {
				return fmt.Errorf("invalid GitHub release redirect")
			}
			switch req.URL.Hostname() {
			case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
				return nil
			default:
				return fmt.Errorf("GitHub release redirected outside the allowed hosts")
			}
		},
	}}
}

// Latest returns the latest stable tag and whether it is newer than current.
func Latest(ctx context.Context, current string) (string, bool, error) {
	return githubClient().latest(ctx, current)
}

func (g github) latest(ctx context.Context, current string) (string, bool, error) {
	if !stableTag.MatchString(current) {
		return "", false, fmt.Errorf("automatic updates require a binary from a tagged stable release")
	}
	body, err := g.get(ctx, latestURL, "application/vnd.github+json", maxMetadataBytes)
	if err != nil {
		return "", false, fmt.Errorf("check latest GitHub release: %w", err)
	}
	defer body.Close()
	data, err := readBounded(body, maxMetadataBytes)
	if err != nil {
		return "", false, err
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if json.Unmarshal(data, &release) != nil || release.Draft || release.Prerelease ||
		!stableTag.MatchString(release.Tag) {
		return "", false, fmt.Errorf("GitHub did not return a stable release")
	}
	return release.Tag, version.Compare("go"+release.Tag[1:], "go"+current[1:]) > 0, nil
}

func (g github) get(ctx context.Context, url, accept string, limit int64) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "CertPouch")
	resp, err := g.client.Do(req)
	if err != nil {
		// Download redirects can carry signed query parameters.
		return nil, fmt.Errorf("GitHub HTTPS request failed")
	}
	if resp.StatusCode != http.StatusOK || resp.ContentLength > limit {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub returned status %d or an oversized response", resp.StatusCode)
	}
	return resp.Body, nil
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(body)) > limit {
		err = fmt.Errorf("GitHub release response exceeds the size limit")
	}
	return body, err
}

func (g github) archive(ctx context.Context, tag, name string, file *os.File) error {
	base := downloadURL + tag + "/"
	sums, err := g.get(ctx, base+"SHA256SUMS", "application/octet-stream", maxMetadataBytes)
	if err != nil {
		return fmt.Errorf("download SHA256SUMS: %w", err)
	}
	data, err := readBounded(sums, maxMetadataBytes)
	sums.Close()
	if err != nil {
		return err
	}
	want, err := archiveChecksum(strings.NewReader(string(data)), name)
	if err != nil {
		return err
	}
	archive, err := g.get(ctx, base+name, "application/octet-stream", maxArchiveBytes)
	if err != nil {
		return fmt.Errorf("download release archive: %w", err)
	}
	defer archive.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(archive, maxArchiveBytes+1))
	if err != nil {
		return err
	}
	if n > maxArchiveBytes || hex.EncodeToString(hash.Sum(nil)) != want {
		return fmt.Errorf("release archive size or SHA-256 is invalid")
	}
	_, err = file.Seek(0, io.SeekStart)
	return err
}

func archiveChecksum(r io.Reader, name string) (string, error) {
	var checksum string
	lines := bufio.NewScanner(r)
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		decoded, err := hex.DecodeString(fields[0])
		if checksum != "" || err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("release checksum for %s is ambiguous or invalid", name)
		}
		checksum = strings.ToLower(fields[0])
	}
	if err := lines.Err(); err != nil {
		return "", err
	}
	if checksum == "" {
		return "", fmt.Errorf("release checksum for %s is missing", name)
	}
	return checksum, nil
}

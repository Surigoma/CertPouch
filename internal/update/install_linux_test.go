package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestApplyVerifiedRelease(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("no release archive for this architecture")
	}
	const oldVersion, newVersion = "v0.2.1", "v0.10.0"
	build := t.TempDir()
	module := "module github.com/" + repository + "/cmd/certpouch\n"
	if err := os.WriteFile(filepath.Join(build, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf("package main\nimport \"fmt\"\nfunc main() { fmt.Println(%q) }\n", newVersion)
	if err := os.WriteFile(filepath.Join(build, "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "go", "build", "-o", "certpouch", ".")
	command.Dir = build
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build release fixture: %v\n%s", err, out)
	}
	body, err := os.ReadFile(filepath.Join(build, "certpouch"))
	if err != nil {
		t.Fatal(err)
	}
	name := "certpouch-linux-" + runtime.GOARCH
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name + "/certpouch", Mode: 0755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		label     string
		latest    string
		badDigest bool
		locked    bool
		update    bool
	}{
		{label: "update", latest: newVersion, update: true},
		{label: "checksum failure", latest: newVersion, badDigest: true},
		{label: "concurrent update", latest: newVersion, locked: true},
		{label: "no downgrade", latest: "v0.1.0"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			folder := t.TempDir()
			if err := os.Chmod(folder, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(folder, "certpouch")
			original := []byte("original binary")
			if err := os.WriteFile(path, original, 0700); err != nil {
				t.Fatal(err)
			}
			running, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.locked {
				guard, err := lockUpdate(path + ".update-lock")
				if err != nil {
					t.Fatal(err)
				}
				defer guard.Close()
			}
			g := github{client: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.String() {
				case latestURL:
					return testResponse(http.StatusOK, fmt.Sprintf(`{"tag_name":%q}`, tc.latest)), nil
				case downloadURL + newVersion + "/SHA256SUMS":
					if !tc.update && !tc.badDigest {
						t.Fatal("download attempted when no update should be installed")
					}
					hash := sha256.Sum256(compressed.Bytes())
					if tc.badDigest {
						hash[0] ^= 0xff
					}
					return testResponse(http.StatusOK, fmt.Sprintf("%x  %s.tar.gz\n", hash, name)), nil
				case downloadURL + newVersion + "/" + name + ".tar.gz":
					return testResponse(http.StatusOK, compressed.String()), nil
				default:
					t.Fatalf("unexpected request to %s", r.URL)
					return nil, nil
				}
			})}}
			if _, changed, err := g.apply(t.Context(), path, running, oldVersion); changed != tc.update ||
				(err != nil) != (tc.badDigest || tc.locked) {
				t.Fatalf("update result = %t, %v", changed, err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.update {
				previous, err := os.ReadFile(path + ".previous")
				if err != nil || !bytes.Equal(got, body) || !bytes.Equal(previous, original) {
					t.Fatal("verified release or rollback binary is missing")
				}
			} else if !bytes.Equal(got, original) {
				t.Fatal("update modified the executable despite not installing a release")
			}
		})
	}
}

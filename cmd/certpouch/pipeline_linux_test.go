package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelineCheckDoesNotApply(t *testing.T) {
	base := t.TempDir()
	mkdir := func(name string) string {
		t.Helper()
		path := filepath.Join(base, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	bin, stage, zones := mkdir("bin"), mkdir("stage"), mkdir("zones")
	t.Setenv("PATH", bin)
	controlCalled := filepath.Join(base, "control.called")
	for _, tool := range []string{"nsd-checkzone", "nsd-control"} {
		write(filepath.Join(bin, tool), fmt.Sprintf("#!/bin/sh\n: > '%s'\nexit 1\n", controlCalled), 0700)
	}
	checkCalled := filepath.Join(base, "check.called")
	checkconf := filepath.Join(bin, "nsd-checkconf")
	configPath, nsdPath := filepath.Join(base, "server.yaml"), filepath.Join(base, "nsd.conf")
	include, cache := filepath.Join(base, "zones.conf"), filepath.Join(base, "cache.json")
	write(nsdPath, "# Test NSD configuration\n", 0600)
	credentials := writeTestSheetsCredentials(t, base)
	for _, tc := range []struct {
		name, includePath, body, stagePath, address string
		mode                                        os.FileMode
		checkExit, wantCode                         int
		wantError                                   string
	}{
		{"valid", include, managedNSDHeader, stage, "", 0600, 0, 0, ""},
		{"missing stage", include, managedNSDHeader, filepath.Join(base, "missing"), "", 0600, 0, 1, "existing absolute directory"},
		{"shared config", nsdPath, managedNSDHeader, stage, "", 0600, 0, 1, "dedicated absolute path"},
		{"unmanaged include", include, "# Hand managed\n", stage, "", 0600, 0, 1, "must begin with"},
		{"writable include", include, managedNSDHeader, stage, "", 0660, 0, 1, "protected from other writers"},
		{"non-loopback DNS", include, managedNSDHeader, stage, "192.0.2.10:53", 0600, 0, 1, "loopback IP"},
		{"invalid NSD config", include, managedNSDHeader, stage, "", 0600, 1, 1, "validate NSD configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Remove(checkCalled); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			write(include, tc.body, tc.mode)
			write(checkconf, fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$1\" > '%s'\nexit %d\n", checkCalled, tc.checkExit), 0700)
			write(configPath, fmt.Sprintf(`inventory:
  cache_file: %q
  fallback_prefix: "192.0.2"
  sheets:
    spreadsheet_id: "spreadsheet_test"
    ranges: ["Inventory!A2:G256", "Other!A2:G256"]
    credentials_file: %q
dns:
  zone: "example.internal"
  nsd:
    stage_dir: %q
    zone_dir: %q
    config_file: %q
    include_file: %q
    dns_address: %q
`, cache, credentials, tc.stagePath, zones, nsdPath, tc.includePath, tc.address), 0600)
			var stdout, stderr bytes.Buffer
			code := runPipeline(t.Context(), []string{"--config", configPath, "--check"}, &stdout, &stderr)
			if code != tc.wantCode || !strings.Contains(stderr.String(), tc.wantError) {
				t.Fatalf("pipeline --check exit %d, stderr %q", code, stderr.String())
			}
			if code == 0 || tc.checkExit != 0 {
				calledWith, err := os.ReadFile(checkCalled)
				if err != nil || string(calledWith) != nsdPath {
					t.Fatalf("nsd-checkconf argument = %q, %v", calledWith, err)
				}
			} else if _, err := os.Stat(checkCalled); !os.IsNotExist(err) {
				t.Fatalf("unexpected checkconf invocation: %v", err)
			}
			if content, err := os.ReadFile(include); err != nil || string(content) != tc.body {
				t.Fatalf("check changed NSD include: %q, %v", content, err)
			}
			for _, path := range []string{cache, controlCalled} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("unexpected check side effect for %s: %v", path, err)
				}
			}
		})
	}
}

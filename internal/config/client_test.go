package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/distribution"
)

func TestLoadClientDeployment(t *testing.T) {
	issuer := writeTestIssuer(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "client.yaml")
	token := filepath.Join(dir, "token")
	state := filepath.Join(dir, "state")
	dest := filepath.Join(dir, "web")
	const name = "web.example.internal"
	if err := os.WriteFile(token, []byte("test-token\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf(`server: {url: "https://certs.example.internal", ca_file: %q}
client: {id: "web", token_file: %q, state_dir: %q}
validation: {trust_ca_file: %q, issuer_certificate: %q}
certificates:
  - name: %q
    destination: %q
`, issuer, token, state, issuer, issuer, name, dest)
	const command = `    command:
      args: ["reload-app", "service name", "$ENV"]
      timeout: 10s
      verify_tls: {address: "127.0.0.1:8443", server_name: "web.example.internal", timeout: 2s}
`
	writeConfig := func(t *testing.T, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(t, base+command)
	client, err := LoadClient(path)
	if err != nil {
		t.Fatal(err)
	}
	if client.Token != "test-token" || client.TLSRoots == nil || len(client.Targets) != 1 {
		t.Fatal("client authentication or deployment settings were not loaded")
	}
	target := client.Targets[0]
	if !slices.Equal(target.CommandArgs, []string{"reload-app", "service name", "$ENV"}) ||
		target.CommandTimeout != 10*time.Second || target.TLSAddress != "127.0.0.1:8443" ||
		target.TLSServerName != name || target.TLSVerifyTimeout != 2*time.Second ||
		filepath.Dir(target.StateFile) != state || !slices.Equal(target.Policy.DNS, []string{name}) {
		t.Fatalf("unexpected deployment settings: %+v", target)
	}
	if err := os.Remove(token); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadClient(path); err == nil || !strings.Contains(err.Error(), "read client token file") {
		t.Fatalf("LoadClient with missing token: %v", err)
	}
	local, err := LoadLocalClient(path)
	if err != nil || len(local.Targets) != 1 || local.Targets[0].StateFile != target.StateFile {
		t.Fatalf("local status cannot find the deployment state: %+v, %v", local.Targets, err)
	}
	ipConfig := strings.Replace(base, fmt.Sprintf("token_file: %q", token), "auth: ip", 1)
	writeConfig(t, ipConfig)
	ipClient, err := LoadClient(path)
	if err != nil || ipClient.Auth != distribution.AuthIP || ipClient.Token != "" {
		t.Fatalf("IP client configuration: %v", err)
	}
	writeConfig(t, strings.Replace(ipConfig, "auth: ip", "auth: mtls", 1))
	if _, err := LoadClient(path); err == nil || !strings.Contains(err.Error(), "requires absolute client.tls_certificate") {
		t.Fatalf("mTLS configuration without credentials: %v", err)
	}

	for _, tc := range []struct {
		name, config, want string
	}{
		{"two activation mechanisms", base + command + "    service: {unit: web.service, action: reload}\n", "cannot configure both service and command"},
		{"empty executable", base + "    command: {args: [\"\"]}\n", "requires an executable"},
		{"invalid command timeout", base + strings.Replace(command, "10s", "0s", 1), "must be a positive duration"},
		{"invalid verification timeout", base + strings.Replace(command, "2s", "3m", 1), "must be between 1s and 2m"},
		{"overlapping deployments", base + fmt.Sprintf("  - {name: other.example.internal, destination: %q}\n", filepath.Join(dest, "other")), "destinations must not overlap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeConfig(t, tc.config)
			if _, err := LoadLocalClient(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadLocalClient error = %v, want %q", err, tc.want)
			}
		})
	}
}

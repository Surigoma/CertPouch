package main

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/Surigoma/CertPouch/internal/config"
)

func TestInitClientAndServer(t *testing.T) {
	base := t.TempDir()
	certs := workflowCertificates(t)
	write := func(name string, body []byte) string {
		t.Helper()
		path := filepath.Join(base, name)
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	ca := write("ca.pem", certs.caPEM)
	cert := write("server.pem", certs.listener.cert)
	key := write("server.key", certs.listener.key)
	csv := write("inventory.csv", []byte(",192.0.2,10,node,,,web\n"))
	common := []string{"--id", "workflow", "--name", "web.example.internal", "--trust-ca", ca, "--issuer", ca}
	var out, diagnostic bytes.Buffer
	run := func(role string, args ...string) int {
		out.Reset()
		diagnostic.Reset()
		return runInit(append(append([]string{role}, common...), args...), &out, &diagnostic)
	}
	clientDir := filepath.Join(base, "client")
	clientArgs := []string{"--out", clientDir, "--url", "https://cert.example.internal:8443", "--server-ca", ca, "--unit", "nginx.service"}
	if code := run("client", clientArgs...); code != 0 {
		t.Fatalf("client init exit %d: %s", code, &diagnostic)
	}
	cfg, err := config.LoadClient(filepath.Join(clientDir, "client.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), []byte(cfg.Token)) {
		t.Fatal("client init printed its token")
	}
	if code := run("client", clientArgs...); code == 0 {
		t.Fatal("client init accepted an existing destination")
	}
	unchanged, err := config.LoadClient(filepath.Join(clientDir, "client.yaml"))
	if err != nil || unchanged.Token != cfg.Token {
		t.Fatalf("retry changed client credentials: %v", err)
	}
	serverDir := filepath.Join(base, "server")
	serverArgs := []string{"--out", serverDir, "--tls-cert", cert, "--tls-key", key, "--csv", csv,
		"--zone", "example.internal", "--fallback-prefix", "192.0.2"}
	if code := run("server", append(serverArgs, "--token-hash", filepath.Join(clientDir, "client.token"))...); code == 0 {
		t.Fatal("server init accepted a plaintext token")
	}
	if _, err := os.Stat(serverDir); !os.IsNotExist(err) {
		t.Fatalf("invalid credentials wrote server files: %v", err)
	}
	if code := run("server", append(serverArgs, "--token-hash", filepath.Join(clientDir, "client.token.sha256"))...); code != 0 {
		t.Fatalf("server init exit %d: %s", code, &diagnostic)
	}
	server, err := config.LoadServer(filepath.Join(serverDir, "server.yaml"))
	if err != nil || len(server.Clients) != 1 || server.Clients[0].TokenHash != sha256.Sum256([]byte(cfg.Token)) {
		t.Fatalf("server config did not retain the expected hash: %v", err)
	}
	if _, err := os.Stat(filepath.Join(serverDir, "client.token")); !os.IsNotExist(err) {
		t.Fatalf("unexpected plaintext token on server: %v", err)
	}
	for _, path := range []string{filepath.Join(clientDir, "client.token"), filepath.Join(serverDir, "client.token.sha256")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("expected %s to be protected: %v", path, err)
		}
	}
	badDir := filepath.Join(base, "invalid")
	if code := run("client", "--out", badDir, "--url", "http://example.internal", "--server-ca", ca, "--unit", "nginx.service"); code == 0 {
		t.Fatal("client init accepted HTTP")
	}
	if _, err := os.Stat(badDir); !os.IsNotExist(err) {
		t.Fatalf("validation did not clean up the new directory: %v", err)
	}
}

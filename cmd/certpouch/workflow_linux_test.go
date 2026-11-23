package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLinuxPublicationAndPullWorkflow(t *testing.T) {
	base := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(base, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	mkdir := func(name string) string {
		t.Helper()
		path := filepath.Join(base, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	store, destination := mkdir("store"), mkdir("destination")
	caPEM, ca, tlsCert, tlsKey, firstCert, firstKey, secondCert, secondKey := workflowCertificates(t)
	caFile := write("ca.pem", caPEM)
	tlsCertFile, tlsKeyFile := write("tls.pem", tlsCert), write("tls.key", tlsKey)
	firstFile, firstKeyFile := write("first.pem", firstCert), write("first.key", firstKey)
	secondFile, secondKeyFile := write("second.pem", secondCert), write("second.key", secondKey)
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	tokenFile := write("token", []byte(token+"\n"))
	hashFile := write("token.sha256", []byte(fmt.Sprintf("%x\n", sha256.Sum256([]byte(token)))))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	origin := "https://" + addr
	serverConfig := write("server.yaml", []byte(fmt.Sprintf(`
server:
  listen: %q
  tls: {certificate: %q, private_key: %q}
storage: {certificate_dir: %q, trust_ca_file: %q}
certificates:
  - {name: web.example.internal, expected_dns_sans: [web.example.internal], issuer_certificate: %q}
clients:
  - {id: workflow, enabled: true, allowed_ips: ["127.0.0.1"], allowed_certificates: [web.example.internal], token_hash_file: %q}
`, addr, tlsCertFile, tlsKeyFile, store, caFile, caFile, hashFile)))
	unit := "workflow.service"
	realSystemd := os.Getenv("CERTPOUCH_TEST_SYSTEMD") == "1"
	logPath := filepath.Join(base, "systemctl.log")
	if realSystemd {
		suffix := make([]byte, 8)
		if _, err := rand.Read(suffix); err != nil {
			t.Fatal(err)
		}
		unit = fmt.Sprintf("certpouch-workflow-%x.service", suffix)
		cmd := exec.Command("systemd-run", "--unit="+unit, "--collect", "--property=Type=exec",
			"--property=ExecReload=/usr/bin/touch "+logPath, "/usr/bin/sleep", "infinity")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("start isolated systemd consumer: %v: %s", err, output)
		}
		t.Cleanup(func() {
			if output, err := exec.Command("systemctl", "stop", "--", unit).CombinedOutput(); err != nil {
				t.Errorf("stop isolated consumer: %v: %s", err, output)
			}
		})
	} else {
		shimDir := mkdir("bin")
		if err := os.WriteFile(filepath.Join(shimDir, "systemctl"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CERTPOUCH_TEST_SYSTEMCTL_LOG\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("CERTPOUCH_TEST_SYSTEMCTL_LOG", logPath)
	}
	clientConfig := write("client.yaml", []byte(fmt.Sprintf(`
server: {url: %q, ca_file: %q}
client: {id: workflow, token_file: %q}
validation: {trust_ca_file: %q}
certificates:
  - name: web.example.internal
    destination: %q
    expected_dns_sans: [web.example.internal]
    issuer_certificate: %q
    service: {unit: %q, action: reload}
`, origin, caFile, tokenFile, caFile, destination, caFile, unit)))
	var output, diagnostic bytes.Buffer
	publish := func(cert, key string) string {
		t.Helper()
		output.Reset()
		diagnostic.Reset()
		if code := runPublish([]string{"--config", serverConfig, "--name", "web.example.internal", "--certificate", cert, "--private-key", key}, &output, &diagnostic); code != 0 {
			t.Fatalf("publish exit %d: %s", code, &diagnostic)
		}
		fields := strings.Fields(output.String())
		if len(fields) != 2 {
			t.Fatalf("unexpected publish response: %s", &output)
		}
		return fields[1]
	}
	tlsRoots := x509.NewCertPool()
	tlsRoots.AddCert(ca)
	httpClient := &http.Client{Timeout: 300 * time.Millisecond, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: tlsRoots, MinVersion: tls.VersionTLS12},
	}}
	defer httpClient.CloseIdleConnections()
	start := func() func() {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan int, 1)
		go func() { result <- run(ctx, []string{"server", "--config", serverConfig}, io.Discard) }()
		stop := func() {
			t.Helper()
			cancel()
			select {
			case code := <-result:
				if code != 0 {
					t.Errorf("server returned %d", code)
				}
			case <-time.After(5 * time.Second):
				t.Error("server did not stop")
			}
			httpClient.CloseIdleConnections()
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := httpClient.Get(origin + "/api/v1/health")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return stop
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		stop()
		t.Fatal("HTTPS server did not become ready")
		return nil
	}
	pull := func(action, version string, preview bool) {
		t.Helper()
		output.Reset()
		diagnostic.Reset()
		args := []string{"--config", clientConfig}
		if preview {
			args = append(args, "--dry-run")
		}
		if code := runPull(t.Context(), args, &output, &diagnostic); code != 0 {
			t.Fatalf("pull exit %d: %s", code, &diagnostic)
		}
		if want := fmt.Sprintf("web.example.internal\t%s\t%s\n", action, version); output.String() != want {
			t.Fatalf("pull output = %q, want %q", output.String(), want)
		}
	}
	first := publish(firstFile, firstKeyFile)
	var firstReload time.Time
	func() {
		stop := start()
		defer stop()
		pull("install", first, true)
		if _, err := os.Stat(filepath.Join(destination, "current")); !os.IsNotExist(err) {
			t.Fatal("dry-run changed the active generation")
		}
		pull("install", first, false)
		if realSystemd {
			firstReload = reloadTime(t, logPath)
		}
		pull("unchanged", first, false)
		if realSystemd && !reloadTime(t, logPath).Equal(firstReload) {
			t.Fatal("unchanged certificate triggered a systemd reload")
		}
	}()
	second := publish(secondFile, secondKeyFile)
	func() {
		stop := start()
		defer stop()
		pull("update", second, false)
		var secondReload time.Time
		if realSystemd {
			secondReload = reloadTime(t, logPath)
			if !secondReload.After(firstReload) {
				t.Fatal("updated certificate did not trigger a systemd reload")
			}
		}
		pull("unchanged", second, false)
		if realSystemd && !reloadTime(t, logPath).Equal(secondReload) {
			t.Fatal("unchanged updated certificate triggered a systemd reload")
		}
	}()

	output.Reset()
	diagnostic.Reset()
	if code := runStatus([]string{"--config", clientConfig}, &output, &diagnostic); code != 0 ||
		!strings.Contains(output.String(), second) || !strings.Contains(output.String(), "last_successful_sync") {
		t.Fatalf("offline status exit %d: %s %s", code, &output, &diagnostic)
	}
	if realSystemd {
		return
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "reload -- workflow.service\nis-active --quiet -- workflow.service\n"
	if string(log) != want+want {
		t.Fatalf("unexpected service operations: %q", log)
	}
}

func reloadTime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("read systemd reload marker: %v", err)
	}
	return info.ModTime()
}

func workflowCertificates(t *testing.T) ([]byte, *x509.Certificate, []byte, []byte, []byte, []byte, []byte, []byte) {
	t.Helper()
	newKey := func() *rsa.PrivateKey {
		t.Helper()
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	issue := func(template, issuer *x509.Certificate, key, signer *rsa.PrivateKey) ([]byte, *x509.Certificate) {
		t.Helper()
		der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, signer)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cert
	}
	template := func(serial int64) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber: big.NewInt(serial), NotBefore: time.Now().Add(-time.Hour),
			NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
	}
	caKey := newKey()
	root := template(1)
	root.Subject = pkix.Name{CommonName: "Workflow test CA"}
	root.IsCA, root.BasicConstraintsValid, root.KeyUsage, root.ExtKeyUsage = true, true, x509.KeyUsageCertSign, nil
	caPEM, ca := issue(root, root, caKey, caKey)
	leaf := func(serial int64, tlsServer bool) ([]byte, []byte) {
		node, key := template(serial), newKey()
		if tlsServer {
			node.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		} else {
			node.DNSNames = []string{"web.example.internal"}
		}
		cert, _ := issue(node, ca, key, caKey)
		return cert, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	}
	tlsCert, tlsKey := leaf(2, true)
	firstCert, firstKey := leaf(3, false)
	secondCert, secondKey := leaf(4, false)
	return caPEM, ca, tlsCert, tlsKey, firstCert, firstKey, secondCert, secondKey
}

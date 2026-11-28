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
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/client"
	"github.com/Surigoma/CertPouch/internal/deployment"
	"github.com/Surigoma/CertPouch/internal/dns"
	"github.com/Surigoma/CertPouch/internal/pki"
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
	certs := workflowCertificates(t)
	caFile := write("ca.pem", certs.caPEM)
	tlsCertFile, tlsKeyFile := write("tls.pem", certs.listener.cert), write("tls.key", certs.listener.key)
	firstFile, firstKeyFile := write("first.pem", certs.first.cert), write("first.key", certs.first.key)
	secondFile, secondKeyFile := write("second.pem", certs.second.cert), write("second.key", certs.second.key)
	clientCertFile, clientKeyFile := write("client.pem", certs.client.cert), write("client.key", certs.client.key)
	clientPair := certs.client.tlsPair(t)
	clientFingerprint := fmt.Sprintf("%x", sha256.Sum256(clientPair.Certificate[0]))
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
	cacheBody := func(octet string) []byte {
		return []byte(fmt.Sprintf(`{"valueRanges":[{"values":[["","127.0.0",%q,"node","","","web"]]}]}`, octet))
	}
	cacheRecord := func(body []byte, now time.Time) []byte {
		return []byte(fmt.Sprintf(`{"fetched_at":%q,"sha256":%q}`, now.UTC().Format(time.RFC3339Nano), fmt.Sprintf("%x", sha256.Sum256(body))))
	}
	initialInventory := cacheBody("1")
	cache := write("inventory.json", initialInventory)
	write("inventory.json"+dns.CacheFreshnessSuffix, cacheRecord(initialInventory, time.Now()))
	const noRevocations = "client_ids: []\nclient_certificate_sha256: []\n"
	revocationFile := write("revocations.yaml", []byte(noRevocations))
	serverConfig := write("server.yaml", []byte(fmt.Sprintf(`
distribution:
  listen: %q
  refresh_interval: "1s"
  revocation_file: %q
  revocation_refresh: "1s"
  tls: {certificate_file: %q, private_key_file: %q, client_ca: %q}
  clients:
    - {id: workflow, enabled: true, token_hash_file: %q, client_certificate_sha256: [%q]}
certificates:
  store_dir: %q
  trust_ca_file: %q
  targets:
    - {name: web.example.internal, expected_dns_sans: [web.example.internal], issuer_certificate: %q}
inventory: {cache_file: %q, fallback_prefix: "127.0.0", max_age: "1h"}
dns: {zone: example.internal}
`, addr, revocationFile, tlsCertFile, tlsKeyFile, caFile, hashFile, clientFingerprint, store, caFile, caFile, cache)))
	unit := "workflow.service"
	realSystemd := os.Getenv("CERTPOUCH_TEST_SYSTEMD") == "1"
	keycloakHome := os.Getenv("CERTPOUCH_TEST_KEYCLOAK_HOME")
	useKeycloak := realSystemd && keycloakHome != ""
	action := "reload"
	logPath := filepath.Join(base, "systemctl.log")
	tlsVerification := ""
	permissions := ""
	consumerAddress := ""
	staleReload := filepath.Join(base, "stale-reload")
	if useKeycloak {
		action = "restart"
		consumer, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		consumerAddress = consumer.Addr().String()
		port := fmt.Sprint(consumer.Addr().(*net.TCPAddr).Port)
		if err := consumer.Close(); err != nil {
			t.Fatal(err)
		}
		launcher, err := filepath.Abs(filepath.Join(keycloakHome, "bin", "kc.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(launcher); err != nil {
			t.Fatal(err)
		}
		preStart := write("keycloak-pre-start.sh", []byte(`#!/bin/sh
set -eu
if [ -f "$1" ]; then
  rm -- "$1"
  exit 1
fi
touch -- "$2"
`))
		current := filepath.Join(destination, "current")
		certificate := filepath.Join(current, "fullchain.pem")
		command := keycloakTestCommand(launcher, port, certificate, filepath.Join(current, "private-key.pem"))
		unit = startKeycloakTestUnit(t, certificate, command,
			"ExecStartPre=/bin/sh "+strings.Join([]string{preStart, staleReload, logPath}, " "))
		tlsVerification = fmt.Sprintf("\n      verify_tls: {address: %q, server_name: web.example.internal, timeout: 2m}", consumerAddress)
	} else if realSystemd {
		serviceUser, err := user.Lookup("nobody")
		if err != nil {
			t.Fatal(err)
		}
		serviceGroup, err := user.LookupGroupId(serviceUser.Gid)
		if err != nil {
			t.Fatal(err)
		}
		uid, err := strconv.Atoi(serviceUser.Uid)
		if err != nil || uid <= 0 {
			t.Fatalf("invalid test service UID %q: %v", serviceUser.Uid, err)
		}
		gid, err := strconv.Atoi(serviceGroup.Gid)
		if err != nil || gid <= 0 {
			t.Fatalf("invalid test service GID %q: %v", serviceGroup.Gid, err)
		}
		for _, path := range []string{filepath.Dir(base), base} {
			if err := os.Chmod(path, 0711); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chown(destination, -1, gid); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(destination, 0750); err != nil {
			t.Fatal(err)
		}
		serviceState := mkdir("consumer")
		if err := os.Chown(serviceState, uid, gid); err != nil {
			t.Fatal(err)
		}
		served := filepath.Join(serviceState, "served")
		staleReload = filepath.Join(serviceState, "stale-reload")
		logPath = filepath.Join(serviceState, "systemctl.log")
		permissions = fmt.Sprintf("\n    group: %q\n    private_key_mode: \"0640\"", serviceGroup.Name)
		consumer, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		endpoint := &http.Server{TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				generation, err := os.Readlink(served)
				if err != nil {
					return nil, err
				}
				pair, err := tls.LoadX509KeyPair(filepath.Join(generation, "fullchain.pem"), filepath.Join(generation, "private-key.pem"))
				return &pair, err
			},
		}, ErrorLog: log.New(io.Discard, "", 0)}
		go func() { _ = endpoint.ServeTLS(consumer, "", "") }()
		t.Cleanup(func() {
			_ = endpoint.Close()
			_ = consumer.Close()
		})
		consumerAddress = consumer.Addr().String()
		tlsVerification = fmt.Sprintf("\n      verify_tls: {address: %q, server_name: web.example.internal, timeout: 1s}", consumerAddress)
		reload := write("reload.sh", []byte(`#!/bin/sh
set -eu
if [ -f "$1" ]; then
  rm -- "$1"
  exit 0
fi
generation=$(readlink -f -- "$2/current")
test "$(stat -c %a -- "$generation/private-key.pem")" = 640
test ! -w "$generation/private-key.pem"
test ! -w "$generation"
head -c 1 -- "$generation/private-key.pem" >/dev/null
head -c 1 -- "$generation/fullchain.pem" >/dev/null
ln -s "$generation" "$3.next"
mv -Tf -- "$3.next" "$3"
touch -- "$4"
`))
		if err := os.Chmod(reload, 0644); err != nil {
			t.Fatal(err)
		}
		suffix := make([]byte, 8)
		if _, err := rand.Read(suffix); err != nil {
			t.Fatal(err)
		}
		unit = fmt.Sprintf("certpouch-workflow-%x.service", suffix)
		cmd := exec.Command("systemd-run", "--unit="+unit, "--collect", "--property=Type=exec",
			"--property=User="+serviceUser.Username, "--property=Group="+serviceGroup.Name,
			"--property=ExecReload=/bin/sh "+strings.Join([]string{reload, staleReload, destination, served, logPath}, " "),
			"/usr/bin/sleep", "infinity")
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
client: {id: workflow, token_file: %q, tls_certificate: %q, tls_private_key: %q}
validation: {trust_ca_file: %q}
certificates:
  - name: web.example.internal
    destination: %q
    expected_dns_sans: [web.example.internal]
    issuer_certificate: %q%s
    service:
      unit: %q
      action: %s%s
`, origin, caFile, tokenFile, clientCertFile, clientKeyFile, caFile, destination, caFile, permissions, unit, action, tlsVerification)))
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
	tlsRoots.AddCert(certs.ca)
	httpClient := &http.Client{Timeout: 300 * time.Millisecond, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: tlsRoots, MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{clientPair}},
	}}
	defer httpClient.CloseIdleConnections()
	start := func() func() {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan int, 1)
		go func() { result <- run(ctx, []string{"server", "--config", serverConfig}, io.Discard, io.Discard) }()
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
	if code := run(t.Context(), []string{"server", "--config", serverConfig, "--check-config"}, io.Discard, &diagnostic); code != 0 {
		t.Fatalf("server preflight exit %d: %s", code, &diagnostic)
	}
	serverYAML, err := os.ReadFile(serverConfig)
	if err != nil {
		t.Fatal(err)
	}
	badTLS := write("server-mismatched-key.yaml", bytes.Replace(serverYAML, []byte(tlsKeyFile), []byte(firstKeyFile), 1))
	diagnostic.Reset()
	if code := run(t.Context(), []string{"server", "--config", badTLS, "--check-config"}, io.Discard, &diagnostic); code == 0 ||
		!strings.Contains(diagnostic.String(), "check server TLS key pair") {
		t.Fatalf("server preflight accepted a mismatched TLS key: %d, %s", code, &diagnostic)
	}
	var firstReload time.Time
	stop := start()
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	noCert, err := client.New(origin, "workflow", token, tlsRoots)
	if err != nil {
		t.Fatal(err)
	}
	defer noCert.CloseIdleConnections()
	if _, err := noCert.Inventory(t.Context()); err == nil || strings.Contains(err.Error(), "returned HTTP") {
		t.Fatalf("client without a certificate was not rejected by TLS: %v", err)
	}
	unassignedPair := certs.otherClient.tlsPair(t)
	unassigned, err := client.NewWithCertificate(origin, "workflow", token, tlsRoots, &unassignedPair)
	if err != nil {
		t.Fatal(err)
	}
	defer unassigned.CloseIdleConnections()
	if _, err := unassigned.Inventory(t.Context()); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("trusted certificate assigned to another identity was not rejected by the API: %v", err)
	}
	invalidToken := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	wrongToken, err := client.NewWithCertificate(origin, "workflow", invalidToken, tlsRoots, &clientPair)
	if err != nil {
		t.Fatal(err)
	}
	defer wrongToken.CloseIdleConnections()
	if _, err := wrongToken.Inventory(t.Context()); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("valid mTLS certificate bypassed the token check: %v", err)
	}
	func() {
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
	inventory, err := client.NewWithCertificate(origin, "workflow", token, tlsRoots, &clientPair)
	if err != nil {
		t.Fatal(err)
	}
	defer inventory.CloseIdleConnections()
	wait, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		entries, err := inventory.Inventory(wait)
		if err == nil && len(entries) == 1 && entries[0].Version == second {
			break
		}
		if wait.Err() != nil {
			t.Fatalf("server did not load new publication without restarting: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	func() {
		previousReload := firstReload
		if realSystemd {
			if err := os.WriteFile(staleReload, nil, 0600); err != nil {
				t.Fatal(err)
			}
			output.Reset()
			diagnostic.Reset()
			expectedFailure := "different leaf certificate"
			if useKeycloak {
				expectedFailure = "systemctl restart " + unit
			}
			if code := runPull(t.Context(), []string{"--config", clientConfig}, &output, &diagnostic); code == 0 ||
				!strings.Contains(diagnostic.String(), expectedFailure) {
				t.Fatalf("expected service activation failure %q: %d, %s", expectedFailure, code, &diagnostic)
			}
			active, err := os.Readlink(filepath.Join(destination, "current"))
			if err != nil || filepath.Base(active) != first {
				t.Fatalf("previous generation was not restored: %q, %v", active, err)
			}
			if _, err := os.Stat(staleReload); !os.IsNotExist(err) {
				t.Fatalf("systemd did not consume the stale-reload marker: %v", err)
			}
			previousReload = reloadTime(t, logPath)
			if !previousReload.After(firstReload) {
				t.Fatal("systemd did not reload the restored generation")
			}
			roots := x509.NewCertPool()
			roots.AddCert(certs.ca)
			probe, err := deployment.NewTLSProbe(consumerAddress, "web.example.internal", roots, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			firstLeaf, err := pki.DescribeLeaf(certs.first.cert)
			if err != nil {
				t.Fatal(err)
			}
			if err := probe.Wait(t.Context(), firstLeaf.Fingerprint); err != nil {
				t.Fatalf("verify restored certificate: %v", err)
			}
			output.Reset()
			diagnostic.Reset()
			if code := runStatus([]string{"--config", clientConfig}, &output, &diagnostic); code != 1 ||
				!strings.Contains(output.String(), "\"last_error_category\": \"deployment\"") ||
				!strings.Contains(output.String(), "last_successful_sync") {
				t.Fatalf("rollback status exit %d: %s %s", code, &output, &diagnostic)
			}
		}
		if realSystemd && !useKeycloak {
			runWorkflowTimer(t, base, unit, clientConfig, second)
		} else {
			pull("update", second, false)
		}
		var secondReload time.Time
		if realSystemd {
			secondReload = reloadTime(t, logPath)
			if !secondReload.After(previousReload) {
				t.Fatal("updated certificate did not trigger a systemd reload")
			}
		}
		pull("unchanged", second, false)
		if realSystemd && !reloadTime(t, logPath).Equal(secondReload) {
			t.Fatal("unchanged updated certificate triggered a systemd reload")
		}
	}()

	replaceInventory := func(content []byte, fetchedAt time.Time) {
		t.Helper()
		candidate := write("inventory.next", content)
		if err := os.Rename(candidate, cache); err != nil {
			t.Fatal(err)
		}
		record := write("inventory-record.next", cacheRecord(content, fetchedAt))
		if err := os.Rename(record, cache+dns.CacheFreshnessSuffix); err != nil {
			t.Fatal(err)
		}
	}
	waitInventory := func(expect func(int, error) bool) {
		t.Helper()
		wait, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		for {
			entries, err := inventory.Inventory(wait)
			if expect(len(entries), err) {
				return
			}
			if wait.Err() != nil {
				t.Fatalf("inventory authorization did not converge: count=%d: %v", len(entries), err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	replaceInventory(cacheBody("2"), time.Now())
	waitInventory(func(count int, err error) bool { return err == nil && count == 0 })
	replaceInventory([]byte("malformed cache"), time.Now())
	waitInventory(func(_ int, err error) bool { return err != nil && strings.Contains(err.Error(), "HTTP 401") })
	replaceInventory(cacheBody("1"), time.Now())
	waitInventory(func(count int, err error) bool { return err == nil && count == 1 })
	replaceInventory(cacheBody("1"), time.Now().Add(-2*time.Hour))
	waitInventory(func(_ int, err error) bool { return err != nil && strings.Contains(err.Error(), "HTTP 401") })
	replaceInventory(cacheBody("1"), time.Now())
	waitInventory(func(count int, err error) bool { return err == nil && count == 1 })

	replaceRevocations := func(contents string) {
		t.Helper()
		next := write("revocations.next", []byte(contents))
		if err := os.Rename(next, revocationFile); err != nil {
			t.Fatal(err)
		}
	}
	deny := func(_ int, err error) bool { return err != nil && strings.Contains(err.Error(), "HTTP 401") }
	allow := func(count int, err error) bool { return err == nil && count == 1 }
	replaceRevocations("client_ids: [workflow]\nclient_certificate_sha256: []\n")
	waitInventory(deny)
	replaceRevocations(noRevocations)
	waitInventory(allow)
	replaceRevocations(fmt.Sprintf("client_ids: []\nclient_certificate_sha256: [%q]\n", clientFingerprint))
	waitInventory(deny)
	// A DNS inventory reload must not bypass the independent revocation list.
	replaceInventory(cacheBody("1"), time.Now())
	waitInventory(deny)
	replaceRevocations(noRevocations)
	waitInventory(allow)
	replaceRevocations("client_ids: []\n")
	waitInventory(deny)
	replaceRevocations(noRevocations)
	waitInventory(allow)

	current := filepath.Join(destination, "current")
	chainPath, fullchain := filepath.Join(current, "chain.pem"), filepath.Join(current, "fullchain.pem")
	chainBackup, err := os.ReadFile(chainPath)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(fullchain)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []struct {
		path string
		body []byte
	}{{chainPath, chainBackup}, {fullchain, backup}} {
		if err := os.WriteFile(artifact.path, append(bytes.Clone(artifact.body), '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	output.Reset()
	diagnostic.Reset()
	if code := runPull(t.Context(), []string{"--dry-run", "--config", clientConfig}, &output, &diagnostic); code != 1 ||
		!strings.Contains(diagnostic.String(), "published artifacts for the same version") {
		t.Fatalf("preview accepted a changed artifact with the same version: %d, %s", code, &diagnostic)
	}
	if err := os.WriteFile(chainPath, chainBackup, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullchain, backup, 0644); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	diagnostic.Reset()
	stop()
	stop = nil
	if code := runStatus([]string{"--config", clientConfig}, &output, &diagnostic); code != 0 ||
		!strings.Contains(output.String(), second) || !strings.Contains(output.String(), "last_successful_sync") ||
		strings.Contains(output.String(), "last_error_category") {
		t.Fatalf("offline status exit %d: %s %s", code, &output, &diagnostic)
	}
	corrupt := bytes.Clone(backup)
	corrupt[0] ^= 1
	if err := os.WriteFile(fullchain, corrupt, 0644); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if code := runStatus([]string{"--config", clientConfig}, &output, &diagnostic); code != 1 ||
		!strings.Contains(output.String(), "\"state\": \"invalid\"") {
		t.Fatalf("status accepted damaged fullchain: %d, %s", code, &output)
	}
	if err := os.WriteFile(fullchain, backup, 0644); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if code := runStatus([]string{"--config", clientConfig}, &output, &diagnostic); code != 0 {
		t.Fatalf("status rejected the restored fixture: %d, %s", code, &output)
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

func runWorkflowTimer(t *testing.T, base, consumer, config, expectedVersion string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	unit := strings.TrimSuffix(consumer, ".service") + "-pull"
	journal := filepath.Join(base, "timer-result")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemd-run", "--unit="+unit, "--collect", "--on-active=1s",
		"--timer-property=AccuracySec=100ms", "--property=UMask=0077", "--property=RuntimeMaxSec=10s",
		"--setenv=CERTPOUCH_TIMER_TEST_CONFIG="+config, "--setenv=CERTPOUCH_TIMER_TEST_RESULT="+journal,
		executable, "-test.run=^TestWorkflowTimerProcess$")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start isolated systemd timer: %v: %s", err, output)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		// Completed transient units may already have been collected.
		_ = exec.CommandContext(cleanup, "systemctl", "stop", "--", unit+".timer", unit+".service").Run()
	}()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("systemd timer did not complete the certificate update")
		case <-ticker.C:
			data, err := os.ReadFile(journal)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("0\nweb.example.internal\tupdate\t%s\n", expectedVersion)
			if string(data) != want {
				t.Fatalf("unexpected timer result: %q, want %q", data, want)
			}
			return
		}
	}
}

// TestWorkflowTimerProcess is executed by the isolated systemd timer.
func TestWorkflowTimerProcess(t *testing.T) {
	cfg, result := os.Getenv("CERTPOUCH_TIMER_TEST_CONFIG"), os.Getenv("CERTPOUCH_TIMER_TEST_RESULT")
	if cfg == "" || result == "" {
		t.Skip("only used by the workflow timer")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := runPull(ctx, []string{"--config", cfg}, &stdout, &stderr)
	tmp := result + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d\n%s%s", code, &stdout, &stderr)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, result); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("pull exited with status %d", code)
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

type workflowKeyPair struct {
	cert, key []byte
}

func (p workflowKeyPair) tlsPair(t *testing.T) tls.Certificate {
	t.Helper()
	result, err := tls.X509KeyPair(p.cert, p.key)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type workflowMaterial struct {
	caPEM                                        []byte
	ca                                           *x509.Certificate
	caKeyPEM                                     []byte
	listener, first, second, client, otherClient workflowKeyPair
}

func workflowCertificates(t *testing.T) workflowMaterial {
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
	leaf := func(serial int64, role string) workflowKeyPair {
		node, key := template(serial), newKey()
		switch role {
		case "listener":
			node.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		case "client":
			node.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		default:
			node.DNSNames = []string{"web.example.internal"}
		}
		cert, _ := issue(node, ca, key, caKey)
		return workflowKeyPair{cert, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})}
	}
	return workflowMaterial{caPEM: caPEM, ca: ca,
		caKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)}),
		listener: leaf(2, "listener"), first: leaf(3, "leaf"),
		second: leaf(4, "leaf"), client: leaf(5, "client"), otherClient: leaf(6, "client")}
}

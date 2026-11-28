package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
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
)

func TestPackagedServerSystemdUnit(t *testing.T) {
	if os.Getenv("CERTPOUCH_TEST_SYSTEMD") != "1" {
		t.Skip("requires an isolated Linux systemd host and administrative privileges")
	}
	account, err := user.Lookup("nobody")
	if err != nil {
		t.Fatal(err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil || uid <= 0 {
		t.Fatal("test service requires an unprivileged UID")
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil || gid <= 0 {
		t.Fatal("test service requires an unprivileged GID")
	}
	base, err := os.MkdirTemp("/run", "certpouch-unit-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(base); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chown(base, uid, gid); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(base, name)
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, uid, gid); err != nil {
			t.Fatal(err)
		}
		return path
	}
	certs := workflowCertificates(t)
	caFile, certFile, keyFile := write("ca.pem", certs.caPEM, 0600), write("tls.pem", certs.listener.cert, 0600), write("tls.key", certs.listener.key, 0600)
	store := filepath.Join(base, "store")
	if err := os.Mkdir(store, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(store, uid, gid); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	config := write("server.yaml", []byte(fmt.Sprintf("distribution:\n  listen: %q\n  tls: {certificate_file: %q, private_key_file: %q}\ncertificates: {store_dir: %q, trust_ca_file: %q}\n",
		addr, certFile, keyFile, store, caFile)), 0600)

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	helper := write("certpouch-test", image, 0700)
	unitTemplate := os.Getenv("CERTPOUCH_SERVER_UNIT_TEMPLATE")
	if unitTemplate == "" {
		unitTemplate = "../../configs/systemd/certpouch-server.service"
	}
	template, err := os.ReadFile(unitTemplate)
	if err != nil {
		t.Fatal(err)
	}
	unit := filepath.Base(base) + ".service"
	unitFile := filepath.Join("/run/systemd/system", unit)
	contents := strings.NewReplacer(
		"User=certpouch", "User="+account.Uid,
		"Group=certpouch", "Group="+account.Gid,
		"ExecStart=/usr/local/bin/certpouch server --config /etc/certpouch/server.yaml",
		fmt.Sprintf("Environment=CERTPOUCH_TEST_SERVER_CONFIG=%s\nExecStart=%s -test.run=^TestPackagedServerProcess$", config, helper),
	).Replace(strings.ReplaceAll(string(template), "\r\n", "\n"))

	systemctl := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	}
	file, err := os.OpenFile(unitFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if output, err := systemctl("stop", "--", unit); err != nil {
			t.Errorf("stop isolated server unit: %v: %s", err, output)
		}
		if err := os.Remove(unitFile); err != nil {
			t.Error(err)
		}
		if output, err := systemctl("daemon-reload"); err != nil {
			t.Errorf("clear isolated server unit: %v: %s", err, output)
		}
	})
	_, err = io.WriteString(file, contents)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("write test unit: %v, %v", err, closeErr)
	}
	if output, err := systemctl("daemon-reload"); err != nil {
		t.Fatalf("load isolated server unit: %v: %s", err, output)
	}
	if output, err := systemctl("start", "--", unit); err != nil {
		t.Fatalf("start isolated server unit: %v: %s", err, output)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certs.ca)
	httpClient := &http.Client{Timeout: 300 * time.Millisecond, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}}
	defer httpClient.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := httpClient.Get("https://" + addr + "/api/v1/health")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	output, _ := systemctl("status", "--no-pager", "--", unit)
	t.Fatalf("isolated server unit did not become healthy: %s", output)
}

func TestPackagedServerProcess(t *testing.T) {
	config := os.Getenv("CERTPOUCH_TEST_SERVER_CONFIG")
	if config == "" {
		t.Skip("only used by the isolated server unit")
	}
	mainArgs := os.Args
	os.Args = []string{mainArgs[0], "server", "--config", config}
	defer func() { os.Args = mainArgs }()
	main()
}

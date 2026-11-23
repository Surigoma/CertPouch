package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/deployment"
	"github.com/Surigoma/CertPouch/internal/pki"
)

func TestKeycloakCertificateDeployment(t *testing.T) {
	home := os.Getenv("CERTPOUCH_TEST_KEYCLOAK_HOME")
	if home == "" {
		t.Skip("set CERTPOUCH_TEST_KEYCLOAK_HOME to a disposable Keycloak distribution")
	}
	launcher, err := filepath.Abs(filepath.Join(home, "bin", "kc.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(launcher); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	material := workflowCertificates(t)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(material.caPEM) {
		t.Fatal("could not load fixture CA")
	}
	const serverName = "web.example.internal"
	const startupTimeout = 2 * time.Minute
	current := filepath.Join(dest, "current")
	certificate := filepath.Join(current, "fullchain.pem")
	key := filepath.Join(current, "private-key.pem")
	command := keycloakTestCommand(launcher, port, certificate, key)

	var session *deployment.Session
	var activate func(context.Context) error
	if os.Getenv("CERTPOUCH_TEST_SYSTEMD") == "1" {
		unit := startKeycloakTestUnit(t, certificate, command)
		session, activate, err = beginTarget(t.Context(), config.Target{
			Destination: dest, Policy: pki.Policy{Roots: roots}, Unit: unit, Action: "restart",
			TLSAddress: address, TLSServerName: serverName, TLSVerifyTimeout: startupTimeout,
		})
	} else {
		probe, probeErr := deployment.NewTLSProbe(address, serverName, roots, startupTimeout)
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		activate = keycloakProcessActivation(t, dest, certificate, command, probe)
		session, err = deployment.Begin(deployment.Options{
			Destination: dest, UID: -1, GID: -1, CertificateMode: 0644,
			PrivateKeyMode: 0600, RecoveryTimeout: startupTimeout + 15*time.Second,
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	first, second, failing := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	injected := errors.New("activation failed after serving the new certificate")
	var served []string
	apply := func(ctx context.Context) error {
		target, err := os.Readlink(current)
		if err != nil {
			return err
		}
		version := filepath.Base(target)
		if err := activate(ctx); err != nil {
			return fmt.Errorf("activate Keycloak generation %s: %w", version, err)
		}
		served = append(served, version)
		if version == failing {
			return injected
		}
		return nil
	}
	install := func(version string, pair workflowKeyPair) error {
		return session.Install(t.Context(), version, pki.Bundle{
			CertificatePEM: pair.cert, PrivateKeyPEM: pair.key,
		}, apply)
	}
	if err := install(first, material.first); err != nil {
		t.Fatal(err)
	}
	if err := install(second, material.second); err != nil {
		t.Fatal(err)
	}
	if err := install(failing, material.first); !errors.Is(err, injected) {
		t.Fatalf("expected injected failure: %v", err)
	}
	if !slices.Equal(served, []string{first, second, failing, second}) {
		t.Fatalf("unexpected TLS activations: %v", served)
	}
}

func keycloakTestCommand(launcher, port, certificate, key string) []string {
	return []string{launcher, "start-dev", "--db=dev-mem",
		"--http-enabled=false", "--http-host=127.0.0.1", "--https-port=" + port,
		"--https-certificate-file=" + certificate, "--https-certificate-key-file=" + key}
}

func startKeycloakTestUnit(t *testing.T, certificate string, command []string, properties ...string) string {
	t.Helper()
	unit := "certpouch-keycloak-" + filepath.Base(filepath.Dir(t.TempDir())) + ".service"
	// Keep a failed transient unit loaded so rollback can restart it.
	args := []string{"--unit=" + unit, "--property=Type=exec", "--property=TimeoutStopSec=15s"}
	for _, property := range properties {
		args = append(args, "--property="+property)
	}
	if java := os.Getenv("JAVA_HOME"); java != "" {
		args = append(args, "--setenv=JAVA_HOME="+java)
	}
	// Start the transient unit before the first install creates the certificate.
	args = append(args, "/bin/sh", "-c", `while [ ! -r "$1" ]; do sleep 1; done; shift; exec "$@"`, "keycloak", certificate)
	args = append(args, command...)
	if output, err := exec.Command("systemd-run", args...).CombinedOutput(); err != nil {
		t.Fatalf("start isolated Keycloak unit: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if t.Failed() {
			output, _ := exec.Command("journalctl", "--no-pager", "--unit", unit, "-n", "40").CombinedOutput()
			t.Logf("Keycloak unit journal:\n%s", output)
		}
		if output, err := exec.Command("systemctl", "stop", "--", unit).CombinedOutput(); err != nil {
			t.Errorf("stop isolated Keycloak unit: %v: %s", err, output)
		}
	})
	return unit
}

func keycloakProcessActivation(t *testing.T, dest, certificate string, command []string, probe *deployment.TLSProbe) func(context.Context) error {
	t.Helper()
	var stop func()
	t.Cleanup(func() {
		if stop != nil {
			stop()
		}
	})
	return func(ctx context.Context) error {
		if stop != nil {
			stop()
			stop = nil
		}
		pem, err := os.ReadFile(certificate)
		if err != nil {
			return err
		}
		leaf, err := pki.DescribeLeaf(pem)
		if err != nil {
			return err
		}
		log, err := os.CreateTemp(dest, "keycloak-*.log")
		if err != nil {
			return err
		}
		defer log.Close()
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Stdout, cmd.Stderr = log, log
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		exited := make(chan struct{})
		probeCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			defer close(exited)
			_ = cmd.Wait()
			cancel()
		}()
		stop = func() {
			select {
			case <-exited:
				return
			default:
			}
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			select {
			case <-exited:
			case <-timer.C:
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-exited
			}
		}
		if err := probe.Wait(probeCtx, leaf.Fingerprint); err != nil {
			output, _ := os.ReadFile(log.Name())
			return fmt.Errorf("%w\n%s", err, output)
		}
		return nil
	}
}

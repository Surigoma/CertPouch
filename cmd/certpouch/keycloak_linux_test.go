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
	probe, err := deployment.NewTLSProbe(address, serverName, roots, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var stop func()
	t.Cleanup(func() {
		if stop != nil {
			stop()
		}
	})
	session, err := deployment.Begin(deployment.Options{
		Destination: dest, UID: -1, GID: -1, CertificateMode: 0644,
		PrivateKeyMode: 0600, RecoveryTimeout: 90 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	first, second, failing := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	injected := errors.New("activation failed after serving the new certificate")
	var served []string
	apply := func(ctx context.Context) error {
		if stop != nil {
			stop()
			stop = nil
		}
		target, err := os.Readlink(filepath.Join(dest, "current"))
		if err != nil {
			return err
		}
		version := filepath.Base(target)
		current := filepath.Join(dest, "current")
		pem, err := os.ReadFile(filepath.Join(current, "fullchain.pem"))
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
		cmd := exec.Command(launcher, "start-dev", "--db=dev-mem",
			"--http-enabled=false", "--http-host=127.0.0.1", "--https-port="+port,
			"--https-certificate-file="+filepath.Join(current, "fullchain.pem"),
			"--https-certificate-key-file="+filepath.Join(current, "private-key.pem"))
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
			return fmt.Errorf("verify Keycloak generation %s: %w\n%s", version, err, output)
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

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Surigoma/CertPouch/internal/config"
	"github.com/Surigoma/CertPouch/internal/dns"
	"github.com/Surigoma/CertPouch/internal/storage"
	"golang.org/x/net/dns/dnsmessage"
)

type pipelineTransport func(*http.Request) (*http.Response, error)

func (f pipelineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type cancelOnWrite struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	w.cancel()
	return w.Buffer.Write(p)
}

func TestPipelineSheetsToCertificatePublication(t *testing.T) {
	base := t.TempDir()
	mkdir := func(name string) string {
		t.Helper()
		path := filepath.Join(base, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write := func(name string, body []byte, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(base, name)
		if err := os.WriteFile(path, body, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	bin, stage, zones, store := mkdir("bin"), mkdir("stage"), mkdir("zones"), mkdir("store")
	t.Setenv("PATH", bin)
	failCheck := filepath.Join(base, "reject-zones")
	t.Setenv("CERTPOUCH_TEST_REJECT_ZONES", failCheck)
	t.Setenv("CERTPOUCH_TEST_ZONE_DIR", zones)
	write("bin/nsd-checkzone", []byte("#!/bin/sh\n[ ! -f \"$CERTPOUCH_TEST_REJECT_ZONES\" ] && [ -s \"$2\" ]\n"), 0700)
	write("bin/nsd-control", []byte("#!/bin/sh\n[ \"$3\" = reconfig ] || [ \"$3\" = reload ]\n"), 0700)
	write("bin/nsd-checkconf", []byte("#!/bin/sh\nif [ \"$1\" = -o ]; then\n  printf '%s/current/%s.zone\\n' \"$CERTPOUCH_TEST_ZONE_DIR\" \"$4\"\nelse\n  [ -f \"$1\" ]\nfi\n"), 0700)
	listener := pipelineSOAServer(t, zones)
	cache := filepath.Join(base, "cache.json")
	include := write("zones.conf", []byte(managedNSDHeader), 0600)
	nsd := write("nsd.conf", nil, 0600)
	material := workflowCertificates(t)
	ca := write("ca.pem", material.caPEM, 0600)
	key := write("ca.key", material.caKeyPEM, 0600)
	listenerCert := write("server.pem", material.listener.cert, 0600)
	listenerKey := write("server.key", material.listener.key, 0600)
	credentials := writeTestSheetsCredentials(t, base)
	path := write("server.yaml", []byte(fmt.Sprintf(`inventory:
  cache_file: %q
  fallback_prefix: "192.0.2"
  sheets: {spreadsheet_id: "spreadsheet_test", ranges: ["Inventory!A2:G10"], credentials_file: %q}
dns:
  zone: example.internal
  zones:
    primary: ns.example.net
    mailbox: hostmaster.example.internal
    nameservers: [ns.example.net]
    reverse_zones: ["2.0.192.in-addr.arpa"]
    ttl: 300
    refresh: 3600
    retry: 600
    expire: 604800
    negative_ttl: 300
  nsd: {config_file: %q, include_file: %q, stage_dir: %q, zone_dir: %q, dns_address: %q}
certificates:
  issuer: {certificate_file: %q, private_key_file: %q}
  trust_ca_file: %q
  store_dir: %q
  validity: 30m
  renew_before: 5m
distribution:
  listen: "127.0.0.1:8443"
  tls: {certificate_file: %q, private_key_file: %q}
`, cache, credentials, nsd, include, stage, zones, listener, ca, key, ca, store, listenerCert, listenerKey)), 0600)

	originalTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	octet, alias := "10", "web"
	http.DefaultTransport = pipelineTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		switch {
		case r.URL.Host == "oauth2.googleapis.com" && r.Method == http.MethodPost:
			body = `{"access_token":"pipeline-test-token","token_type":"Bearer","expires_in":3600}`
		case r.URL.Host == "sheets.googleapis.com" && r.Header.Get("Authorization") == "Bearer pipeline-test-token" &&
			r.URL.Path == "/v4/spreadsheets/spreadsheet_test/values:batchGet" &&
			slices.Equal(r.URL.Query()["ranges"], []string{"Inventory!A2:G10"}):
			body = fmt.Sprintf(`{"spreadsheetId":"spreadsheet_test","valueRanges":[{"range":"Inventory!A2:G10","majorDimension":"ROWS","values":[["","192.0.2",%q,"node","","",%q]]}]}`, octet, alias)
		default:
			return nil, fmt.Errorf("unexpected pipeline HTTP request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	run := func(want int, diagnostic string) {
		t.Helper()
		var out, stderr bytes.Buffer
		args := []string{"--config", path, "--once"}
		if code := runPipeline(t.Context(), args, &out, &stderr); code != want || !strings.Contains(stderr.String(), diagnostic) {
			t.Fatalf("pipeline --once exit %d: stdout=%q stderr=%q", code, &out, &stderr)
		}
	}
	publications := func() map[string]string {
		t.Helper()
		cfg, err := config.LoadServer(path)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := storage.Load(store, cfg.Policies)
		if err != nil {
			t.Fatal(err)
		}
		versions := make(map[string]string, len(entries))
		for _, entry := range entries {
			versions[entry.Name] = entry.Version
		}
		return versions
	}

	run(0, "")
	first := publications()
	if len(first) != 2 || first["web.example.internal"] == "" || first["node.pvt.example.internal"] == "" {
		t.Fatalf("unexpected initial publications: %v", first)
	}
	initial, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := os.ReadFile(include)
	if err != nil || !bytes.Contains(managed, []byte("2.0.192.in-addr.arpa")) {
		t.Fatalf("reverse zone missing from managed NSD include: %s, %v", managed, err)
	}
	octet, alias = "11", "portal"
	write("reject-zones", nil, 0600)
	run(1, "nsd-checkzone rejected")
	if body, err := os.ReadFile(cache); err != nil || !bytes.Equal(body, initial) {
		t.Fatalf("unconfirmed snapshot replaced cache: %s, %v", body, err)
	}
	if current := publications(); current["web.example.internal"] != first["web.example.internal"] || len(current) != 2 {
		t.Fatalf("unconfirmed snapshot changed publications: %v", current)
	}
	if err := os.Remove(failCheck); err != nil {
		t.Fatal(err)
	}
	run(0, "")
	if current := publications(); len(current) != 2 || current["web.example.internal"] != "" || current["portal.example.internal"] == "" {
		t.Fatalf("confirmed snapshot did not update publications: %v", current)
	}
	zone, err := os.ReadFile(filepath.Join(zones, "current", "example.internal.zone"))
	if err != nil || !bytes.Contains(zone, []byte("192.0.2.11")) || bytes.Contains(zone, []byte("192.0.2.10")) {
		t.Fatalf("wrong active zone: %s, %v", zone, err)
	}
	if entries, err := os.ReadDir(stage); err != nil || len(entries) != 0 {
		t.Fatalf("staged candidates were not cleaned up: %v, %v", entries, err)
	}

	t.Run("watch reports failure and stops during retry delay", func(t *testing.T) {
		write("reject-zones", nil, 0600)
		defer func() {
			if err := os.Remove(failCheck); err != nil {
				t.Error(err)
			}
		}()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		diagnostic := &cancelOnWrite{cancel: cancel}
		if code := runPipeline(ctx, []string{"--config", path, "--watch"}, io.Discard, diagnostic); code != 0 ||
			!strings.Contains(diagnostic.String(), "nsd-checkzone rejected") {
			t.Fatalf("pipeline --watch exit %d: %s", code, diagnostic.String())
		}
	})

	t.Run("server config preflight", func(t *testing.T) {
		var stderr bytes.Buffer
		if code := runServer(t.Context(), []string{"--config", path, "--check-config"}, &stderr); code != 0 ||
			!strings.Contains(stderr.String(), "2 published certificates") {
			t.Fatalf("valid server config exit %d: %s", code, &stderr)
		}
		cfgBody, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bad := write("bad-listener.yaml", bytes.Replace(cfgBody, []byte(listenerKey), []byte(key), 1), 0600)
		stderr.Reset()
		if code := runServer(t.Context(), []string{"--config", bad, "--check-config"}, &stderr); code != 1 ||
			!strings.Contains(stderr.String(), "check server TLS key pair") {
			t.Fatalf("mismatched listener key exit %d: %s", code, &stderr)
		}
	})

	t.Run("manual DNS workflow", func(t *testing.T) {
		var out, diagnostic bytes.Buffer
		runCommand := func(command func([]string) int, args []string, want int, expected string, output *bytes.Buffer) {
			t.Helper()
			out.Reset()
			diagnostic.Reset()
			if code := command(args); code != want || !strings.Contains(output.String(), expected) {
				t.Fatalf("%v exit %d: stdout=%q stderr=%q", args, code, &out, &diagnostic)
			}
		}
		pipeline := func(args []string) int { return runPipeline(t.Context(), args, &out, &diagnostic) }
		fetch := func(args []string) int { return runDNSFetch(t.Context(), args, &out, &diagnostic) }
		runCommand(fetch, []string{"--config", path}, 2, "confirm DNS before updating the cache", &diagnostic)
		runCommand(fetch, []string{"--config", path, "--status"}, 0, `"state":"verified"`, &out)
		cfgBody, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		manual := write("dns-only.yaml", bytes.SplitN(cfgBody, []byte("\ncertificates:"), 2)[0], 0600)
		octet = "12"
		runCommand(pipeline, []string{"--config", manual}, 0, "certpouch-zones-", &out)
		candidate := strings.TrimSpace(out.String())
		diff := func(args []string) int { return runDNSDiff(args, &out, &diagnostic) }
		runCommand(diff, []string{"--candidate", candidate, "--zone-dir", zones}, 0, "192.0.2.12", &out)
		apply := func(args []string) int { return runDNSApply(t.Context(), args, &out, &diagnostic) }
		runCommand(apply, []string{"--candidate", candidate, "--zone-dir", zones,
			"--nsd-config", nsd, "--dns-address", listener, "--max-removed-records", "0"}, 1, "remov", &diagnostic)
		runCommand(pipeline, []string{"--config", manual, "--apply", candidate}, 0, "generation-", &out)
		runCommand(fetch, []string{"--config", manual}, 0, "unchanged", &out)
		if err := os.Remove(cache); err != nil {
			t.Fatal(err)
		}
		runCommand(fetch, []string{"--config", manual, "--status"}, 1, `"state":"missing"`, &out)
	})
}

// Answer SOA requests from the active files to exercise the same TCP confirmation
// the pipeline uses after an NSD reload.
func pipelineSOAServer(t *testing.T, zoneDir string) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			err = answerPipelineSOA(conn, zoneDir)
			conn.Close()
			if err != nil {
				t.Error(err)
			}
		}
	}()
	t.Cleanup(func() {
		l.Close()
		<-done
	})
	return l.Addr().String()
}

func answerPipelineSOA(conn net.Conn, zoneDir string) error {
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	var length uint16
	if err := binary.Read(conn, binary.BigEndian, &length); err != nil {
		return err
	}
	packet := make([]byte, length)
	if _, err := io.ReadFull(conn, packet); err != nil {
		return err
	}
	var query dnsmessage.Message
	if err := query.Unpack(packet); err != nil {
		return err
	}
	if len(query.Questions) != 1 || query.Questions[0].Type != dnsmessage.TypeSOA {
		return fmt.Errorf("unexpected DNS question: %v", query.Questions)
	}
	name := query.Questions[0].Name
	serials, err := dns.ReadManagedSerials(zoneDir)
	if err != nil {
		return err
	}
	serial, ok := serials[strings.TrimSuffix(name.String(), ".")]
	if !ok {
		return fmt.Errorf("no active zone for %s", name)
	}
	query.Response, query.Authoritative = true, true
	query.Answers = []dnsmessage.Resource{{
		Header: dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET, TTL: 300},
		Body:   &dnsmessage.SOAResource{NS: name, MBox: name, Serial: serial},
	}}
	packet, err = query.Pack()
	if err != nil {
		return err
	}
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(packet)))
	_, err = conn.Write(append(frame, packet...))
	return err
}

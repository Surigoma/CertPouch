package main

import (
	"bytes"
	"strings"
	"testing"
)

// Requested help is regular output: stdout and exit 0, so it can be piped.
// Usage printed because a command is missing or wrong is a diagnostic:
// stderr and exit 2.
func TestCommandHelp(t *testing.T) {
	const toStdout, toStderr = true, false
	for _, tc := range []struct {
		args   []string
		code   int
		stdout bool
		want   string
	}{
		{[]string{"--help"}, 0, toStdout, "pipeline --watch"},
		{[]string{"-h"}, 0, toStdout, "pipeline --watch"},
		{[]string{"help"}, 0, toStdout, "certpouch help <command>"},
		{[]string{"help", "pull"}, 0, toStdout, "-dry-run"},
		{[]string{"help", "dns", "sync"}, 0, toStdout, "-serial"},
		{[]string{"help", "cert"}, 0, toStdout, "publish|renew|status"},
		{[]string{"help", "unknown"}, 2, toStderr, "Usage:"},
		{[]string{"configure", "--help"}, 0, toStdout, "dns|server|client"},
		{[]string{"configure"}, 2, toStderr, "dns|server|client"},
		{[]string{"configure", "unknown"}, 2, toStderr, "dns|server|client"},
		{[]string{"dns", "--help"}, 0, toStdout, "fetch|sync|diff|apply"},
		{[]string{"cert", "--help"}, 0, toStdout, "publish|renew|status"},
		{[]string{"init", "client"}, 2, toStderr, "configure server|client"},
		{[]string{"configure", "client", "--help"}, 0, toStdout, "configure client"},
		{[]string{"configure", "dns", "-h"}, 0, toStdout, "-zone"},
		{nil, 2, toStderr, "Usage:"},
		{[]string{"unknown"}, 2, toStderr, "Usage:"},
		{[]string{"cert"}, 2, toStderr, "publish|renew|status"},
		{[]string{"dns", "unknown"}, 2, toStderr, "fetch|sync|diff|apply"},
		{[]string{"pipeline", "--help"}, 0, toStdout, "-check"},
		{[]string{"pipeline", "--config", "server.yaml", "--check", "--once"}, 2, toStderr, "Usage of pipeline"},
		{[]string{"pipeline", "--unknown-flag"}, 2, toStderr, "flag provided but not defined"},
		{[]string{"pull", "--help"}, 0, toStdout, "-dry-run"},
		{[]string{"pull"}, 2, toStderr, "Usage:"},
		{[]string{"status", "--help"}, 0, toStdout, "-config"},
		{[]string{"update", "--help"}, 0, toStdout, "-check"},
		{[]string{"server", "--help"}, 0, toStdout, "-check-config"},
		{[]string{"server"}, 2, toStderr, "--check-config"},
		{[]string{"dns", "sync", "--help"}, 0, toStdout, "-stage-dir"},
		{[]string{"dns", "fetch", "--help"}, 0, toStdout, "-status"},
		{[]string{"dns", "apply", "--help"}, 0, toStdout, "-max-removed-records"},
		{[]string{"dns", "diff", "--help"}, 0, toStdout, "-baseline"},
		{[]string{"cert", "publish", "--help"}, 0, toStdout, "-config"},
		{[]string{"cert", "renew", "--help"}, 0, toStdout, "-dry-run"},
		{[]string{"cert", "status", "--help"}, 0, toStdout, "-config"},
	} {
		var stdout, stderr bytes.Buffer
		code := run(t.Context(), tc.args, &stdout, &stderr)
		want, other := &stderr, &stdout
		if tc.stdout {
			want, other = &stdout, &stderr
		}
		if code != tc.code || !strings.Contains(want.String(), tc.want) || other.Len() != 0 {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want exit %d with %q only on %s",
				tc.args, code, &stdout, &stderr, tc.code, tc.want, map[bool]string{true: "stdout", false: "stderr"}[tc.stdout])
		}
	}
}

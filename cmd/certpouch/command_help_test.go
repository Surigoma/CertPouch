package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandHelp(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"--help"}, 0, "pipeline --watch"},
		{[]string{"configure", "--help"}, 0, "dns|server|client"},
		{[]string{"dns", "--help"}, 0, "fetch|sync|diff|apply"},
		{[]string{"cert", "--help"}, 0, "publish|renew|status"},
		{[]string{"init", "client"}, 2, "configure server|client"},
		{[]string{"configure", "client", "--help"}, 0, "configure client"},
		{nil, 2, "Usage:"},
		{[]string{"unknown"}, 2, "Usage:"},
		{[]string{"cert"}, 2, "Usage:"},
		{[]string{"dns", "unknown"}, 2, "Usage:"},
		{[]string{"pipeline", "--help"}, 0, "-check"},
		{[]string{"pipeline", "--config", "server.yaml", "--check", "--once"}, 2, "Usage of pipeline"},
		{[]string{"pull", "--help"}, 0, "-dry-run"},
		{[]string{"pull"}, 2, "Usage:"},
		{[]string{"status", "--help"}, 0, "-config"},
		{[]string{"update", "--help"}, 0, "-check"},
		{[]string{"server", "--help"}, 0, "-check-config"},
		{[]string{"server"}, 2, "Usage:"},
		{[]string{"dns", "sync", "--help"}, 0, "-stage-dir"},
		{[]string{"dns", "fetch", "--help"}, 0, "-status"},
		{[]string{"dns", "apply", "--help"}, 0, "-max-removed-records"},
		{[]string{"dns", "diff", "--help"}, 0, "-baseline"},
		{[]string{"cert", "publish", "--help"}, 0, "-config"},
		{[]string{"cert", "renew", "--help"}, 0, "-dry-run"},
		{[]string{"cert", "status", "--help"}, 0, "-config"},
	} {
		var diagnostic bytes.Buffer
		if code := run(t.Context(), tc.args, &diagnostic); code != tc.code || !strings.Contains(diagnostic.String(), tc.want) {
			t.Errorf("%v: got exit %d and output %q", tc.args, code, diagnostic.String())
		}
	}
}

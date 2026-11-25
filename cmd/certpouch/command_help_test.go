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
	} {
		var diagnostic bytes.Buffer
		if code := run(t.Context(), tc.args, &diagnostic); code != tc.code || !strings.Contains(diagnostic.String(), tc.want) {
			t.Errorf("%v: got exit %d and output %q", tc.args, code, diagnostic.String())
		}
	}
}

package deployment

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureLinuxFilePermissions(t *testing.T) {
	dest := t.TempDir()
	for _, tc := range []struct{ owner, group, certMode, keyMode, want string }{
		{"certpouch-no-such-test-user", "", "", "", "resolve deployment owner"},
		{"", "certpouch-no-such-test-group", "", "", "resolve deployment group"},
		{"", "", "0664", "", "invalid certificate file mode"},
		{"", "", "", "0644", "invalid certificate file mode"},
		{"", "", "", "0640", "require an explicit deployment group"},
	} {
		t.Run(tc.want+tc.certMode+tc.keyMode, func(t *testing.T) {
			if _, _, err := Configure(dest, tc.owner, tc.group, tc.certMode, tc.keyMode, "", ""); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Configure error = %v, want %q", err, tc.want)
			}
		})
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	group, err := user.LookupGroupId(current.Gid)
	if err != nil {
		t.Skipf("test user's group is unavailable: %v", err)
	}
	opts, _, err := Configure(dest, current.Username, group.Name, "0640", "0640", "", "")
	if err != nil || opts.UID != os.Getuid() || opts.GID != os.Getgid() ||
		opts.CertificateMode != 0640 || opts.PrivateKeyMode != 0640 {
		t.Fatalf("explicit owner/group permissions = %+v, %v", opts, err)
	}
}

func TestConfigureSystemdReportsReloadAndHealthFailures(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	control := filepath.Join(bin, "systemctl")
	for _, tc := range []struct{ action, failAction, want string }{
		{"reload", "reload", "systemctl reload web.service"},
		{"restart", "is-active", "did not confirm web.service is active"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Setenv("CERTPOUCH_TEST_FAIL_ACTION", tc.failAction)
			if err := os.WriteFile(control, []byte("#!/bin/sh\n[ \"$1\" != \"$CERTPOUCH_TEST_FAIL_ACTION\" ]\n"), 0700); err != nil {
				t.Fatal(err)
			}
			_, apply, err := Configure(t.TempDir(), "", "", "", "", "web.service", tc.action)
			if err != nil {
				t.Fatal(err)
			}
			if err := apply(t.Context()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("service activation error = %v, want %q", err, tc.want)
			}
		})
	}
}

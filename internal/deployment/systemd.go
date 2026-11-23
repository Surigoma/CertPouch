package deployment

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"runtime"
	"strconv"
	"time"
)

const SystemdTimeout = 30 * time.Second

var serviceUnit = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]*\.service$`)

func Configure(destination, owner, group, publicMode, keyMode, unit, action string) (Options, func(context.Context) error, error) {
	if runtime.GOOS != "linux" {
		return Options{}, nil, fmt.Errorf("certificate deployment requires Linux and systemd")
	}
	if !serviceUnit.MatchString(unit) || (action != "reload" && action != "restart") {
		return Options{}, nil, fmt.Errorf("configure a valid systemd service unit and reload or restart action")
	}
	uid, gid := -1, -1
	if owner != "" {
		entry, err := user.Lookup(owner)
		if err != nil {
			return Options{}, nil, fmt.Errorf("resolve deployment owner: %w", err)
		}
		uid, err = strconv.Atoi(entry.Uid)
		if err != nil || uid < 0 {
			return Options{}, nil, fmt.Errorf("invalid deployment owner ID")
		}
	}
	if group != "" {
		entry, err := user.LookupGroup(group)
		if err != nil {
			return Options{}, nil, fmt.Errorf("resolve deployment group: %w", err)
		}
		gid, err = strconv.Atoi(entry.Gid)
		if err != nil || gid < 0 {
			return Options{}, nil, fmt.Errorf("invalid deployment group ID")
		}
	}
	certMode, err := parseMode(publicMode, 0644, 0644)
	if err != nil {
		return Options{}, nil, err
	}
	privateMode, err := parseMode(keyMode, 0600, 0640)
	if err != nil {
		return Options{}, nil, err
	}
	if privateMode&0040 != 0 && gid < 0 {
		return Options{}, nil, fmt.Errorf("group-readable private keys require an explicit deployment group")
	}
	options := Options{Destination: destination, UID: uid, GID: gid, CertificateMode: certMode, PrivateKeyMode: privateMode}
	apply := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, SystemdTimeout)
		defer cancel()
		if err := exec.CommandContext(ctx, "systemctl", action, "--", unit).Run(); err != nil {
			return fmt.Errorf("systemctl %s %s: %w", action, unit, err)
		}
		if err := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "--", unit).Run(); err != nil {
			return fmt.Errorf("systemd did not confirm %s is active: %w", unit, err)
		}
		return nil
	}
	return options, apply, nil
}

func parseMode(value string, fallback, allowed os.FileMode) (os.FileMode, error) {
	if value == "" {
		return fallback, nil
	}
	octal, err := strconv.ParseUint(value, 8, 12)
	mode := os.FileMode(octal)
	if err != nil || mode&^allowed != 0 || mode&0600 != 0600 {
		return 0, fmt.Errorf("invalid certificate file mode")
	}
	return mode, nil
}

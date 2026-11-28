package dns

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// RecoverZones resolves an interrupted transaction before rendering new serials.
func RecoverZones(ctx context.Context, root string, control ZoneControl,
	reconfigure func(context.Context, []string) error) (err error) {
	if !filepath.IsAbs(root) || filepath.Dir(filepath.Clean(root)) == filepath.Clean(root) {
		return fmt.Errorf("DNS recovery requires an absolute, non-root managed directory")
	}
	if err := managedDirectory(root); err != nil {
		return err
	}
	lock, guard, err := acquireActivationLock(root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, guard.Close()) }()
	return recoverZones(ctx, root, lock, control, reconfigure)
}

func recordDNSChange(lock, name string, targets ...string) error {
	pending := filepath.Join(lock, "pending")
	if err := writeZoneFile(pending, []byte(strings.Join(targets, "\n")+"\n"), 0600); err != nil {
		return err
	}
	if err := os.Rename(pending, filepath.Join(lock, name)); err != nil {
		return err
	}
	return syncDirectory(lock)
}

func finishDNSChange(lock, name string) error {
	if err := os.Remove(filepath.Join(lock, name)); err != nil {
		return err
	}
	return syncDirectory(lock)
}

func readDNSChange(lock, name string, count int, allowMissingOld bool) ([]string, error) {
	path := filepath.Join(lock, name)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 256 {
		return nil, fmt.Errorf("invalid DNS recovery record %s", name)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	body, complete := strings.CutSuffix(string(raw), "\n")
	targets := strings.Split(body, "\n")
	if !complete || len(targets) != count {
		return nil, fmt.Errorf("invalid DNS recovery record %s", name)
	}
	for i, target := range targets {
		if i == 0 && allowMissingOld && target == "" {
			continue
		}
		if !generationName.MatchString(target) || slices.Contains(targets[:i], target) {
			return nil, fmt.Errorf("invalid generation in DNS recovery record %s", name)
		}
	}
	return targets, nil
}

func recoverZones(ctx context.Context, root, lock string, control ZoneControl,
	reconfigure func(context.Context, []string) error) error {
	regular, err := readDNSChange(lock, "recovery", 3, false)
	if err != nil {
		return err
	}
	startup, err := readDNSChange(lock, "startup-recovery", 2, true)
	if err != nil {
		return err
	}
	if startup != nil && (regular != nil || reconfigure == nil) {
		return fmt.Errorf("startup DNS recovery requires the managed NSD include and a single recovery record")
	}
	// These files precede an atomic rename and can remain after interruption.
	for _, temp := range []string{"pending", "next"} {
		if err := removeIfExists(filepath.Join(lock, temp)); err != nil {
			return err
		}
	}
	targets := regular
	if startup != nil {
		targets = startup
	}
	if targets == nil {
		return nil
	}
	current, err := os.Readlink(filepath.Join(root, "current"))
	if (err != nil && !os.IsNotExist(err)) || !slices.Contains(targets, current) {
		return fmt.Errorf("current DNS generation is outside the recovery record")
	}
	var old []ZoneSnapshot
	if targets[0] != "" {
		old, err = readSnapshot(filepath.Join(root, targets[0]))
		if err != nil {
			return err
		}
	}
	if startup != nil {
		if err := restoreStartupZones(ctx, root, lock, targets[0], old, control, reconfigure); err != nil {
			return fmt.Errorf("recover startup DNS zones: %w", err)
		}
		return finishDNSChange(lock, "startup-recovery")
	}
	if err := recoverActivation(ctx, root, lock, current, targets, old, control); err != nil {
		return fmt.Errorf("recover DNS activation: %w", err)
	}
	return finishDNSChange(lock, "recovery")
}

func recoverActivation(ctx context.Context, root, lock, current string, targets []string, old []ZoneSnapshot, control ZoneControl) error {
	if current == targets[0] && confirmServed(ctx, control, old) == nil {
		return nil
	}
	next, err := readSnapshot(filepath.Join(root, targets[1]))
	if err != nil {
		return err
	}
	changed, expected, err := planActivation(old, next)
	if err != nil {
		return err
	}
	rollback, err := readSnapshot(filepath.Join(root, targets[2]))
	if err != nil {
		return err
	}
	if !slices.EqualFunc(rollback, expected, func(a, b ZoneSnapshot) bool {
		return a.name == b.name && a.serial == b.serial && sameZoneContent(a, b)
	}) {
		return fmt.Errorf("rollback snapshot does not match the interrupted activation")
	}
	for i, zone := range old {
		served, err := control.ServedSerial(ctx, zone.name)
		if err != nil {
			return err
		}
		if served != zone.serial && served != next[i].serial && served != rollback[i].serial {
			return fmt.Errorf("zone %s serves serial %d outside the recovery record", zone.name, served)
		}
	}
	if err := switchSnapshot(root, lock, targets[2]); err != nil {
		return err
	}
	return reloadZones(ctx, control, changed, rollback)
}

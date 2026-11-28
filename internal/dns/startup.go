package dns

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// PrepareZones changes managed zone membership at startup. Existing members
// retain their served contents until ordinary activation advances their serials.
func PrepareZones(ctx context.Context, root, candidate string, control ZoneControl,
	reconfigure func(context.Context, []string) error) (err error) {
	if !filepath.IsAbs(root) || filepath.Dir(filepath.Clean(root)) == filepath.Clean(root) {
		return fmt.Errorf("startup requires an absolute, non-root managed directory")
	}
	if err := managedDirectory(root); err != nil {
		return err
	}
	lock, guard, err := acquireActivationLock(root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, guard.Close()) }()
	if err := recoverZones(ctx, root, lock, control, reconfigure); err != nil {
		return err
	}
	next, err := readSnapshot(candidate)
	if err != nil {
		return err
	}
	current := filepath.Join(root, "current")
	oldTarget, err := os.Readlink(current)
	var old []ZoneSnapshot
	if err == nil {
		old, err = readManagedSnapshot(root)
		if err != nil {
			return err
		}
		if err := confirmServed(ctx, control, old); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if slices.Equal(zoneNames(old), zoneNames(next)) {
		return nil
	}
	for i := range next {
		for _, prev := range old {
			if prev.name == next[i].name {
				next[i] = prev
				break
			}
		}
	}
	target, err := saveSnapshot(ctx, root, next, control)
	if err != nil {
		return err
	}
	if err := recordDNSChange(lock, "startup-recovery", oldTarget, target); err != nil {
		return err
	}
	err = switchSnapshot(root, lock, target)
	if err == nil {
		err = reconfigure(ctx, zoneNames(next))
	}
	if err == nil {
		err = reloadZones(ctx, control, nil, next)
	}
	if err != nil {
		restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if restoreErr := restoreStartupZones(restore, root, lock, oldTarget, old, control, reconfigure); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore startup zones: %w", restoreErr))
		}
	}
	return errors.Join(err, finishDNSChange(lock, "startup-recovery"))
}

func zoneNames(zones []ZoneSnapshot) []string {
	list := make([]string, 0, len(zones))
	for _, zone := range zones {
		list = append(list, zone.name)
	}
	return list
}

func restoreStartupZones(ctx context.Context, root, lock, target string, zones []ZoneSnapshot, control ZoneControl,
	reconfigure func(context.Context, []string) error) error {
	if target == "" {
		if err := removeIfExists(filepath.Join(root, "current")); err != nil {
			return err
		}
		if err := syncDirectory(root); err != nil {
			return err
		}
	} else if err := switchSnapshot(root, lock, target); err != nil {
		return err
	}
	names := zoneNames(zones)
	if err := reconfigure(ctx, names); err != nil {
		return err
	}
	return reloadZones(ctx, control, names, zones)
}

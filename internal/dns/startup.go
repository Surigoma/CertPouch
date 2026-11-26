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
	lock, err := acquireActivationLock(root)
	if err != nil {
		return err
	}
	keepLock := false
	defer func() {
		if !keepLock {
			err = errors.Join(err, releaseActivationLock(root, lock))
		}
	}()
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
	names := func(zones []ZoneSnapshot) []string {
		list := make([]string, 0, len(zones))
		for _, zone := range zones {
			list = append(list, zone.name)
		}
		return list
	}
	if slices.Equal(names(old), names(next)) {
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
	recovery := filepath.Join(lock, "startup-recovery")
	if err := writeZoneFile(recovery, []byte(oldTarget+"\n"+target+"\n"), 0600); err != nil {
		return errors.Join(err, removeIfExists(recovery))
	}
	if err := syncDirectory(lock); err != nil {
		return errors.Join(err, removeIfExists(recovery))
	}
	keepLock = true
	err = switchSnapshot(root, lock, target)
	if err == nil {
		err = reconfigure(ctx, names(next))
	}
	if err == nil {
		err = reloadZones(ctx, control, nil, next)
	}
	if err != nil {
		restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		var restoreErr error
		if oldTarget == "" {
			restoreErr = removeIfExists(current)
		} else {
			restoreErr = switchSnapshot(root, lock, oldTarget)
		}
		if restoreErr == nil {
			restoreErr = reconfigure(restore, names(old))
		}
		if restoreErr == nil {
			restoreErr = reloadZones(restore, control, nil, old)
		}
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore startup zones: %w", restoreErr))
		}
	}
	if cleanup := os.Remove(recovery); cleanup != nil {
		return errors.Join(err, cleanup)
	}
	keepLock = false
	return err
}

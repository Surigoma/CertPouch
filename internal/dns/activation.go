package dns

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ZoneControl connects candidate validation and NSD control without coupling
// snapshot management to an external process.
type ZoneControl interface {
	Check(context.Context, string, string) error
	Reload(context.Context, string) error
	ServedSerial(context.Context, string) (uint32, error)
}

type ActivationOptions struct {
	Initialize        bool
	MaxRemovedRecords *int
}

type ZoneSnapshot struct {
	name       string
	body       []byte
	serial     uint32
	start, end int
}

var serialLine = regexp.MustCompile(`(?m)^@ IN SOA [^ \n]+ [^ \n]+ \( ([0-9]{1,10}) [0-9]+ [0-9]+ [0-9]+ [0-9]+ \)$`)
var generationName = regexp.MustCompile(`^generation-[0-9]+$`)

// ReadManagedSerials snapshots the SOA serials from the managed current link.
// Activation separately verifies them against the authoritative listener.
func ReadManagedSerials(directory string) (map[string]uint32, error) {
	zones, err := readManagedSnapshot(directory)
	if err != nil {
		return nil, err
	}
	return snapshotSerials(zones), nil
}

// ReadCandidateSerials reads SOA serials from a staged snapshot.
func ReadCandidateSerials(directory string) (map[string]uint32, error) {
	zones, err := readSnapshot(directory)
	if err != nil {
		return nil, err
	}
	return snapshotSerials(zones), nil
}

func snapshotSerials(zones []ZoneSnapshot) map[string]uint32 {
	serials := make(map[string]uint32, len(zones))
	for _, zone := range zones {
		serials[zone.name] = zone.serial
	}
	return serials
}

func readManagedSnapshot(directory string) ([]ZoneSnapshot, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("managed DNS directory must be absolute")
	}
	if err := managedDirectory(directory); err != nil {
		return nil, err
	}
	current, err := os.Readlink(filepath.Join(directory, "current"))
	if err != nil || !generationName.MatchString(current) {
		return nil, fmt.Errorf("current must reference a managed DNS snapshot")
	}
	return readSnapshot(filepath.Join(directory, current))
}

// Activate installs a coordinated set of already rendered zone candidates.
// Initialization prepares an empty managed directory without contacting NSD.
func Activate(ctx context.Context, root, candidate string, options ActivationOptions, control ZoneControl) (result string, err error) {
	if runtime.GOOS != "linux" || !filepath.IsAbs(root) || !filepath.IsAbs(candidate) ||
		filepath.Dir(filepath.Clean(root)) == filepath.Clean(root) {
		return "", fmt.Errorf("DNS activation requires Linux and absolute, non-root managed paths")
	}
	if limit := options.MaxRemovedRecords; limit != nil && (*limit < 0 || options.Initialize) {
		return "", fmt.Errorf("DNS removal limit requires a non-negative count and an existing snapshot")
	}
	if err := managedDirectory(root); err != nil {
		return "", err
	}
	lock, err := acquireActivationLock(root)
	if err != nil {
		return "", err
	}
	preserve := false
	defer func() {
		if !preserve {
			err = errors.Join(err, releaseActivationLock(root, lock))
		}
	}()
	newZones, err := readSnapshot(candidate)
	if err != nil {
		return "", fmt.Errorf("read candidate snapshot: %w", err)
	}
	current := filepath.Join(root, "current")
	oldTarget, err := os.Readlink(current)
	if options.Initialize {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("initialize requires an absent current link")
		}
		snapshot, err := saveSnapshot(ctx, root, newZones, control)
		if err != nil {
			return "", err
		}
		return snapshot, switchSnapshot(root, lock, snapshot)
	}
	if err != nil || !generationName.MatchString(oldTarget) {
		return "", fmt.Errorf("current must reference a managed DNS snapshot")
	}
	oldZones, err := readSnapshot(filepath.Join(root, oldTarget))
	if err != nil {
		return "", fmt.Errorf("read active snapshot: %w", err)
	}
	changed, rollback, err := planActivation(oldZones, newZones)
	if err != nil {
		return "", err
	}
	if limit := options.MaxRemovedRecords; limit != nil {
		removed := 0
		for i, previous := range oldZones {
			if !sameZoneContent(previous, newZones[i]) {
				removed += removedRecordCount(previous, newZones[i])
			}
		}
		if removed > *limit {
			return "", fmt.Errorf("candidate removes or replaces %d DNS records, exceeding the limit of %d", removed, *limit)
		}
	}
	if err := confirmServed(ctx, control, oldZones); err != nil {
		return "", fmt.Errorf("NSD does not match the active snapshot: %w", err)
	}
	if len(changed) == 0 {
		return "unchanged", nil
	}
	snapshot, err := saveSnapshot(ctx, root, newZones, control)
	if err != nil {
		return "", err
	}
	undo, err := saveSnapshot(ctx, root, rollback, control)
	if err != nil {
		return "", fmt.Errorf("prepare rollback snapshot: %w", err)
	}
	record := filepath.Join(lock, "recovery")
	if err := writeZoneFile(record, []byte(oldTarget+"\n"+snapshot+"\n"+undo+"\n"), 0600); err != nil {
		return "", errors.Join(err, removeIfExists(record))
	}
	if err := syncDirectory(lock); err != nil {
		return "", errors.Join(err, removeIfExists(record))
	}
	preserve = true
	err = switchSnapshot(root, lock, snapshot)
	if err == nil {
		err = reloadZones(ctx, control, changed, newZones)
	}
	if err != nil {
		restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		if rollbackErr := switchSnapshot(root, lock, undo); rollbackErr != nil {
			return "", errors.Join(err, fmt.Errorf("switch to rollback snapshot: %w", rollbackErr))
		}
		if rollbackErr := reloadZones(restore, control, changed, rollback); rollbackErr != nil {
			return "", errors.Join(err, fmt.Errorf("reload rollback snapshot: %w", rollbackErr))
		}
		err = fmt.Errorf("activation failed; previous records restored with newer SOA serials: %w", err)
	}
	if cleanup := os.Remove(record); cleanup != nil {
		return "", errors.Join(err, cleanup)
	}
	preserve = false
	return snapshot, err
}

func acquireActivationLock(root string) (string, error) {
	lock := filepath.Join(root, ".dns-apply-lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		return "", fmt.Errorf("acquire DNS activation lock: %w", err)
	}
	return lock, nil
}

func releaseActivationLock(root, lock string) error {
	return errors.Join(os.Remove(lock), syncDirectory(root))
}

func readSnapshot(dir string) ([]ZoneSnapshot, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("zone snapshot directory must be absolute")
	}
	if err := managedDirectory(dir); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	folder, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer folder.Close()
	entries, err := folder.ReadDir(1025)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) == 0 || len(entries) > 1024 {
		return nil, fmt.Errorf("invalid number of zone files")
	}
	remain := int64(32 << 20)
	zones := make([]ZoneSnapshot, 0, len(entries))
	for _, entry := range entries {
		name, suffix := strings.CutSuffix(entry.Name(), ".zone")
		normalized, ok := hostname(name)
		if !suffix || !ok || normalized != name || !entry.Type().IsRegular() {
			return nil, fmt.Errorf("unexpected snapshot entry")
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm()&0022 != 0 {
			return nil, fmt.Errorf("zone file must not be writable by other users")
		}
		file, err := root.Open(entry.Name())
		if err != nil {
			return nil, err
		}
		opened, statErr := file.Stat()
		if statErr != nil || !os.SameFile(info, opened) {
			file.Close()
			return nil, fmt.Errorf("zone file changed while opening")
		}
		body, err := io.ReadAll(io.LimitReader(file, remain+1))
		if err := errors.Join(err, file.Close()); err != nil {
			return nil, err
		}
		remain -= int64(len(body))
		if remain < 0 {
			return nil, fmt.Errorf("zone snapshot exceeds size limit")
		}
		zone, err := parseSnapshotZone(name, body)
		if err != nil {
			return nil, err
		}
		zones = append(zones, zone)
	}
	slices.SortFunc(zones, func(a, b ZoneSnapshot) int { return strings.Compare(a.name, b.name) })
	return zones, nil
}

func parseSnapshotZone(name string, body []byte) (ZoneSnapshot, error) {
	matches := serialLine.FindAllSubmatchIndex(body, 2)
	// Generated files are self-contained. Includes and other directives could
	// change the records independently of the managed snapshot.
	lines := bytes.Split(body, []byte("\n"))
	if len(lines) < 4 || string(lines[0]) != "$ORIGIN "+name+"." ||
		!bytes.HasPrefix(lines[1], []byte("$TTL ")) || len(matches) != 1 ||
		!serialLine.Match(lines[2]) || bytes.ContainsAny(bytes.Join(lines[2:], nil), "$\\") {
		return ZoneSnapshot{}, fmt.Errorf("zone %s is not in the generated snapshot format", name)
	}
	start, end := matches[0][2], matches[0][3]
	serial, err := strconv.ParseUint(string(body[start:end]), 10, 32)
	if err != nil {
		return ZoneSnapshot{}, fmt.Errorf("invalid SOA serial for zone %s", name)
	}
	return ZoneSnapshot{name: name, body: body, serial: uint32(serial), start: start, end: end}, nil
}

func planActivation(old, next []ZoneSnapshot) ([]string, []ZoneSnapshot, error) {
	if len(old) != len(next) {
		return nil, nil, fmt.Errorf("managed zone set changed; reconfigure NSD separately")
	}
	var changed []string
	rollback := slices.Clone(old)
	for i, candidate := range next {
		previous := old[i]
		if candidate.name != previous.name {
			return nil, nil, fmt.Errorf("managed zone set changed; reconfigure NSD separately")
		}
		if sameZoneContent(candidate, previous) {
			next[i] = previous
			continue
		}
		if !newerSerial(candidate.serial, previous.serial) || !newerSerial(candidate.serial+1, previous.serial) {
			return nil, nil, fmt.Errorf("zone %s needs a newer serial with room for rollback", candidate.name)
		}
		changed = append(changed, candidate.name)
		body := append([]byte{}, previous.body[:previous.start]...)
		body = strconv.AppendUint(body, uint64(candidate.serial+1), 10)
		body = append(body, previous.body[previous.end:]...)
		var err error
		rollback[i], err = parseSnapshotZone(candidate.name, body)
		if err != nil {
			return nil, nil, err
		}
	}
	return changed, rollback, nil
}

func saveSnapshot(ctx context.Context, root string, zones []ZoneSnapshot, control ZoneControl) (string, error) {
	dir, err := os.MkdirTemp(root, "generation-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			for _, z := range zones {
				_ = os.Remove(filepath.Join(dir, z.name+".zone"))
			}
			_ = os.Remove(dir)
		}
	}()
	for _, zone := range zones {
		path := filepath.Join(dir, zone.name+".zone")
		if err := writeZoneFile(path, zone.body, 0640); err != nil {
			return "", err
		}
		if err := control.Check(ctx, zone.name, path); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(dir, 0750); err != nil {
		return "", err
	}
	if err := errors.Join(syncDirectory(dir), syncDirectory(root)); err != nil {
		return "", err
	}
	ok = true
	return filepath.Base(dir), nil
}

func confirmServed(ctx context.Context, control ZoneControl, zones []ZoneSnapshot) error {
	for _, zone := range zones {
		serial, err := control.ServedSerial(ctx, zone.name)
		if err != nil {
			return err
		}
		if serial != zone.serial {
			return fmt.Errorf("zone %s serves serial %d, expected %d", zone.name, serial, zone.serial)
		}
	}
	return nil
}

func reloadZones(ctx context.Context, control ZoneControl, changed []string, expected []ZoneSnapshot) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	for _, name := range changed {
		if err := control.Reload(ctx, name); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := confirmServed(ctx, control, expected)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

func switchSnapshot(root, lock, target string) error {
	next := filepath.Join(lock, "next")
	if err := os.Symlink(target, next); err != nil {
		return err
	}
	if err := os.Rename(next, filepath.Join(root, "current")); err != nil {
		return errors.Join(err, os.Remove(next))
	}
	return syncDirectory(root)
}

func managedDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("DNS snapshot directory must be a regular directory writable only by its owner")
	}
	return nil
}

func writeZoneFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

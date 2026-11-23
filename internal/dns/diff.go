package dns

import (
	"bytes"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

type ZoneChange struct {
	Name            string   `json:"name"`
	State           string   `json:"state"`
	CurrentSerial   *uint32  `json:"current_serial,omitempty"`
	CandidateSerial *uint32  `json:"candidate_serial,omitempty"`
	Added           []string `json:"added,omitempty"`
	Removed         []string `json:"removed,omitempty"`
}

// DiffSnapshots compares a local candidate to current without querying NSD.
func DiffSnapshots(directory, candidate string) ([]ZoneChange, error) {
	old, err := readManagedSnapshot(directory)
	if err != nil {
		return nil, fmt.Errorf("read current snapshot: %w", err)
	}
	return diffCandidate(old, candidate)
}

// DiffCandidates compares two staged snapshots before either one is managed.
func DiffCandidates(baseline, candidate string) ([]ZoneChange, error) {
	old, err := readSnapshot(baseline)
	if err != nil {
		return nil, fmt.Errorf("read baseline snapshot: %w", err)
	}
	return diffCandidate(old, candidate)
}

func diffCandidate(old []ZoneSnapshot, candidate string) ([]ZoneChange, error) {
	if !filepath.IsAbs(candidate) {
		return nil, fmt.Errorf("candidate directory must be absolute")
	}
	next, err := readSnapshot(candidate)
	if err != nil {
		return nil, fmt.Errorf("read candidate snapshot: %w", err)
	}
	type pair struct {
		current, candidate ZoneSnapshot
	}
	byName := make(map[string]pair)
	for _, zone := range old {
		byName[zone.name] = pair{current: zone}
	}
	for _, zone := range next {
		value := byName[zone.name]
		value.candidate = zone
		byName[zone.name] = value
	}
	result := make([]ZoneChange, 0, len(byName))
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		change := ZoneChange{Name: name, State: "unchanged"}
		p := byName[name]
		if p.current.name != "" {
			change.CurrentSerial = &p.current.serial
		}
		if p.candidate.name != "" {
			change.CandidateSerial = &p.candidate.serial
		}
		switch {
		case p.current.name == "":
			change.State = "added"
		case p.candidate.name == "":
			change.State = "removed"
		case !sameZoneContent(p.current, p.candidate):
			change.State = "changed"
		case p.current.serial != p.candidate.serial:
			change.State = "serial_only"
		}
		if change.State == "changed" || change.State == "added" || change.State == "removed" {
			change.Added, change.Removed = changedLines(p.current, p.candidate)
		}
		result = append(result, change)
	}
	return result, nil
}

func sameZoneContent(a, b ZoneSnapshot) bool {
	return bytes.Equal(a.body[:a.start], b.body[:b.start]) && bytes.Equal(a.body[a.end:], b.body[b.end:])
}

// removedRecordCount compares generated record lines after the origin, TTL and SOA.
func removedRecordCount(current, candidate ZoneSnapshot) int {
	records := func(zone ZoneSnapshot) string {
		_, lines, _ := strings.Cut(string(zone.body[zone.end:]), "\n")
		return lines
	}
	next := make(map[string]int)
	for line := range strings.SplitSeq(records(candidate), "\n") {
		next[line]++
	}
	removed := 0
	for line := range strings.SplitSeq(records(current), "\n") {
		if line == "" {
			continue
		}
		if next[line] > 0 {
			next[line]--
		} else {
			removed++
		}
	}
	return removed
}

func changedLines(current, candidate ZoneSnapshot) (added, removed []string) {
	lines := func(zone ZoneSnapshot) map[string]int {
		counts := make(map[string]int)
		if zone.name == "" {
			return counts
		}
		body := string(zone.body[:zone.start]) + "<serial>" + string(zone.body[zone.end:])
		for line := range strings.SplitSeq(body, "\n") {
			if line != "" {
				counts[line]++
			}
		}
		return counts
	}
	old, next := lines(current), lines(candidate)
	for line, count := range next {
		for range count - old[line] {
			added = append(added, line)
		}
	}
	for line, count := range old {
		for range count - next[line] {
			removed = append(removed, line)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	return added, removed
}

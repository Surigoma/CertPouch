package dns

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// StaticRecord adds an operator-defined record to an existing candidate zone.
type StaticRecord struct {
	Zone, Owner, Type, Value string
	Priority                 *uint16
}

func appendStaticRecords(sets map[string][]zoneRecord, forward string, records []StaticRecord) error {
	for i, record := range records {
		zone := forward
		if record.Zone != "" {
			var ok bool
			zone, ok = hostname(record.Zone)
			if !ok {
				return fmt.Errorf("static_records[%d] has an invalid zone", i)
			}
		}
		if _, exists := sets[zone]; !exists {
			return fmt.Errorf("static_records[%d] targets a zone outside the candidate set", i)
		}
		kind := strings.ToUpper(record.Type)
		owner := zone
		if record.Owner != "@" {
			var ok bool
			owner, ok = recordOwner(record.Owner, kind == "TXT")
			if !ok || (owner != zone && !strings.HasSuffix(owner, "."+zone)) {
				return fmt.Errorf("static_records[%d] requires an absolute owner in its zone", i)
			}
		}
		if kind == "CNAME" && owner == zone {
			return fmt.Errorf("static_records[%d] cannot place a CNAME at the zone apex", i)
		}
		value, err := staticValue(record, kind)
		if err != nil {
			return fmt.Errorf("static_records[%d]: %w", i, err)
		}
		sets[zone] = append(sets[zone], zoneRecord{owner + ".", kind, value})
	}
	for _, entries := range sets {
		if err := checkAliases(entries); err != nil {
			return err
		}
	}
	return nil
}

func recordOwner(value string, allowUnderscore bool) (string, bool) {
	normalized := strings.ToLower(strings.TrimSuffix(value, "."))
	validated := normalized
	if allowUnderscore {
		validated = strings.ReplaceAll(validated, "_", "x")
	}
	_, ok := hostname(validated)
	return normalized, ok && strings.TrimSpace(value) == value && strings.Contains(normalized, ".") && !strings.HasSuffix(normalized, ".")
}

func staticValue(record StaticRecord, kind string) (string, error) {
	if kind != "MX" && record.Priority != nil {
		return "", fmt.Errorf("only MX accepts a priority")
	}
	switch kind {
	case "A", "AAAA":
		addr, err := netip.ParseAddr(record.Value)
		if err != nil || addr.Zone() != "" || addr.Is4In6() || addr.Is4() != (kind == "A") {
			return "", fmt.Errorf("invalid %s address", kind)
		}
		return addr.String(), nil
	case "CNAME", "MX", "PTR":
		target, ok := recordOwner(record.Value, false)
		if !ok {
			return "", fmt.Errorf("invalid target name")
		}
		if kind == "MX" {
			if record.Priority == nil {
				return "", fmt.Errorf("MX requires a priority")
			}
			return strconv.FormatUint(uint64(*record.Priority), 10) + " " + target + ".", nil
		}
		return target + ".", nil
	case "TXT":
		if len(record.Value) > 255 || strings.ContainsAny(record.Value, "\\\"$") {
			return "", fmt.Errorf("TXT requires at most 255 printable ASCII bytes without quote, backslash or dollar sign")
		}
		for _, c := range []byte(record.Value) {
			if c < ' ' || c > '~' {
				return "", fmt.Errorf("TXT contains a non-printable byte")
			}
		}
		return "\"" + record.Value + "\"", nil
	default:
		return "", fmt.Errorf("unsupported static record type")
	}
}

func checkAliases(records []zoneRecord) error {
	aliases := make(map[string]string)
	other := make(map[string]bool)
	for _, record := range records {
		if record.kind != "CNAME" {
			other[record.owner] = true
			continue
		}
		if target, exists := aliases[record.owner]; exists && target != record.value {
			return fmt.Errorf("CNAME has multiple targets for %s", record.owner)
		}
		aliases[record.owner] = record.value
	}
	checked := make(map[string]bool)
	for name := range aliases {
		if other[name] {
			return fmt.Errorf("CNAME conflicts with another record at %s", name)
		}
		chain := make(map[string]bool)
		for node := name; aliases[node] != "" && !checked[node]; node = aliases[node] {
			if chain[node] {
				return fmt.Errorf("CNAME cycle involving %s", name)
			}
			chain[node] = true
		}
		for node := range chain {
			checked[node] = true
		}
	}
	return nil
}

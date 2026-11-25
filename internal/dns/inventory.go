// Package dns models the DNS inventory independently of its source.
package dns

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Record represents an inventory row with an absolute owner name.
type Record struct {
	Name  string
	Type  string
	Value string
}

// Inventory is a snapshot of host addresses declared in the DNS inventory.
type Inventory struct {
	addresses map[string][]netip.Addr
}

func (inventory Inventory) HostCount() int {
	return len(inventory.addresses)
}

func (inventory Inventory) Names() []string {
	names := make([]string, 0, len(inventory.addresses))
	for name := range inventory.addresses {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func NewInventory(records []Record) (Inventory, error) {
	addresses := make(map[string][]netip.Addr)
	for i, record := range records {
		recordType := strings.ToUpper(strings.TrimSpace(record.Type))
		if recordType != "A" && recordType != "AAAA" {
			continue
		}
		name, ok := hostname(record.Name)
		addr, err := netip.ParseAddr(strings.TrimSpace(record.Value))
		if !ok || err != nil || addr.Zone() != "" || addr.Is4In6() ||
			(recordType == "A") != addr.Is4() {
			return Inventory{}, fmt.Errorf("invalid address record at inventory row %d", i+1)
		}
		addresses[name] = append(addresses[name], addr)
	}
	return Inventory{addresses: addresses}, nil
}

// AllowedPrefixes returns exact host prefixes for the explicitly named hosts.
// A missing host rejects the entire assignment.
func (inventory Inventory) AllowedPrefixes(hosts []string) ([]netip.Prefix, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("at least one inventory hostname is required")
	}
	seen := make(map[netip.Addr]bool)
	var addresses []netip.Addr
	for _, raw := range hosts {
		name, ok := hostname(raw)
		if !ok {
			return nil, fmt.Errorf("invalid allowed inventory hostname")
		}
		ips := inventory.addresses[name]
		if len(ips) == 0 {
			return nil, fmt.Errorf("allowed inventory host %s has no A or AAAA records", name)
		}
		for _, addr := range ips {
			if !seen[addr] {
				seen[addr] = true
				addresses = append(addresses, addr)
			}
		}
	}
	slices.SortFunc(addresses, netip.Addr.Compare)
	prefixes := make([]netip.Prefix, 0, len(addresses))
	for _, addr := range addresses {
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

// PrefixesForName returns the addresses registered for a single exact DNS name.
// A certificate whose name has no address record has no default access.
func (inventory Inventory) PrefixesForName(name string) []netip.Prefix {
	prefixes, _ := inventory.AllowedPrefixes([]string{name})
	return prefixes
}

func hostname(raw string) (string, bool) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if len(name) > 253 || !strings.Contains(name, ".") {
		return "", false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return "", false
	}
	for label := range strings.SplitSeq(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", false
			}
		}
	}
	return name, true
}

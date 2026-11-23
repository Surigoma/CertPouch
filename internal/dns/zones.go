package dns

import (
	"bytes"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// ZoneOptions describes the authoritative zone and the common SOA used for
// forward records and generated IPv4 /24 reverse zones.
type ZoneOptions struct {
	Forward                                          string
	Primary                                          string
	Mailbox                                          string
	Nameservers                                      []string
	TTL, Serial, Refresh, Retry, Expire, NegativeTTL uint32
	PreviousSerials                                  map[string]uint32
	AdvanceSerial                                    bool
	StaticRecords                                    []StaticRecord
}

type Zone struct {
	Name     string
	Contents []byte
}

type zoneRecord struct {
	owner, kind, value string
}

func RenderZones(inventory Inventory, options ZoneOptions) ([]Zone, error) {
	forward, ok := hostname(options.Forward)
	if !ok || strings.HasSuffix(forward, ".in-addr.arpa") || strings.HasSuffix(forward, ".ip6.arpa") {
		return nil, fmt.Errorf("invalid forward zone")
	}
	primary, primaryOK := hostname(options.Primary)
	mailbox, mailboxOK := hostname(options.Mailbox)
	if !primaryOK || !mailboxOK || options.TTL == 0 || options.TTL > 2147483647 ||
		options.Refresh == 0 || options.Retry == 0 || options.Expire <= options.Refresh ||
		options.NegativeTTL == 0 || options.NegativeTTL > 2147483647 {
		return nil, fmt.Errorf("invalid zone TTL or SOA settings")
	}
	var nameservers []string
	for _, raw := range options.Nameservers {
		ns, ok := hostname(raw)
		if !ok {
			return nil, fmt.Errorf("invalid nameserver")
		}
		nameservers = append(nameservers, ns)
	}
	slices.Sort(nameservers)
	nameservers = slices.Compact(nameservers)
	if !slices.Contains(nameservers, primary) {
		return nil, fmt.Errorf("nameservers must include the SOA primary")
	}
	if len(inventory.addresses) == 0 {
		return nil, fmt.Errorf("refusing to generate zones from an empty inventory")
	}
	sets := map[string][]zoneRecord{forward: nil}
	for name, addresses := range inventory.addresses {
		if name != forward && !strings.HasSuffix(name, "."+forward) {
			return nil, fmt.Errorf("inventory owner %s is outside the forward zone", name)
		}
		for _, addr := range addresses {
			kind := "AAAA"
			if addr.Is4() {
				kind = "A"
				reverse, ptrOwner := reverseOwner(addr)
				sets[reverse] = append(sets[reverse], zoneRecord{ptrOwner, "PTR", name + "."})
			}
			sets[forward] = append(sets[forward], zoneRecord{name + ".", kind, addr.String()})
		}
	}
	if err := appendStaticRecords(sets, forward, options.StaticRecords); err != nil {
		return nil, err
	}
	if err := verifyGlue(forward, nameservers, sets[forward]); err != nil {
		return nil, err
	}
	previous := make(map[string]uint32, len(options.PreviousSerials))
	for raw, serial := range options.PreviousSerials {
		name, ok := hostname(raw)
		if _, generated := sets[name]; !ok || !generated {
			return nil, fmt.Errorf("previous serial refers to a zone not generated: %q", raw)
		}
		if _, duplicate := previous[name]; duplicate {
			return nil, fmt.Errorf("duplicate previous serial for zone %s", name)
		}
		previous[name] = serial
	}
	zones := make([]Zone, 0, len(sets))
	for name, records := range sets {
		serial := options.Serial
		before, hasPrevious := previous[name]
		if options.AdvanceSerial {
			if !hasPrevious {
				return nil, fmt.Errorf("previous SOA serial required for zone %s", name)
			}
			serial = before + 1
		} else if hasPrevious && !newerSerial(serial, before) {
			return nil, fmt.Errorf("SOA serial %d is not newer than %d for zone %s", serial, before, name)
		}
		slices.SortFunc(records, func(a, b zoneRecord) int {
			for _, comparison := range []int{strings.Compare(a.owner, b.owner), strings.Compare(a.kind, b.kind), strings.Compare(a.value, b.value)} {
				if comparison != 0 {
					return comparison
				}
			}
			return 0
		})
		var out bytes.Buffer
		fmt.Fprintf(&out, "$ORIGIN %s.\n$TTL %d\n", name, options.TTL)
		fmt.Fprintf(&out, "@ IN SOA %s. %s. ( %d %d %d %d %d )\n", primary, mailbox,
			serial, options.Refresh, options.Retry, options.Expire, options.NegativeTTL)
		for _, ns := range nameservers {
			fmt.Fprintf(&out, "@ IN NS %s.\n", ns)
		}
		for _, r := range slices.Compact(records) {
			fmt.Fprintf(&out, "%s IN %s %s\n", r.owner, r.kind, r.value)
		}
		zones = append(zones, Zone{Name: name, Contents: out.Bytes()})
	}
	slices.SortFunc(zones, func(a, b Zone) int { return strings.Compare(a.Name, b.Name) })
	return zones, nil
}

// RFC 1982 leaves serials exactly half the 32-bit range apart unordered.
func newerSerial(candidate, previous uint32) bool {
	distance := candidate - previous
	return distance != 0 && distance < 1<<31
}

func reverseOwner(addr netip.Addr) (string, string) {
	octets := addr.As4()
	zone := fmt.Sprintf("%d.%d.%d.in-addr.arpa", octets[2], octets[1], octets[0])
	return zone, fmt.Sprintf("%d.%s.", octets[3], zone)
}

func verifyGlue(forward string, nameservers []string, records []zoneRecord) error {
	addresses := make(map[string]bool)
	for _, record := range records {
		if record.kind == "A" || record.kind == "AAAA" {
			addresses[record.owner] = true
		}
	}
	for _, ns := range nameservers {
		if (ns == forward || strings.HasSuffix(ns, "."+forward)) && !addresses[ns+"."] {
			return fmt.Errorf("in-zone nameserver %s lacks an address record", ns)
		}
	}
	return nil
}

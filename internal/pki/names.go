package pki

import (
	"crypto/x509"
	"maps"
	"net/netip"
	"strings"
)

func matchesNames(leaf *x509.Certificate, policy Policy) bool {
	if len(leaf.EmailAddresses)+len(leaf.URIs) != 0 {
		return false
	}
	normalize := func(names []string) map[string]bool {
		set := make(map[string]bool)
		for _, raw := range names {
			name := strings.ToLower(strings.TrimSuffix(raw, "."))
			if name == "" || strings.Contains(name, "*") || leaf.VerifyHostname(name) != nil {
				return nil
			}
			set[name] = true
		}
		return set
	}
	actualDNS, expectedDNS := normalize(leaf.DNSNames), normalize(policy.DNS)
	if actualDNS == nil || expectedDNS == nil || !maps.Equal(actualDNS, expectedDNS) {
		return false
	}
	actualIPs := make(map[netip.Addr]bool)
	for _, ip := range leaf.IPAddresses {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			return false
		}
		actualIPs[addr.Unmap()] = true
	}
	expectedIPs := make(map[netip.Addr]bool)
	for _, ip := range policy.IPs {
		if !ip.IsValid() || ip.Zone() != "" {
			return false
		}
		expectedIPs[ip.Unmap()] = true
	}
	return maps.Equal(actualIPs, expectedIPs)
}

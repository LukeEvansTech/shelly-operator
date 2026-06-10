// Package discovery maintains ShellyDevice objects by periodically
// probing configured subnets for Shelly Gen2+ devices.
package discovery

import (
	"fmt"
	"net/netip"
)

// maxSweepHosts bounds CIDR expansion so a typo'd prefix can't generate a
// runaway sweep.
const maxSweepHosts = 4096

// ExpandCIDRs returns every probe-able host address in the given IPv4
// CIDRs, in order. Network and broadcast addresses are excluded for
// prefixes shorter than /31 (a /31 is point-to-point, a /32 is a single
// host). The total across all CIDRs is capped at maxSweepHosts.
func ExpandCIDRs(cidrs []string) ([]string, error) {
	var hosts []string
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("discovery: bad CIDR %q: %w", c, err)
		}
		if !p.Addr().Is4() {
			return nil, fmt.Errorf("discovery: only IPv4 CIDRs supported, got %q", c)
		}
		p = p.Masked()
		skipEdges := p.Bits() < 31
		first := p.Addr()
		for a := first; p.Contains(a); a = a.Next() {
			if skipEdges && (a == first || isLastInPrefix(a, p)) {
				continue
			}
			hosts = append(hosts, a.String())
			if len(hosts) > maxSweepHosts {
				return nil, fmt.Errorf("discovery: CIDRs expand to more than %d hosts", maxSweepHosts)
			}
		}
	}
	return hosts, nil
}

// isLastInPrefix reports whether a is the highest address in p (the IPv4
// broadcast address for conventional prefixes).
func isLastInPrefix(a netip.Addr, p netip.Prefix) bool {
	return !p.Contains(a.Next())
}

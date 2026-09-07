// Package netutil holds small, dependency-free IP/network helpers shared
// across packages that otherwise have no business depending on each other
// (e.g. engine/nodes's real payment-dial SSRF guard and bazaar's catalog
// filter) -- kept here rather than in either of those so neither has to
// import the other just to share one pure function.
package netutil

import "net"

// IsPrivateIP reports whether ip falls in a private, loopback, link-local,
// CGNAT, multicast, unspecified, or otherwise reserved range. This is the
// single source of truth for that classification: engine/nodes's real
// payment-dial SSRF guard (dialAndValidate) and bazaar's catalog filter
// (keepResource) both call this instead of maintaining their own,
// independently drifting range lists.
func IsPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	private := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16", // link-local
		"100.64.0.0/10",  // CGNAT
		"::1/128",        // loopback IPv6
		"fc00::/7",       // unique local IPv6
		"fe80::/10",      // link-local IPv6
		"224.0.0.0/4",    // multicast IPv4
		"ff00::/8",       // multicast IPv6 (incl. link/interface-local)
		"240.0.0.0/4",    // reserved
		"0.0.0.0/8",      // this network
	}
	for _, cidr := range private {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

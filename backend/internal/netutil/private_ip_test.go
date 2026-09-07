package netutil

import (
	"net"
	"testing"
)

// TestIsPrivateIP pins the ranges this guard blocks. It is the single
// source of truth for two independent callers -- engine/nodes's real
// dial-time SSRF check and bazaar's catalog filter -- so a range silently
// dropping out of the list here weakens both at once, with the catalog
// filter's failure mode being the quiet one (an endpoint that looks
// pickable in the UI but can never be paid).
func TestIsPrivateIP(t *testing.T) {
	blocked := []struct{ ip, why string }{
		{"127.0.0.1", "loopback v4"},
		{"::1", "loopback v6"},
		{"0.0.0.0", "unspecified v4"},
		{"::", "unspecified v6"},
		{"10.0.0.1", "RFC1918 /8"},
		{"172.16.0.1", "RFC1918 /12"},
		{"192.168.1.1", "RFC1918 /16"},
		{"169.254.169.254", "link-local v4 (cloud metadata)"},
		{"fe80::1", "link-local v6"},
		{"fc00::1", "unique local v6"},
		{"100.64.0.5", "CGNAT"},
		{"224.0.0.1", "multicast v4"},
		{"ff02::1", "multicast v6, link-local scope"},
		{"ff01::1", "multicast v6, interface-local scope"},
		{"240.0.0.1", "reserved v4"},
		{"::ffff:10.0.0.1", "v4-mapped v6 of an RFC1918 address"},
	}
	for _, tc := range blocked {
		if !IsPrivateIP(net.ParseIP(tc.ip)) {
			t.Errorf("want %s blocked (%s), got allowed", tc.ip, tc.why)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888", "93.184.216.34"}
	for _, ip := range allowed {
		if IsPrivateIP(net.ParseIP(ip)) {
			t.Errorf("want public %s allowed, got blocked", ip)
		}
	}
}

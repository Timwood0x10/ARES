package apitools

import (
	"net"
	"testing"
)

// TestIsPrivateIPBlocksExtendedRanges locks T-1: the hand-rolled checks used
// to miss CGNAT (100.64.0.0/10 — the Kubernetes pod CIDR), IPv6 ULA
// (fc00::/7), and did not normalize IPv4-mapped IPv6 addresses before
// classifying them. This mirrors builtin/network/ssrf.go's hardened gate;
// the two must not drift apart.
func TestIsPrivateIPBlocksExtendedRanges(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
		note string
	}{
		{"100.64.0.1", true, "CGNAT low bound"},
		{"100.127.255.255", true, "CGNAT high bound"},
		{"100.63.1.1", false, "just below CGNAT is public"},
		{"100.128.0.1", false, "just above CGNAT is public"},
		{"fc00::1", true, "IPv6 ULA"},
		{"fd12:3456:789a::1", true, "IPv6 ULA fd range"},
		{"::ffff:10.0.0.1", true, "IPv4-mapped private must classify private"},
		{"::ffff:8.8.8.8", false, "IPv4-mapped public stays public"},
		{"0.0.0.0", true, "unspecified"},
		{"::", true, "unspecified v6"},
		{"169.254.169.254", true, "link-local (cloud metadata service)"},
		{"fe80::1", true, "link-local v6"},
		{"10.0.0.1", true, "RFC1918 10/8"},
		{"172.16.0.1", true, "RFC1918 172.16/12 low"},
		{"172.31.255.255", true, "RFC1918 172.16/12 high"},
		{"172.32.0.1", false, "outside RFC1918"},
		{"192.168.1.1", true, "RFC1918 192.168/16"},
		{"8.8.8.8", false, "public"},
		{"2606:4700:4700::1111", false, "public v6"},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			got := isPrivateIP(net.ParseIP(tc.ip))
			if got != tc.want {
				t.Fatalf("isPrivateIP(%s) = %v, want %v (%s)", tc.ip, got, tc.want, tc.note)
			}
		})
	}
}

// TestIsPrivateIPNilFailsClosed verifies a nil IP (unparseable input) is
// treated as private/blocked, never as a pass-through.
func TestIsPrivateIPNilFailsClosed(t *testing.T) {
	if !isPrivateIP(nil) {
		t.Fatal("isPrivateIP(nil) must fail closed (blocked)")
	}
}

package builtin

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsBlockedIP verifies that isBlockedIP classifies private, loopback,
// link-local, and unspecified IPs as blocked — including IPv4-mapped IPv6
// addresses that previously bypassed the loopback/private checks.
func TestIsBlockedIP(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		blocked bool
	}{
		{name: "ipv4-mapped loopback", addr: "::ffff:127.0.0.1", blocked: true},
		{name: "ipv4-mapped link-local", addr: "::ffff:169.254.1.1", blocked: true},
		{name: "ipv4-mapped public", addr: "::ffff:8.8.8.8", blocked: false},
		{name: "ipv4-mapped private", addr: "::ffff:192.168.1.1", blocked: true},
		{name: "ipv4 unspecified", addr: "0.0.0.0", blocked: true},
		{name: "ipv4 loopback", addr: "127.0.0.1", blocked: true},
		{name: "ipv4 public", addr: "8.8.8.8", blocked: false},
		{name: "ipv6 loopback", addr: "::1", blocked: true},
		{name: "ipv4 private rfc1918", addr: "192.168.1.1", blocked: true},
		{name: "ipv4 private rfc1918 10", addr: "10.0.0.1", blocked: true},
		{name: "ipv4 public cloudflare", addr: "1.1.1.1", blocked: false},
		{name: "ipv4 link-local", addr: "169.254.1.1", blocked: true},
		// CGNAT / RFC 6598 100.64.0.0/10 (#61): used by Kubernetes pod CIDRs
		// and ISP-grade NAT; not covered by IsPrivate (RFC 1918 only).
		{name: "cgnat range start", addr: "100.64.0.1", blocked: true},
		{name: "cgnat range end", addr: "100.127.255.254", blocked: true},
		{name: "cgnat ipv4-mapped", addr: "::ffff:100.64.0.1", blocked: true},
		{name: "below cgnat range", addr: "100.63.255.255", blocked: false},
		{name: "above cgnat range", addr: "100.128.0.1", blocked: false},
		{name: "public 100.x outside cgnat", addr: "100.1.2.3", blocked: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.addr)
			require.NotNil(t, ip, "input %q must parse as IP", tt.addr)
			require.Equal(t, tt.blocked, isBlockedIP(ip), "isBlockedIP(%s)", tt.addr)
		})
	}
}

// TestSSRFDialControl exercises the connect-time validation directly, without
// any network. This is the connect-time mechanism: the dialer's Control callback fires
// with the resolved IP and re-runs isBlockedIP, closing the DNS-rebinding
// TOCTOU window.
func TestSSRFDialControl(t *testing.T) {
	tests := []struct {
		name        string
		addr        string
		wantBlocked bool
	}{
		{name: "loopback ipv4", addr: "127.0.0.1:80", wantBlocked: true},
		{name: "public ipv4", addr: "8.8.8.8:80", wantBlocked: false},
		{name: "private rfc1918", addr: "192.168.1.1:80", wantBlocked: true},
		{name: "ipv4-mapped loopback", addr: "[::ffff:127.0.0.1]:80", wantBlocked: true},
		{name: "ipv6 loopback", addr: "[::1]:80", wantBlocked: true},
		{name: "public cloudflare https", addr: "1.1.1.1:443", wantBlocked: false},
		{name: "unspecified ipv4", addr: "0.0.0.0:80", wantBlocked: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ssrfDialControl("tcp", tt.addr, nil)
			if tt.wantBlocked {
				require.Error(t, err, "expected dial to %s to be blocked", tt.addr)
				require.ErrorIs(t, err, ErrSSRFBlocked, "expected ErrSSRFBlocked for %s", tt.addr)
				return
			}
			require.NoError(t, err, "expected dial to %s to be allowed", tt.addr)
		})
	}
}

// TestSSRFDialControl_MalformedAddress ensures a non-IP, non-host:port string
// surfaces a wrapped error rather than panicking.
func TestSSRFDialControl_MalformedAddress(t *testing.T) {
	err := ssrfDialControl("tcp", "no-port-here", nil)
	require.Error(t, err)
	// Malformed input fails at SplitHostPort, before isBlockedIP, so it must
	// NOT wrap ErrSSRFBlocked.
	require.False(t, errors.Is(err, ErrSSRFBlocked))
}

// TestSSRFDialer_BlocksLoopback drives the full dialer end-to-end. Control
// fires before any TCP connect is attempted, so no listener is required and
// the test is fully offline.
func TestSSRFDialer_BlocksLoopback(t *testing.T) {
	_, err := SSRFDialer().DialContext(context.Background(), "tcp", "127.0.0.1:80")
	require.ErrorIs(t, err, ErrSSRFBlocked)

	_, err = SSRFDialer().DialContext(context.Background(), "tcp", "192.168.1.1:80")
	require.ErrorIs(t, err, ErrSSRFBlocked)
}

// TestSSRFTransport_ReturnsClonedTransport verifies SSRFTransport wires a
// non-nil DialContext backed by SSRFDialer, without performing any network.
func TestSSRFTransport_ReturnsClonedTransport(t *testing.T) {
	tr := SSRFTransport()
	require.NotNil(t, tr)
	require.NotNil(t, tr.DialContext, "SSRFTransport must set DialContext")
}

// TestSSRFControlLayer_IPLiteralsAndHostnames pins the dial-control contract
// hermetically (no DNS, so it runs in every mode including -short): the control
// function is handed the RESOLVED ip by the dialer, never a hostname — an IP
// literal is therefore classified by its address, while a hostname parses to a
// nil IP and fails closed. A previous version of TestSSRF_Rebinding_nipio
// asserted the opposite (that the hostname "1.1.1.1.nip.io" is allowed here),
// which can never hold and only went unnoticed because -short skipped it.
func TestSSRFControlLayer_IPLiteralsAndHostnames(t *testing.T) {
	// The dialer resolves the hostname before calling Control, so this is the
	// shape that actually reaches production: a public IP is allowed.
	require.NoError(t, ssrfDialControl("tcp", "1.1.1.1:80", nil))
	// Private/loopback/link-local literals stay blocked.
	require.ErrorIs(t, ssrfDialControl("tcp", "10.0.0.1:80", nil), ErrSSRFBlocked)
	require.ErrorIs(t, ssrfDialControl("tcp", "127.0.0.1:80", nil), ErrSSRFBlocked)
	require.ErrorIs(t, ssrfDialControl("tcp", "169.254.169.254:80", nil), ErrSSRFBlocked)
	// A hostname is not an IP → fail closed (never a silent pass).
	require.ErrorIs(t, ssrfDialControl("tcp", "1.1.1.1.nip.io:80", nil), ErrSSRFBlocked)
	// A malformed address is an explicit error, not a silent pass.
	require.Error(t, ssrfDialControl("tcp", "no-port-here", nil))
}

// TestSSRF_Rebinding_nipio is the end-to-end DNS-rebinding check: it uses
// nip.io so the dialer really resolves a public-looking name to a blocked IP.
// It needs DNS, not a test mode, so the guard is resolvability (offline runs
// skip it) — deliberately NOT -short: hiding a network assertion behind -short
// is what let a broken variant of this test survive locally while CI (which
// runs the un-shortened suite) failed on it.
func TestSSRF_Rebinding_nipio(t *testing.T) {
	// Precheck: skip if nip.io cannot be resolved (offline CI).
	addrs, err := net.DefaultResolver.LookupHost(context.Background(), "127.0.0.1.nip.io")
	if err != nil {
		t.Skipf("skipping: nip.io not resolvable: %v", err)
	}
	// Verify that at least one resolved IP is actually a blocked IP.
	// nip.io may not resolve to 127.0.0.1 in all environments.
	hasBlocked := false
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip != nil && isBlockedIP(ip) {
			hasBlocked = true
			break
		}
	}
	if !hasBlocked {
		t.Skipf("skipping: nip.io resolved to non-blocked IPs %v", addrs)
	}

	_, err = SSRFDialer().DialContext(context.Background(), "tcp", "127.0.0.1.nip.io:80")
	require.ErrorIs(t, err, ErrSSRFBlocked)

	// The public-name half of the contract is asserted hermetically in
	// TestSSRFControlLayer_IPLiteralsAndHostnames — Control never sees a
	// hostname, so it cannot be asserted through this dial path offline.
}

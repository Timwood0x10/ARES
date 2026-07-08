package builtin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// MaxHTTPResponseBytes is the default cap on response body reads to prevent
// memory exhaustion from oversized responses.
const MaxHTTPResponseBytes = 10 * 1024 * 1024 // 10 MB

// MaxHTTPRedirects limits how many redirects the HTTP client will follow.
// Each hop is re-validated against the SSRF filter.
const MaxHTTPRedirects = 3

// ErrSSRFBlocked is returned when a URL targets a blocked address.
var ErrSSRFBlocked = fmt.Errorf("url targets a blocked address (private/loopback/link-local)")

// ErrUnsupportedScheme is returned when a URL uses a non-http(s) scheme.
var ErrUnsupportedScheme = fmt.Errorf("only http and https schemes are allowed")

// ValidateURL checks that a URL string uses an allowed scheme and does not
// resolve to a private, loopback, or link-local address. It defends against
// SSRF attacks targeting cloud metadata endpoints and internal services.
func ValidateURL(ctx context.Context, rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("url is required")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url %q: %w", rawURL, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ErrUnsupportedScheme
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host")
	}

	return checkHost(ctx, host)
}

// checkHost resolves the host and rejects private, loopback, or link-local IPs.
// Hostnames that resolve to multiple IPs are rejected if any IP is blocked.
func checkHost(ctx context.Context, host string) error {
	// Handle bracketed IPv6 form.
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}

	// If the host is already an IP literal, validate it directly.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: %s", ErrSSRFBlocked, ip)
		}
		return nil
	}

	// Resolve the hostname and reject if any resolved address is blocked.
	ipAddrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	for _, ipAddr := range ipAddrs {
		if isBlockedIP(ipAddr.IP) {
			return fmt.Errorf("%w: %s resolves to %s", ErrSSRFBlocked, host, ipAddr.IP)
		}
	}
	return nil
}

// isBlockedIP reports whether the IP is private, loopback, link-local, or
// otherwise unsuitable as an outbound destination from a tool.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsUnspecified() {
		return true
	}
	return false
}

// SSRFCheckRedirect returns an http.CheckRedirect function that caps the
// number of redirects and re-validates each destination URL against the SSRF
// filter. This prevents public URLs from redirecting to internal services.
func SSRFCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxHTTPRedirects {
		return fmt.Errorf("stopped after %d redirects", MaxHTTPRedirects)
	}
	return ValidateURL(req.Context(), req.URL.String())
}

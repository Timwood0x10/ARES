package apitools

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	builtin_network "github.com/Timwood0x10/ares/internal/tools/resources/builtin/network"
)

// TestWebSearchTool_SSRFClientRefusesPrivateTargets is the first half of the SSRF fix: the guard
// must sit at CONNECT time, not in a pre-flight resolution.
//
// The previous implementation resolved the host, checked the addresses, and then dialled by NAME —
// so a DNS answer that changed between the two steps reached a private address that had already
// passed the check. Asserting on the dial of a loopback address proves the connection-time control
// is wired, which is the property a pre-flight check cannot provide.
//
// The block-list predicate itself is covered once, in the network package (isBlockedIP); this file
// only asserts what apitools wires up.
func TestWebSearchTool_SSRFClientRefusesPrivateTargets(t *testing.T) {
	tool := newWebSearchTool()

	transport, ok := tool.ssrfClient.Transport.(*http.Transport)
	require.True(t, ok, "the SSRF client must carry the hardened transport")
	require.NotNil(t, transport.DialContext)

	_, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:9")
	require.Error(t, err, "a loopback target must be refused before any connection is made")
	assert.Contains(t, err.Error(), "blocked address",
		"the refusal must come from the SSRF control, not from a connection failure: %v", err)
}

// TestWebSearchTool_SSRFClientRejectsRedirectToPrivate is the redirect half: a public endpoint that
// answers 302 to a metadata address must not be followed.
//
// Go's default client follows up to ten hops and checks nothing about where they lead, so a
// compromised SearXNG instance (or any endpoint the LLM can name) could redirect the request into
// the instance metadata service.
func TestWebSearchTool_SSRFClientRejectsRedirectToPrivate(t *testing.T) {
	tool := newWebSearchTool()
	require.NotNil(t, tool.ssrfClient.CheckRedirect,
		"without a CheckRedirect the client follows redirects anywhere")

	req := &http.Request{URL: &url.URL{Scheme: "http", Host: "169.254.169.254", Path: "/latest/meta-data/"}}
	err := tool.ssrfClient.CheckRedirect(req, []*http.Request{{URL: &url.URL{Scheme: "http", Host: "search.example"}}})

	require.Error(t, err, "a redirect to the metadata service must be refused")
}

// TestWebSearchTool_TrustedClientCapsRedirects: the operator-configured endpoint is trusted by
// configuration, but its responses are not. Hops are capped; the destination is deliberately NOT
// IP-checked, because the documented default endpoint is localhost:5605 and the SSRF block list
// would refuse it.
func TestWebSearchTool_TrustedClientCapsRedirects(t *testing.T) {
	tool := newWebSearchTool()
	require.NotNil(t, tool.trustedClient.CheckRedirect,
		"an unbounded redirect chain against a broken proxy holds the request open for free")

	sameHost := &url.URL{Scheme: "http", Host: "localhost:5605", Path: "/search"}
	req := &http.Request{URL: sameHost}

	assert.NoError(t, tool.trustedClient.CheckRedirect(req, nil),
		"the first hop to the operator's own endpoint must be allowed")

	via := make([]*http.Request, builtin_network.MaxHTTPRedirects)
	for i := range via {
		via[i] = &http.Request{URL: sameHost}
	}
	require.Error(t, tool.trustedClient.CheckRedirect(req, via),
		"a chain longer than the cap must stop")
}

// TestWebSearchTool_SSRFClientDoesNotInheritTheProxy: ProxyFromEnvironment would let a proxy
// resolve and connect on the tool's behalf, bypassing the dial-time check entirely.
func TestWebSearchTool_SSRFClientDoesNotInheritTheProxy(t *testing.T) {
	tool := newWebSearchTool()

	transport, ok := tool.ssrfClient.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, transport.Proxy, "a proxy would route around the connect-time IP check")
}

// TestWebSearchTool_SSRFClientBlocksMetadataAddress keeps the most valuable target in the test:
// link-local 169.254.169.254 is not a private RFC1918 range, so an implementation that only checks
// IsPrivate would let it through.
func TestWebSearchTool_SSRFClientBlocksMetadataAddress(t *testing.T) {
	tool := newWebSearchTool()
	transport, ok := tool.ssrfClient.Transport.(*http.Transport)
	require.True(t, ok)

	// DialContext takes host:port; name resolution is not involved for a literal address.
	_, err := transport.DialContext(context.Background(), "tcp", net.JoinHostPort("169.254.169.254", "80"))

	require.Error(t, err, "the instance metadata service must never be reachable")
	assert.Contains(t, err.Error(), "blocked address")
}

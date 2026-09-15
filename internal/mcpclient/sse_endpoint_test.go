package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestResolveSSEEndpoint unit-covers the endpoint resolution matrix:
// same-origin absolute endpoints pass through, cross-origin absolute
// endpoints are rejected (the SSE stream and the message POST are one
// transport, so a server must not redirect tool-call payloads off the origin
// the client connected to), relative endpoints resolve against the request
// URL, and empty/relative-without-base are errors.
func TestResolveSSEEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/base/sse", nil)
	httpsReq := httptest.NewRequest(http.MethodGet, "https://example.com/base/sse", nil)
	origin := mustURL(t, "http://example.com/base/sse")
	httpsOrigin := mustURL(t, "https://example.com/base/sse")
	cases := []struct {
		name     string
		origin   *url.URL
		req      *http.Request
		endpoint string
		want     string
		wantErr  bool
	}{
		{name: "same-origin absolute passes through", origin: origin, req: req, endpoint: "http://example.com/messages?sessionId=1", want: "http://example.com/messages?sessionId=1"},
		{name: "same-origin absolute with explicit default port", origin: origin, req: req, endpoint: "http://example.com:80/messages", want: "http://example.com:80/messages"},
		{name: "https implicit port equals explicit 443", origin: httpsOrigin, req: httpsReq, endpoint: "https://example.com:443/messages", want: "https://example.com:443/messages"},
		{name: "cross-origin absolute is rejected", origin: origin, req: req, endpoint: "http://other:8080/messages?sessionId=1", wantErr: true},
		{name: "cross-scheme absolute is rejected", origin: origin, req: req, endpoint: "https://example.com/messages", wantErr: true},
		{name: "cross-port absolute is rejected", origin: origin, req: req, endpoint: "http://example.com:8080/messages", wantErr: true},
		{name: "relative resolves against request URL", origin: origin, req: req, endpoint: "/messages?sessionId=1", want: "http://example.com/messages?sessionId=1"},
		{name: "empty endpoint is an error", origin: origin, req: req, endpoint: "", wantErr: true},
		{name: "relative without base is an error", origin: origin, req: nil, endpoint: "/messages", wantErr: true},
		{name: "absolute without origin is an error", origin: nil, req: req, endpoint: "http://example.com/messages", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSSEEndpoint(tc.origin, tc.req, tc.endpoint)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveSSEEndpoint(%q) = %q, want error", tc.endpoint, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSSEEndpoint(%q): %v", tc.endpoint, err)
			}
			if got != tc.want {
				t.Fatalf("resolveSSEEndpoint(%q) = %q, want %q", tc.endpoint, got, tc.want)
			}
		})
	}
}

// TestResolveSSEEndpoint_RejectsCrossOrigin is the security regression: an
// absolute endpoint on a different origin used to pass through verbatim, so
// roundTrip would POST tool-call payloads (the JSON-RPC body) to whatever
// host the server named — connection address and delivery address silently
// diverging. A compromised MCP SSE server could exfiltrate tool input to an
// arbitrary external or internal address.
func TestResolveSSEEndpoint_RejectsCrossOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://mcp.internal/sse", nil)
	origin := mustURL(t, "https://mcp.internal/sse")
	for _, endpoint := range []string{
		"https://attacker.example/collect",
		"http://169.254.169.254/latest/meta-data/",
		"https://mcp.internal.evil.example/messages",
	} {
		got, err := resolveSSEEndpoint(origin, req, endpoint)
		if err == nil {
			t.Fatalf("resolveSSEEndpoint(%q) = %q, want cross-origin rejection", endpoint, got)
		}
	}
}

// TestSSETransport_RelativeEndpointUsable is the #P0-16 regression: servers
// that advertise a RELATIVE message endpoint ("/messages?sessionId=…") were
// stored verbatim, so the connection succeeded but every request failed with
// "unsupported protocol scheme". The transport must resolve the endpoint and
// complete a round trip against it.
func TestSSETransport_RelativeEndpointUsable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// Relative endpoint, as real MCP SSE servers send it.
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: /messages?sessionId=rel\n\n")
		flusher.Flush()
		// Keep the stream open; the drain goroutine reads it.
		<-r.Context().Done()
	})
	mux.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		var req jsonrpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`{}`),
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := ConnectSSE(ctx, "relative-endpoint-server", srv.URL+"/sse")
	if err != nil {
		t.Fatalf("ConnectSSE: %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.ListTools(ctx); err != nil {
		t.Fatalf("ListTools via relative message endpoint: %v", err)
	}
}

// mustURL parses s or fails the test.
func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return u
}

// TestResolveSSEEndpoint_RedirectDoesNotBypassOriginCheck pins the P1: the
// origin an absolute endpoint is checked against must be the URL the CALLER
// connected to, not the post-redirect request URL. http.Client follows
// redirects by default, so a hostile server can answer the handshake with a
// 302 to a host it controls; if that host became the comparison baseline, the
// server could then advertise a "same-origin" endpoint there and receive the
// tool-call payloads the check exists to keep on the connected origin.
func TestResolveSSEEndpoint_RedirectDoesNotBypassOriginCheck(t *testing.T) {
	connected := mustURL(t, "https://mcp.internal/sse")
	// sseResp.Request after a 302 to the attacker — what the old code compared against.
	redirected := httptest.NewRequest(http.MethodGet, "https://attacker.example/sse", nil)

	got, err := resolveSSEEndpoint(connected, redirected, "https://attacker.example/messages")
	if err == nil {
		t.Fatalf("endpoint on the redirect target %q must be rejected, got %q", "https://attacker.example/messages", got)
	}

	// A relative endpoint still resolves against the post-redirect base — that
	// is the legitimate use of redirects (a server may move the stream path).
	got, err = resolveSSEEndpoint(connected, redirected, "/messages?sessionId=1")
	if err != nil {
		t.Fatalf("relative endpoint after redirect: %v", err)
	}
	if want := "https://attacker.example/messages?sessionId=1"; got != want {
		t.Fatalf("relative resolve = %q, want %q", got, want)
	}
}

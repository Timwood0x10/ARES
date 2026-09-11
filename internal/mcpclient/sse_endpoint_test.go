package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestResolveSSEEndpoint unit-covers the endpoint resolution matrix:
// absolute endpoints pass through, relative endpoints resolve against the
// request URL, and empty/relative-without-base are errors.
func TestResolveSSEEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/base/sse", nil)
	cases := []struct {
		name     string
		req      *http.Request
		endpoint string
		want     string
		wantErr  bool
	}{
		{name: "absolute passes through", req: req, endpoint: "http://other:8080/messages?sessionId=1", want: "http://other:8080/messages?sessionId=1"},
		{name: "relative resolves against request URL", req: req, endpoint: "/messages?sessionId=1", want: "http://example.com/messages?sessionId=1"},
		{name: "empty endpoint is an error", req: req, endpoint: "", wantErr: true},
		{name: "relative without base is an error", req: nil, endpoint: "/messages", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSSEEndpoint(tc.req, tc.endpoint)
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

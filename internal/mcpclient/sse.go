package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// sseTransport implements JSON-RPC over MCP SSE transport.
type sseTransport struct {
	client     *http.Client
	messageURL string
	sseBody    io.ReadCloser
	sseCtx     context.Context
	sseCancel  context.CancelFunc
	closeMu    sync.Mutex
	closed     bool
}

// ConnectSSE connects to an MCP server via SSE transport.
func ConnectSSE(ctx context.Context, name, url string) (*Client, error) {
	tr := &sseTransport{
		client: &http.Client{Timeout: 0}, // SSE requires no timeout
	}

	sseReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("sse request: %w", err)
	}
	sseReq.Header.Set("Accept", "text/event-stream")

	sseResp, err := tr.client.Do(sseReq)
	if err != nil {
		return nil, fmt.Errorf("sse connect: %w", err)
	}
	if sseResp.StatusCode != http.StatusOK {
		if err := sseResp.Body.Close(); err != nil {
			log.Warn("mcp: close sse body on bad status", "error", err)
		}
		return nil, fmt.Errorf("sse connect: unexpected status %d", sseResp.StatusCode)
	}

	tr.sseBody = sseResp.Body
	tr.sseCtx, tr.sseCancel = context.WithCancel(ctx)

	// One scanner for the connection lifetime: readEndpointEvent stops at
	// the endpoint event but may have buffered later events in its read-ahead;
	// reusing this scanner in the drain goroutine preserves them (#48).
	sc := bufio.NewScanner(tr.sseBody)
	// Raise the scanner cap: the 64KB default silently kills the drain on
	// any oversized SSE line (large server notification), reintroducing the
	// receive-window stall #48 fixed — the stdio transport already guards
	// this class with a 64MB buffer.
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)

	// Read SSE events until we get the endpoint event.
	endpoint, err := tr.readEndpointEvent(sc)
	if err != nil {
		tr.sseCancel()
		if err := sseResp.Body.Close(); err != nil {
			log.Warn("mcp: close sse body on read error", "error", err)
		}
		return nil, fmt.Errorf("read endpoint: %w", err)
	}
	// Resolve the advertised message endpoint against the request URL: MCP
	// SSE servers commonly send a RELATIVE endpoint ("/messages?sessionId=…"),
	// and storing it verbatim makes every later POST fail with "unsupported
	// protocol scheme" — the connection is up but unusable. sseResp.Request
	// is the final request after redirects, so it is the correct base.
	messageURL, err := resolveSSEEndpoint(sseResp.Request, endpoint)
	if err != nil {
		tr.sseCancel()
		if err := sseResp.Body.Close(); err != nil {
			log.Warn("mcp: close sse body on endpoint resolve error", "error", err)
		}
		return nil, fmt.Errorf("resolve endpoint %q: %w", endpoint, err)
	}
	tr.messageURL = messageURL

	// Keep draining the SSE stream for the lifetime of the connection (#48):
	// responses arrive via the message endpoint, but the server keeps pushing
	// events (keepalives, notifications) on this body. If nobody reads it, the
	// TCP receive window fills and the whole connection stalls. close()
	// cancels sseCancel and closes sseBody, which unblocks this goroutine.
	go tr.drainSSE(sc)

	c := &Client{
		name:      name,
		transport: tr,
		idCounter: 1,
	}

	if err := c.initialize(ctx); err != nil {
		_ = tr.close() //nolint: errcheck
		return nil, fmt.Errorf("initialize: %w", err)
	}

	c.connected = true
	return c, nil
}

// notify POSTs a JSON-RPC notification to the SSE message endpoint. The server
// sends no response for notifications; the body is drained and closed.
func (tr *sseTransport) notify(ctx context.Context, notif jsonrpcNotification) error {
	body, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tr.messageURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpResp, err := tr.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer func() {
		if err := httpResp.Body.Close(); err != nil {
			log.Warn("mcp: close http response body", "error", err)
		}
	}()
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("post: unexpected status %d", httpResp.StatusCode)
	}
	return nil
}

// readEndpointEvent reads SSE events until an "endpoint" event is received.
// The scanner is owned by the caller (the connection-lifetime scanner) so the
// drain goroutine continues exactly where this left off — a fresh scanner
// would discard whatever the old one had buffered past the endpoint event.
func (tr *sseTransport) readEndpointEvent(sc *bufio.Scanner) (string, error) {
	var eventType string
	var dataBuf strings.Builder

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			// Empty line = end of event.
			if eventType == "endpoint" {
				return strings.TrimSpace(dataBuf.String()), nil
			}
			eventType = ""
			dataBuf.Reset()
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("sse scan: %w", err)
	}
	return "", fmt.Errorf("sse stream ended without endpoint event")
}

// resolveSSEEndpoint resolves the endpoint advertised by the server's
// "endpoint" event into an absolute URL. Absolute endpoints pass through;
// relative ones are resolved against the (post-redirect) request URL. An
// empty endpoint or a relative endpoint without a base URL is an error —
// both left the transport unusable with a confusing later POST failure.
func resolveSSEEndpoint(req *http.Request, endpoint string) (string, error) {
	if endpoint == "" {
		return "", fmt.Errorf("empty endpoint event")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	if u.IsAbs() {
		return u.String(), nil
	}
	if req == nil || req.URL == nil {
		return "", fmt.Errorf("relative endpoint %q without a base request URL", endpoint)
	}
	return req.URL.ResolveReference(u).String(), nil
}

// drainSSE consumes the SSE body until the stream ends or the transport is
// closed. Server-initiated events are discarded: responses to client requests
// arrive on the message endpoint (roundTrip), so this loop exists purely to
// keep the receive window open (#48).
func (tr *sseTransport) drainSSE(sc *bufio.Scanner) {
	for sc.Scan() {
		// Discard.
	}
	if err := sc.Err(); err != nil {
		// A scanner error ends the drain permanently (no reconnect) — log it
		// so a stall is diagnosable instead of silent.
		log.Warn("mcp: sse drain stopped", "error", err)
	}
}

func (tr *sseTransport) roundTrip(ctx context.Context, req jsonrpcRequest) (*jsonrpcResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	// Use the per-request context so a caller timeout or cancellation can
	// abort a hung POST instead of waiting on the transport's own context,
	// which has no deadline (M13).
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tr.messageURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("post request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := tr.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer func() {
		if err := httpResp.Body.Close(); err != nil {
			log.Warn("mcp: close http response body", "error", err)
		}
	}()

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("post: unexpected status %d", httpResp.StatusCode)
	}

	var resp jsonrpcResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}

func (tr *sseTransport) close() error {
	tr.closeMu.Lock()
	defer tr.closeMu.Unlock()
	if tr.closed {
		return nil
	}
	tr.closed = true
	tr.sseCancel()
	if tr.sseBody != nil {
		return tr.sseBody.Close()
	}
	return nil
}

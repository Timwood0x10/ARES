package ares_mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	sseEventPrefix       = "event:"
	sseDataPrefix        = "data:"
	sseEventTypeEndpoint = "endpoint"
	sseEventTypeMessage  = "message"

	defaultSSETimeout       = 30 * time.Second
	defaultSSEMessageBuffer = 64
)

// SSEConfig holds configuration for an SSE-based MCP transport.
type SSEConfig struct {
	URL     string            `yaml:"url" json:"url"`
	Headers map[string]string `yaml:"headers" json:"headers"`
	Timeout time.Duration     `yaml:"timeout" json:"timeout"`
}

// SSETransport implements Transport by communicating with an MCP server
// over HTTP SSE (Server-Sent Events). Messages are sent via POST and
// received via an SSE stream.
type SSETransport struct {
	config     SSEConfig
	httpClient *http.Client
	msgCh      chan *JSONRPCMessage
	cancel     context.CancelFunc
	eg         errgroup.Group
	mu         sync.Mutex
	started    bool
	postURL    string
	// ready carries the handshake outcome exactly once (guarded by
	// readyOnce) so Start can block until the SSE connection is actually
	// established instead of returning while the dial is still in flight.
	ready     chan error
	readyOnce sync.Once
}

// NewSSETransport creates a new SSE transport with the given config.
func NewSSETransport(config SSEConfig) *SSETransport {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = defaultSSETimeout
	}

	// SSE connections are long-lived, so we must NOT set http.Client.Timeout
	// (which would kill the stream after the timeout elapses). Instead, use a
	// Transport with a DialContext timeout for connection establishment only.
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: dialer.DialContext,
	}

	return &SSETransport{
		config: config,
		httpClient: &http.Client{
			Transport: transport,
			// No Timeout: SSE streams are long-lived and must not be killed.
		},
		msgCh:   make(chan *JSONRPCMessage, defaultSSEMessageBuffer),
		postURL: config.URL, // Set default POST URL; may be overridden by "endpoint" SSE event.
		ready:   make(chan error, 1),
	}
}

// signalHandshake reports the connection outcome to a Start caller exactly
// once: nil once the SSE stream is live, or the handshake error.
func (t *SSETransport) signalHandshake(err error) {
	t.readyOnce.Do(func() { t.ready <- err })
}

// Start connects to the SSE endpoint and begins listening for messages.
// It blocks until the SSE connection is established (or the handshake
// fails) so a caller that immediately Send/Receive after Start does not
// race the in-flight dial — the old fire-and-forget return made a
// connection-refused surface as a later, confusing receive error.
func (t *SSETransport) Start(ctx context.Context) error {
	t.mu.Lock()

	if t.started {
		t.mu.Unlock()
		return errors.New("transport already started")
	}

	if t.config.URL == "" {
		t.mu.Unlock()
		return errors.New("sse url is required")
	}

	ctx, t.cancel = context.WithCancel(ctx)
	t.started = true
	t.mu.Unlock()

	// Connect to SSE endpoint and start receiving messages.
	t.eg.Go(func() error {
		err := t.receiveLoop(ctx)
		close(t.msgCh)
		return err
	})

	select {
	case err := <-t.ready:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// receiveLoop connects to the SSE endpoint and reads events.
func (t *SSETransport) receiveLoop(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.config.URL, nil)
	if err != nil {
		t.signalHandshake(fmt.Errorf("create sse request: %w", err))
		return fmt.Errorf("create sse request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range t.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		t.signalHandshake(fmt.Errorf("sse connect: %w", err))
		return fmt.Errorf("sse connect: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if cerr := resp.Body.Close(); cerr != nil {
			t.signalHandshake(fmt.Errorf("sse endpoint returned status %d: %w", resp.StatusCode, cerr))
			return fmt.Errorf("sse endpoint returned status %d: %w", resp.StatusCode, cerr)
		}

		t.signalHandshake(fmt.Errorf("sse endpoint returned status %d", resp.StatusCode))
		return fmt.Errorf("sse endpoint returned status %d", resp.StatusCode)
	}

	// respBody is NOT stored: Close() interrupts the receive loop via ctx
	// cancellation and the loop's deferred resp.Body.Close() releases the
	// resource (storing it for Close was dead code — nothing ever read it).
	// Connection live: release the Start caller.
	t.signalHandshake(nil)

	// Ensure body is closed exactly once when receiveLoop returns.
	// Close() now only cancels the context (no direct Body close),
	// so this defer is the sole cleanup point for resp.Body.
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("http: close response body failed", "error", err)
		}
	}()

	reader := bufio.NewReader(resp.Body)
	var eventType string
	var dataBuilder strings.Builder

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("sse read: %w", err)
		}

		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			// Empty line = dispatch event.
			if dataBuilder.Len() > 0 {
				data := dataBuilder.String()
				t.handleSSEEvent(ctx, eventType, data)
				dataBuilder.Reset()
			}
			eventType = ""
			continue
		}

		if strings.HasPrefix(line, sseEventPrefix) {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, sseEventPrefix))
		} else if strings.HasPrefix(line, sseDataPrefix) {
			dataStr := strings.TrimSpace(strings.TrimPrefix(line, sseDataPrefix))
			if dataBuilder.Len() > 0 {
				dataBuilder.WriteByte('\n')
			}
			dataBuilder.WriteString(dataStr)
		}
	}
}

// handleSSEEvent processes a single SSE event.
func (t *SSETransport) handleSSEEvent(ctx context.Context, eventType, data string) {
	switch eventType {
	case sseEventTypeEndpoint:
		// Server provides the POST URL for sending messages. Validate the URL
		// host matches the SSE source host to prevent SSRF: a malicious server
		// could otherwise redirect POSTs to an internal endpoint.
		endpoint := strings.TrimSpace(data)
		if !t.isSameHostEndpoint(endpoint) {
			log.Warn("mcp: rejecting endpoint URL with mismatched host (SSRF protection)",
				"sse_url", t.config.URL, "endpoint_url", endpoint)
			return
		}
		t.mu.Lock()
		t.postURL = endpoint
		t.mu.Unlock()
	case sseEventTypeMessage:
		var msg JSONRPCMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return
		}
		select {
		case t.msgCh <- &msg:
		case <-ctx.Done():
		}
	default:
		// Unknown event type, try to parse as JSON-RPC message.
		var msg JSONRPCMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return
		}
		select {
		case t.msgCh <- &msg:
		case <-ctx.Done():
		}
	}
}

// isSameHostEndpoint validates that the endpoint URL targets the same host as
// the configured SSE URL. Relative URLs are resolved against the SSE URL and
// are always allowed. Absolute URLs must share the same host (and port).
func (t *SSETransport) isSameHostEndpoint(endpoint string) bool {
	if endpoint == "" {
		return false
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	// Resolve relative endpoints against the SSE base URL.
	baseURL, err := url.Parse(t.config.URL)
	if err != nil {
		return false
	}
	resolved := baseURL.ResolveReference(endpointURL)
	// Compare host (includes port). An empty host on the endpoint means it was
	// relative, which is safe after resolution.
	return resolved.Host == baseURL.Host
}

// Send sends a JSON-RPC message to the server via HTTP POST.
func (t *SSETransport) Send(ctx context.Context, msg *JSONRPCMessage) error {
	t.mu.Lock()
	if !t.started {
		t.mu.Unlock()
		return errors.New("transport not started")
	}
	postURL := t.postURL
	t.mu.Unlock()

	data, err := Encode(msg)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post message: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("http: close response body failed", "error", err)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Receive returns the next JSON-RPC message from the SSE stream.
func (t *SSETransport) Receive(ctx context.Context) (*JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-t.msgCh:
		if !ok {
			return nil, errors.New("channel closed")
		}
		return msg, nil
	}
}

// Close shuts down the SSE transport.
func (t *SSETransport) Close() error {
	t.mu.Lock()
	if !t.started {
		t.mu.Unlock()
		return nil
	}

	t.started = false
	if t.cancel != nil {
		t.cancel()
	}
	t.mu.Unlock()

	// Wait for the receive loop outside the lock to avoid deadlock:
	// receiveLoop -> handleSSEEvent may need t.mu to update postURL.
	// The channel is closed inside the errgroup goroutine after receiveLoop returns,
	// so there is no race between handleSSEEvent sending and close.
	// Context cancellation unblocks the blocking ReadString in receiveLoop,
	// and the deferred resp.Body.Close() inside receiveLoop releases the resource.
	if err := t.eg.Wait(); err != nil {
		log.Error("mcp: sse receive loop error", "url", t.config.URL, "error", err)
	}

	return nil
}

// Ensure SSETransport implements Transport at compile time.
var _ Transport = (*SSETransport)(nil)

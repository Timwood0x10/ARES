package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
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
}

// NewSSETransport creates a new SSE transport with the given config.
func NewSSETransport(config SSEConfig) *SSETransport {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &SSETransport{
		config: config,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		msgCh:   make(chan *JSONRPCMessage, 64),
		postURL: config.URL, // Set default POST URL; may be overridden by "endpoint" SSE event.
	}
}

// Start connects to the SSE endpoint and begins listening for messages.
func (t *SSETransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return fmt.Errorf("transport already started")
	}

	if t.config.URL == "" {
		return fmt.Errorf("sse url is required")
	}

	ctx, t.cancel = context.WithCancel(ctx)
	t.started = true

	// Connect to SSE endpoint and start receiving messages.
	t.eg.Go(func() error {
		return t.receiveLoop(ctx)
	})

	return nil
}

// receiveLoop connects to the SSE endpoint and reads events.
func (t *SSETransport) receiveLoop(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.config.URL, nil)
	if err != nil {
		return fmt.Errorf("create sse request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range t.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sse connect: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sse endpoint returned status %d", resp.StatusCode)
	}

	// POST URL defaults to config.URL (set in constructor). If the SSE endpoint
	// returns an "endpoint" event, handleSSEEvent updates it under t.mu.

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

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
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
	case "endpoint":
		// Server provides the POST URL for sending messages.
		t.mu.Lock()
		t.postURL = strings.TrimSpace(data)
		t.mu.Unlock()
	case "message":
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

// Send sends a JSON-RPC message to the server via HTTP POST.
func (t *SSETransport) Send(ctx context.Context, msg *JSONRPCMessage) error {
	t.mu.Lock()
	if !t.started {
		t.mu.Unlock()
		return fmt.Errorf("transport not started")
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
	defer func() { _ = resp.Body.Close() }()

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
			return nil, fmt.Errorf("channel closed")
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
	if err := t.eg.Wait(); err != nil {
		slog.Error("mcp: sse receive loop error", "url", t.config.URL, "error", err)
	}
	close(t.msgCh)

	return nil
}

// Ensure SSETransport implements Transport at compile time.
var _ Transport = (*SSETransport)(nil)

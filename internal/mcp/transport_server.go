// Package mcp provides MCP (Model Context Protocol) server-side transport.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// ServerTransport is the interface for server-side message transport.
// Unlike client Transport (send request, receive response), server transport
// accepts incoming requests and sends back responses.
type ServerTransport interface {
	// Start begins listening for incoming connections.
	Start(ctx context.Context) error
	// Accept returns the next incoming JSON-RPC request.
	Accept(ctx context.Context) (*JSONRPCMessage, error)
	// Send sends a response or notification back to the client.
	Send(ctx context.Context, msg *JSONRPCMessage) error
	// Close shuts down the transport.
	Close() error
}

// --- StdioServerTransport ---

// StdioServerTransport implements ServerTransport using stdin/stdout.
// It reads JSON-RPC requests from stdin (line-delimited) and writes
// responses to stdout. This is the standard transport for MCP servers
// launched as subprocesses.
type StdioServerTransport struct {
	scanner *bufio.Scanner
	mu      sync.Mutex
	started atomic.Bool
	msgCh   chan *JSONRPCMessage
	errCh   chan error
	eg      errgroup.Group
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewStdioServerTransport creates a new stdio-based server transport.
//
// Returns:
//   - *StdioServerTransport: the new transport instance
func NewStdioServerTransport() *StdioServerTransport {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	return &StdioServerTransport{
		scanner: scanner,
		msgCh:   make(chan *JSONRPCMessage, 1),
		errCh:   make(chan error, 1),
	}
}

// Start prepares the stdio transport for reading requests.
//
// Args:
//   - ctx: context for cancellation (unused for stdio but required by interface)
//
// Returns:
//   - error: non-nil if the transport was already started
func (t *StdioServerTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started.Load() {
		return fmt.Errorf("transport already started")
	}

	t.ctx, t.cancel = context.WithCancel(ctx)
	t.started.Store(true)

	t.eg.Go(func() error {
		for {
			if !t.scanner.Scan() {
				err := t.scanner.Err()
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				return fmt.Errorf("stdin closed")
			}
			data := t.scanner.Bytes()
			msg, err := Decode(data)
			if err != nil {
				return fmt.Errorf("decode message: %w", err)
			}
			select {
			case t.msgCh <- msg:
			case <-t.ctx.Done():
				return t.ctx.Err()
			}
		}
	})

	slog.Debug("mcp-server: stdio transport started")
	return nil
}

// Accept reads the next JSON-RPC request from stdin.
//
// Args:
//   - ctx: context for cancellation
//
// Returns:
//   - *JSONRPCMessage: the parsed request message
//   - error: non-nil on read/decode error or context cancellation
func (t *StdioServerTransport) Accept(ctx context.Context) (*JSONRPCMessage, error) {
	if !t.started.Load() {
		return nil, fmt.Errorf("transport not started")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-t.msgCh:
		return msg, nil
	}
}

// Send writes a JSON-RPC response to stdout.
//
// Args:
//   - ctx: context for cancellation (unused for stdout write)
//   - msg: the response message to send
//
// Returns:
//   - error: non-nil on encode or write error
func (t *StdioServerTransport) Send(ctx context.Context, msg *JSONRPCMessage) error {
	if !t.started.Load() {
		return fmt.Errorf("transport not started")
	}

	data, err := Encode(msg)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	data = append(data, '\n')
	if _, err := os.Stdout.Write(data); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}

	return nil
}

// Close shuts down the stdio transport.
//
// Returns:
//   - error: always nil for stdio transport
func (t *StdioServerTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started.Load() {
		t.started.Store(false)
		if t.cancel != nil {
			t.cancel()
		}
		// Note: cannot interrupt a blocking scanner.Scan() via context.
		// The read goroutine will exit on next Scan() return (stdin EOF/data).
		// Do not block on eg.Wait() here; the goroutine is self-cleaning.
		slog.Debug("mcp-server: stdio transport closed")
	}

	return nil
}

// Ensure StdioServerTransport implements ServerTransport at compile time.
var _ ServerTransport = (*StdioServerTransport)(nil)

// --- SSEServerTransport ---

// SSEServerTransport implements ServerTransport using HTTP with SSE.
// It serves an SSE endpoint for sending responses to clients and accepts
// POST requests for receiving client requests.
type SSEServerTransport struct {
	addr       string
	server     *http.Server
	requestCh  chan *JSONRPCMessage      // incoming POST requests
	sseClients chan chan *JSONRPCMessage // SSE client streams
	clients    []chan *JSONRPCMessage
	clientsMu  sync.Mutex
	mu         sync.Mutex
	started    bool
	cancel     context.CancelFunc
	eg         errgroup.Group
	srvCtx     context.Context
	closing    atomic.Bool
}

// NewSSEServerTransport creates a new SSE-based server transport.
//
// Args:
//   - addr: listen address (e.g., ":8080" or "localhost:8080")
//
// Returns:
//   - *SSEServerTransport: the new transport instance
func NewSSEServerTransport(addr string) *SSEServerTransport {
	return &SSEServerTransport{
		addr:       addr,
		requestCh:  make(chan *JSONRPCMessage, 64),
		sseClients: make(chan chan *JSONRPCMessage, 16),
	}
}

// Start begins listening for HTTP connections.
//
// Args:
//   - ctx: context for cancellation
//
// Returns:
//   - error: non-nil if the server fails to start or is already started
func (t *SSEServerTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return fmt.Errorf("transport already started")
	}

	t.srvCtx, t.cancel = context.WithCancel(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", t.handleMCP)

	t.server = &http.Server{
		Addr:    t.addr,
		Handler: mux,
	}

	t.eg.Go(func() error {
		if err := t.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("mcp-server: sse server error", "error", err)
			return err
		}
		return nil
	})

	t.started = true
	slog.Info("mcp-server: sse transport started", "addr", t.addr)
	return nil
}

// handleMCP handles both GET (SSE) and POST (requests) at /mcp.
func (t *SSEServerTransport) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		t.handleSSEConnect(w, r)
	case http.MethodPost:
		t.handlePOSTRequest(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSSEConnect handles a new SSE client connection via GET.
func (t *SSEServerTransport) handleSSEConnect(w http.ResponseWriter, r *http.Request) {
	if t.closing.Load() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	msgCh := make(chan *JSONRPCMessage, 64)

	t.clientsMu.Lock()
	t.clients = append(t.clients, msgCh)
	t.clientsMu.Unlock()

	defer func() {
		// Remove this client channel from the active list to prevent
		// Send() from iterating over a closed/stale channel.
		t.removeClient(msgCh)
		// Drain any remaining messages to avoid blocking senders.
		for range msgCh {
		}
	}()

	// Send endpoint event with POST URL.
	postURL := fmt.Sprintf("http://%s/mcp", t.addr)
	_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", postURL)
	flusher.Flush()

	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			data, err := json.Marshal(msg)
			if err != nil {
				slog.Warn("mcp-server: sse marshal error", "error", err)
				continue
			}
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(data))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// handlePOSTRequest handles an incoming JSON-RPC request via POST.
func (t *SSEServerTransport) handlePOSTRequest(w http.ResponseWriter, r *http.Request) {
	var msg JSONRPCMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	select {
	case t.requestCh <- &msg:
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "server busy", http.StatusServiceUnavailable)
	}
}

// Accept returns the next incoming JSON-RPC request from POST.
//
// Args:
//   - ctx: context for cancellation
//
// Returns:
//   - *JSONRPCMessage: the parsed request message
//   - error: non-nil if context is cancelled
func (t *SSEServerTransport) Accept(ctx context.Context) (*JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-t.requestCh:
		return msg, nil
	}
}

// Send sends a response or notification to all connected SSE clients.
//
// Args:
//   - ctx: context for cancellation
//   - msg: the message to broadcast to all SSE clients
//
// Returns:
//   - error: non-nil on context cancellation before delivery
func (t *SSEServerTransport) Send(ctx context.Context, msg *JSONRPCMessage) error {
	if t.closing.Load() {
		return fmt.Errorf("transport is closing")
	}

	t.clientsMu.Lock()
	clients := make([]chan *JSONRPCMessage, len(t.clients))
	copy(clients, t.clients)
	t.clientsMu.Unlock()

	for _, ch := range clients {
		func() {
			defer func() { _ = recover() }()
			select {
			case ch <- msg:
			case <-ctx.Done():
				return
			default:
				// Client buffer full or closed; skip.
			}
		}()
	}

	return nil
}

// removeClient removes a client channel from the active clients list.
// This must be called when an SSE connection is closed to prevent
// stale channel accumulation and goroutine leaks.
func (t *SSEServerTransport) removeClient(ch chan *JSONRPCMessage) {
	t.clientsMu.Lock()
	defer t.clientsMu.Unlock()

	for i, c := range t.clients {
		if c == ch {
			t.clients = append(t.clients[:i], t.clients[i+1:]...)
			break
		}
	}
}

// Close shuts down the SSE transport and HTTP server.
//
// Returns:
//   - error: non-nil if the server fails to shut down gracefully
func (t *SSEServerTransport) Close() error {
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

	t.closing.Store(true)

	if t.server != nil {
		ctx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := t.server.Shutdown(ctx); err != nil {
			slog.Warn("mcp-server: sse shutdown error", "error", err)
		}
	}

	_ = t.eg.Wait()

	close(t.requestCh)

	t.clientsMu.Lock()
	for _, ch := range t.clients {
		close(ch)
	}
	t.clients = nil
	t.clientsMu.Unlock()

	close(t.sseClients)

	slog.Debug("mcp-server: sse transport closed")
	return nil
}

// Ensure SSEServerTransport implements ServerTransport at compile time.
var _ ServerTransport = (*SSEServerTransport)(nil)

// --- Test helper ---

// pipeServerTransport creates a pair of connected transports for testing.
// The writer can be used to simulate client input; the reader receives from it.
type pipeServerTransport struct {
	requestCh  chan *JSONRPCMessage
	responseCh chan *JSONRPCMessage
	mu         sync.Mutex
	started    bool
	cancel     context.CancelFunc
}

// newPipeServerTransport creates a new pipe-based transport for testing.
func newPipeServerTransport() *pipeServerTransport {
	return &pipeServerTransport{
		requestCh:  make(chan *JSONRPCMessage, 64),
		responseCh: make(chan *JSONRPCMessage, 64),
	}
}

// Start prepares the pipe transport.
func (p *pipeServerTransport) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return fmt.Errorf("transport already started")
	}

	_, p.cancel = context.WithCancel(ctx)
	p.started = true
	return nil
}

// Accept returns the next message from the request channel.
func (p *pipeServerTransport) Accept(ctx context.Context) (*JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-p.requestCh:
		return msg, nil
	}
}

// Send puts a message onto the response channel.
func (p *pipeServerTransport) Send(ctx context.Context, msg *JSONRPCMessage) error {
	select {
	case p.responseCh <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close shuts down the pipe transport.
func (p *pipeServerTransport) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return nil
	}
	p.started = false

	if p.cancel != nil {
		p.cancel()
	}

	close(p.requestCh)
	close(p.responseCh)
	return nil
}

// Ensure pipeServerTransport implements ServerTransport at compile time.
var _ ServerTransport = (*pipeServerTransport)(nil)

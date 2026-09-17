// Package mcp provides MCP (Model Context Protocol) server-side transport.
package ares_mcp

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
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
		return errors.New("transport already started")
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
				return errors.New("stdin closed")
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

	log.Debug("mcp-server: stdio transport started")
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
		return nil, errors.New("transport not started")
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
		return errors.New("transport not started")
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

// generateSessionID creates a unique session identifier for SSE client routing.
func generateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID on RNG failure.
		return fmt.Sprintf("sess_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
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
		log.Debug("mcp-server: stdio transport closed")
	}

	return nil
}

// Ensure StdioServerTransport implements ServerTransport at compile time.
var _ ServerTransport = (*StdioServerTransport)(nil)

// --- SSEServerTransport ---

// clientSession represents a single SSE client connection with its session ID.
type clientSession struct {
	id    string
	msgCh chan *JSONRPCMessage
}

// sessionRequest pairs an incoming POST message with its originating session.
type sessionRequest struct {
	msg       *JSONRPCMessage
	sessionID string
}

// SSEServerTransport implements ServerTransport using HTTP with SSE.
// It serves an SSE endpoint for sending responses to clients and accepts
// POST requests for receiving client requests.
type SSEServerTransport struct {
	addr       string
	server     *http.Server
	requestCh  chan *sessionRequest      // incoming POST requests with session info
	sessions   map[string]*clientSession // active sessions by ID
	sessionsMu sync.Mutex
	mu         sync.Mutex
	started    bool
	cancel     context.CancelFunc
	eg         errgroup.Group
	srvCtx     context.Context
	closing    atomic.Bool

	// authToken, when non-empty, is required as "Authorization: Bearer <token>"
	// on every /mcp request (Guarded by mu). Empty leaves the endpoint open,
	// which is only safe behind a loopback bind or an authenticating proxy.
	authToken string

	// currentSession is set by Accept and used by Send to route the response
	// to the correct SSE client.
	currentSession string
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
		addr:      addr,
		requestCh: make(chan *sessionRequest, 64),
		sessions:  make(map[string]*clientSession),
	}
}

// SetAuthToken enables bearer-token auth for the SSE server: when non-empty,
// every /mcp request must present "Authorization: Bearer <token>". Empty (the
// default) keeps the endpoint unauthenticated, which is only safe behind a
// loopback bind or an authenticating reverse proxy — Start logs a warning when
// a non-loopback address is bound without a token.
func (t *SSEServerTransport) SetAuthToken(token string) {
	t.mu.Lock()
	t.authToken = token
	t.mu.Unlock()
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
		return errors.New("transport already started")
	}

	t.srvCtx, t.cancel = context.WithCancel(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", t.handleMCP)

	t.server = &http.Server{
		Addr:              t.addr,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}

	t.eg.Go(func() error {
		if err := t.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("mcp-server: sse server error", "error", err)
			return err
		}
		return nil
	})

	t.started = true
	if t.authToken == "" && !isLoopbackAddr(t.addr) {
		log.Warn("mcp-server: sse transport bound to a non-loopback address without an auth token; /mcp is unauthenticated (call SetAuthToken)",
			"addr", t.addr)
	}
	log.Info("mcp-server: sse transport started", "addr", t.addr)
	return nil
}

// handleMCP handles both GET (SSE) and POST (requests) at /mcp.
func (t *SSEServerTransport) handleMCP(w http.ResponseWriter, r *http.Request) {
	if !t.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		t.handleSSEConnect(w, r)
	case http.MethodPost:
		t.handlePOSTRequest(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// authorized enforces the optional bearer token set by SetAuthToken. With no
// token configured the endpoint stays open; with one, a constant-time compare
// guards both the GET (SSE connect) and POST (request) paths.
func (t *SSEServerTransport) authorized(r *http.Request) bool {
	t.mu.Lock()
	token := t.authToken
	t.mu.Unlock()
	if token == "" {
		return true
	}
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, prefix)), []byte(token)) == 1
}

// isLoopbackAddr reports whether addr binds only the loopback interface. An
// empty host (":port") or a wildcard address ("0.0.0.0"/"::") binds all
// interfaces and returns false — the case the Start warning keys on.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

// safeRequestHost returns the request Host header when it is a syntactically
// valid host[:port] that cannot break out of an SSE data frame, and the bound
// listen address otherwise.
//
// The Host header is attacker-controlled. Writing it straight into the
// endpoint event let a crafted value inject CR/LF and forge extra SSE frames,
// or smuggle markup into a client that renders the stream (gosec G705). The
// fallback keeps the bare-":port" bind working: an empty or rejected header
// degrades to the address the transport actually bound, never to a forged one.
func safeRequestHost(hostHeader, fallback string) string {
	if hostHeader == "" {
		return fallback
	}
	// Frame-breakers and URL/markup delimiters are rejected wholesale rather
	// than escaped: none of them belong in a host, so escaping would only
	// preserve a value the client could never use anyway. Brackets are left
	// out of this set on purpose — an IPv6 literal needs them — and are
	// handled by the hostname check below instead.
	if strings.ContainsAny(hostHeader, " \t\r\n/\\?#@'\"<>`|{}^") {
		return fallback
	}
	hostname := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		hostname = h
	}
	hostname = strings.Trim(hostname, "[]")
	if hostname == "" {
		return fallback
	}
	if net.ParseIP(hostname) != nil {
		return hostHeader
	}
	for _, r := range hostname {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' {
			continue
		}
		return fallback
	}
	return hostHeader
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
	sessionID := generateSessionID()
	sess := &clientSession{id: sessionID, msgCh: msgCh}

	t.sessionsMu.Lock()
	t.sessions[sessionID] = sess
	t.sessionsMu.Unlock()

	defer func() {
		t.sessionsMu.Lock()
		delete(t.sessions, sessionID)
		t.sessionsMu.Unlock()
	}()

	// Send endpoint event with session-scoped POST URL. Derive the scheme and
	// host from the request: hardcoding "http://<addr>" advertised http even
	// behind TLS and, for a bare ":port" bind, a URL with no host at all. The
	// Host header is untrusted, so it is validated before it reaches the
	// frame (see safeRequestHost).
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := safeRequestHost(r.Host, t.addr)
	postURL := fmt.Sprintf("%s://%s/mcp?session_id=%s", scheme, host, sessionID)
	// #nosec G705 — gosec's taint analysis cannot see that safeRequestHost
	// already rejected every frame-breaker and non-host character above. Only
	// a validated host[:port] plus a server-generated session id reaches the
	// frame, and SSE consumers parse this field as a URL, not as markup.
	if _, err := fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", postURL); err != nil {
		log.Warn("mcp-server: sse write endpoint error", "error", err)
		return
	}
	flusher.Flush()

	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			data, err := json.Marshal(msg)
			if err != nil {
				log.Warn("mcp-server: sse marshal error", "error", err)
				continue
			}
			if _, err := fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(data)); err != nil {
				log.Warn("mcp-server: sse write message error", "error", err)
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// handlePOSTRequest handles an incoming JSON-RPC request via POST.
func (t *SSEServerTransport) handlePOSTRequest(w http.ResponseWriter, r *http.Request) {
	// Reject requests during shutdown.
	if t.closing.Load() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}

	var msg JSONRPCMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&msg); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Extract session ID from query parameter for response routing.
	sessionID := r.URL.Query().Get("session_id")

	select {
	case t.requestCh <- &sessionRequest{msg: &msg, sessionID: sessionID}:
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "server busy", http.StatusServiceUnavailable)
	}
}

// Accept returns the next incoming JSON-RPC request from POST.
func (t *SSEServerTransport) Accept(ctx context.Context) (*JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case sr := <-t.requestCh:
		// Store the session ID so Send can route the response.
		t.sessionsMu.Lock()
		t.currentSession = sr.sessionID
		t.sessionsMu.Unlock()
		return sr.msg, nil
	}
}

// Send sends a response to the SSE client that originated the current request.
// If the client has disconnected (session not found), the response is silently
// dropped to avoid accidentally broadcasting to other clients.
func (t *SSEServerTransport) Send(ctx context.Context, msg *JSONRPCMessage) error {
	if t.closing.Load() {
		return errors.New("transport is closing")
	}

	t.sessionsMu.Lock()
	sessionID := t.currentSession
	sess, ok := t.sessions[sessionID]
	if !ok || sess == nil {
		// Client disconnected — drop silently instead of broadcasting.
		t.sessionsMu.Unlock()
		return nil
	}

	// Send while holding the sessions lock so this is atomic with Close's
	// channel close: a send racing a close panics with
	// "send on closed channel". The non-blocking select keeps the
	// critical section short.
	select {
	case sess.msgCh <- msg:
	case <-ctx.Done():
		t.sessionsMu.Unlock()
		return ctx.Err()
	default:
		// Client buffer full: report the failure instead of silently
		// dropping the response, which would leave the caller waiting
		// forever for a reply that never arrives.
		t.sessionsMu.Unlock()
		return fmt.Errorf("sse: client buffer full, response for session %q dropped", sessionID)
	}
	t.sessionsMu.Unlock()
	return nil
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
			log.Warn("mcp-server: sse shutdown error", "error", err)
		}
	}

	if err := t.eg.Wait(); err != nil {
		log.Warn("mcp-server: sse transport goroutine error", "error", err)
	}

	// Do NOT close t.requestCh: a concurrent handlePOSTRequest goroutine could
	// still be sending, and send-on-closed-channel panics. The channel will be
	// garbage collected once the transport is unreferenced. Accept() exits via
	// its context cancellation, not channel close.

	// Clean up any remaining sessions.
	t.sessionsMu.Lock()
	for _, sess := range t.sessions {
		close(sess.msgCh)
	}
	t.sessions = nil
	t.sessionsMu.Unlock()

	log.Debug("mcp-server: sse transport closed")
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
		return errors.New("transport already started")
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

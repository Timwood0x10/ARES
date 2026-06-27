package ares_mcp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// Default timeout for MCP client operations when no config timeout is specified.
const defaultClientTimeout = 30 * time.Second

// MCPClientConfig holds configuration for creating an MCPClient.
type MCPClientConfig struct {
	ServerName string
	Transport  TransportConfig
	Timeout    time.Duration
	OnChange   func()
}

// TransportConfig selects and configures a transport.
type TransportConfig struct {
	Type  string       `yaml:"type" json:"type"`
	Stdio *StdioConfig `yaml:"stdio,omitempty" json:"stdio,omitempty"`
	SSE   *SSEConfig   `yaml:"sse,omitempty" json:"sse,omitempty"`
}

// MCPClient manages a connection to a single MCP server.
type MCPClient struct {
	transport  Transport
	serverName string
	serverCaps *ServerCapabilities
	tools      map[string]*MCPToolDef
	mu         sync.RWMutex
	nextID     IDGenerator
	pending    map[int64]chan *JSONRPCMessage
	pendingMu  sync.Mutex
	onChange   func()
	timeout    time.Duration
	ctx        context.Context
	cancel     context.CancelFunc
	eg         errgroup.Group
	connected  atomic.Bool
}

// NewMCPClient creates a new MCPClient with the given config.
func NewMCPClient(config MCPClientConfig) *MCPClient {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = defaultClientTimeout
	}

	return &MCPClient{
		serverName: config.ServerName,
		tools:      make(map[string]*MCPToolDef),
		pending:    make(map[int64]chan *JSONRPCMessage),
		onChange:   config.OnChange,
		timeout:    timeout,
	}
}

// Connect starts the transport and performs the MCP initialize handshake.
func (c *MCPClient) Connect(ctx context.Context, transport Transport) error {
	if transport == nil {
		return fmt.Errorf("transport is required")
	}

	c.transport = transport
	c.ctx, c.cancel = context.WithCancel(ctx)

	if err := c.transport.Start(c.ctx); err != nil {
		return fmt.Errorf("start transport: %w", err)
	}

	// Start receiving messages in background.
	c.eg.Go(func() error {
		return c.receiveLoop()
	})

	// Perform initialize handshake.
	if err := c.initialize(); err != nil {
		if closeErr := c.Close(); closeErr != nil {
			log.Warn("mcp: close after init failure", "error", closeErr)
		}
		return fmt.Errorf("initialize: %w", err)
	}

	c.connected.Store(true)

	// Discover initial tools.
	if _, err := c.ListTools(ctx); err != nil {
		if closeErr := c.Close(); closeErr != nil {
			log.Warn("mcp: close after list tools failure", "error", closeErr)
		}
		return fmt.Errorf("list tools: %w", err)
	}

	return nil
}

// initialize performs the MCP initialize handshake.
func (c *MCPClient) initialize() error {
	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo: Implementation{
			Name:    ClientName,
			Version: ClientVersion,
		},
		Capabilities: ClientCapabilities{
			Tools: &ToolClientCapabilities{
				ListChanged: true,
			},
		},
	}

	var result InitializeResult
	if err := c.call(context.Background(), MethodInitialize, params, &result); err != nil {
		return fmt.Errorf("initialize call: %w", err)
	}

	c.mu.Lock()
	c.serverCaps = &result.Capabilities
	c.mu.Unlock()

	// Send initialized notification.
	notif, err := NewNotification(NotificationInitialized, nil)
	if err != nil {
		return fmt.Errorf("create initialized notification: %w", err)
	}

	if err := c.transport.Send(context.Background(), notif); err != nil {
		return fmt.Errorf("send initialized: %w", err)
	}

	return nil
}

// ListTools requests the list of tools from the server.
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolDef, error) {
	var result ToolsListResult
	if err := c.call(ctx, MethodToolsList, nil, &result); err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	c.mu.Lock()
	c.tools = make(map[string]*MCPToolDef, len(result.Tools))
	for i := range result.Tools {
		c.tools[result.Tools[i].Name] = &result.Tools[i]
	}
	c.mu.Unlock()

	return result.Tools, nil
}

// CallTool invokes a tool on the server.
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
	params := ToolCallParams{
		Name:      name,
		Arguments: args,
	}

	var result ToolCallResult
	if err := c.call(ctx, MethodToolsCall, params, &result); err != nil {
		return nil, fmt.Errorf("call tool %s: %w", name, err)
	}

	return &result, nil
}

// ServerName returns the configured server name.
func (c *MCPClient) ServerName() string {
	return c.serverName
}

// ServerCapabilities returns the server's declared capabilities.
func (c *MCPClient) ServerCapabilities() *ServerCapabilities {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverCaps
}

// GetTool returns a tool definition by name.
func (c *MCPClient) GetTool(name string) (*MCPToolDef, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.tools[name]
	return t, ok
}

// ToolCount returns the number of discovered tools.
func (c *MCPClient) ToolCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.tools)
}

// IsConnected returns true if the client is connected.
func (c *MCPClient) IsConnected() bool {
	return c.connected.Load()
}

// Close shuts down the client and transport.
func (c *MCPClient) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.connected.Store(false)

	if c.transport != nil {
		if err := c.transport.Close(); err != nil {
			log.Warn("mcp: transport close error", "server", c.serverName, "error", err)
		}
	}

	if err := c.eg.Wait(); err != nil && c.ctx.Err() == nil {
		log.Error("mcp: receive loop error", "server", c.serverName, "error", err)
	}

	// Close all pending channels.
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()

	return nil
}

// call sends a request and waits for the correlated response.
func (c *MCPClient) call(ctx context.Context, method string, params interface{}, result interface{}) error {
	if !c.connected.Load() && method != MethodInitialize {
		return fmt.Errorf("not connected")
	}

	id := c.nextID.Next()

	msg, err := NewRequest(id, method, params)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Register pending channel before sending.
	ch := make(chan *JSONRPCMessage, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.transport.Send(ctx, msg); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	// Check parent context before applying timeout.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Wait for response with timeout.
	callCtx, callCancel := context.WithTimeout(ctx, c.timeout)
	defer callCancel()

	select {
	case <-callCtx.Done():
		return fmt.Errorf("timeout waiting for response to %s: %w", method, callCtx.Err())
	case resp, ok := <-ch:
		if !ok {
			return fmt.Errorf("connection closed waiting for %s", method)
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			if err := DecodeResult(resp, result); err != nil {
				return fmt.Errorf("decode result: %w", err)
			}
		}
		return nil
	}
}

// receiveLoop reads messages from the transport and dispatches them.
func (c *MCPClient) receiveLoop() error {
	for {
		msg, err := c.transport.Receive(c.ctx)
		if err != nil {
			if c.ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("receive: %w", err)
		}

		switch {
		case IsResponse(msg) || IsError(msg):
			c.dispatchResponse(msg)
		case IsNotification(msg):
			c.handleNotification(msg)
		}
	}
}

// dispatchResponse routes a response to its pending channel.
func (c *MCPClient) dispatchResponse(msg *JSONRPCMessage) {
	if msg.ID == nil {
		return
	}

	c.pendingMu.Lock()
	ch, ok := c.pending[*msg.ID]
	if ok {
		// Remove from pending map; we now own the response delivery.
		delete(c.pending, *msg.ID)
	}
	c.pendingMu.Unlock()

	if ok {
		select {
		case ch <- msg:
		default:
			// Channel full means caller already stopped waiting (timeout/cancel).
			// Discard the stale response.
		}
	}
}

// handleNotification processes server notifications.
func (c *MCPClient) handleNotification(msg *JSONRPCMessage) {
	switch msg.Method {
	case NotificationToolsListChanged:
		// Re-fetch tools list.
		ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
		defer cancel()

		if _, err := c.ListTools(ctx); err == nil && c.onChange != nil {
			c.onChange()
		}
	}
}

// NewTransportFromConfig creates a Transport from a TransportConfig.
func NewTransportFromConfig(config TransportConfig) (Transport, error) {
	switch config.Type {
	case "stdio":
		if config.Stdio == nil {
			return nil, fmt.Errorf("stdio config is required")
		}
		return NewStdioTransport(*config.Stdio), nil
	case "sse":
		if config.SSE == nil {
			return nil, fmt.Errorf("sse config is required")
		}
		return NewSSETransport(*config.SSE), nil
	default:
		return nil, fmt.Errorf("unsupported transport type: %s", config.Type)
	}
}

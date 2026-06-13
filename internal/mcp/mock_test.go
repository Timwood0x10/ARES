package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// mockTransport implements Transport for testing.
// It uses a response function that generates responses for each request.
type mockTransport struct {
	mu      sync.Mutex
	started bool
	closed  bool
	respFn  func(msg *JSONRPCMessage) *JSONRPCMessage
	respCh  chan *JSONRPCMessage
	sent    []*JSONRPCMessage
	sendErr error
	recvErr error
}

func newMockTransport(responses ...*JSONRPCMessage) *mockTransport {
	ch := make(chan *JSONRPCMessage, len(responses)+8)
	for _, r := range responses {
		ch <- r
	}
	return &mockTransport{
		respCh: ch,
	}
}

// newMockServer creates a mock transport that responds to each request.
func newMockServer(handler func(msg *JSONRPCMessage) *JSONRPCMessage) *mockTransport {
	return &mockTransport{
		respFn: handler,
		respCh: make(chan *JSONRPCMessage, 16),
	}
}

func (t *mockTransport) Start(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return fmt.Errorf("already started")
	}
	t.started = true
	return nil
}

func (t *mockTransport) Send(_ context.Context, msg *JSONRPCMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sendErr != nil {
		return t.sendErr
	}
	t.sent = append(t.sent, msg)

	// If we have a response function, generate and queue the response.
	if t.respFn != nil {
		resp := t.respFn(msg)
		if resp != nil {
			t.respCh <- resp
		}
	}

	return nil
}

func (t *mockTransport) Receive(_ context.Context) (*JSONRPCMessage, error) {
	if t.recvErr != nil {
		return nil, t.recvErr
	}
	msg, ok := <-t.respCh
	if !ok {
		return nil, fmt.Errorf("channel closed")
	}
	return msg, nil
}

func (t *mockTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	close(t.respCh)
	return nil
}

func (t *mockTransport) Sent() []*JSONRPCMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]*JSONRPCMessage, len(t.sent))
	copy(result, t.sent)
	return result
}

// buildInitializeResult creates a mock initialize response.
func buildInitializeResult() *JSONRPCMessage {
	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo: Implementation{
			Name:    "mock-server",
			Version: "1.0.0",
		},
		Capabilities: ServerCapabilities{
			Tools: &ToolServerCapabilities{ListChanged: true},
		},
	}
	data, _ := json.Marshal(result)
	return &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      int64Ptr(1),
		Result:  data,
	}
}

// buildToolsListResult creates a mock tools/list response.
func buildToolsListResult(tools []MCPToolDef) *JSONRPCMessage {
	result := ToolsListResult{Tools: tools}
	data, _ := json.Marshal(result)
	return &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      int64Ptr(2),
		Result:  data,
	}
}

// buildToolCallResult creates a mock tools/call response.
func buildToolCallResult(text string) *JSONRPCMessage {
	result := ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
	data, _ := json.Marshal(result)
	return &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      int64Ptr(3),
		Result:  data,
	}
}

func buildSimpleToolDef(name, desc string) MCPToolDef {
	return MCPToolDef{
		Name:        name,
		Description: desc,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`),
	}
}

package mcp

import (
	"context"
	"testing"
)

func TestNewStdioTransport(t *testing.T) {
	tr := NewStdioTransport(StdioConfig{Command: "echo"})
	if tr == nil {
		t.Fatal("NewStdioTransport returned nil")
	}
}

func TestStdioTransportStartEmptyCommand(t *testing.T) {
	tr := NewStdioTransport(StdioConfig{})
	err := tr.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for empty command, got nil")
	}
}

func TestStdioTransportDoubleStart(t *testing.T) {
	// Use a command that stays alive so we can test double-start.
	tr := NewStdioTransport(StdioConfig{Command: "cat"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	err := tr.Start(ctx)
	if err == nil {
		t.Fatal("expected error for double start, got nil")
	}
}

func TestStdioTransportCloseUnstarted(t *testing.T) {
	tr := NewStdioTransport(StdioConfig{Command: "echo"})
	// Close on an unstarted transport should be a no-op.
	if err := tr.Close(); err != nil {
		t.Errorf("Close on unstarted: %v", err)
	}
}

func TestStdioTransportSendBeforeStart(t *testing.T) {
	tr := NewStdioTransport(StdioConfig{Command: "echo"})
	msg := &JSONRPCMessage{JSONRPC: JSONRPCVersion, Method: "test"}
	err := tr.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for send before start, got nil")
	}
}

func TestStdioTransportReceiveBeforeStart(t *testing.T) {
	tr := NewStdioTransport(StdioConfig{Command: "echo"})
	_, err := tr.Receive(context.Background())
	if err == nil {
		t.Fatal("expected error for receive before start, got nil")
	}
}

func TestStdioTransportStartWithInvalidCommand(t *testing.T) {
	tr := NewStdioTransport(StdioConfig{Command: "/nonexistent/binary/that/does/not/exist"})
	err := tr.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid command, got nil")
	}
}

func TestNewSSETransport(t *testing.T) {
	tr := NewSSETransport(SSEConfig{URL: "http://localhost:0"})
	if tr == nil {
		t.Fatal("NewSSETransport returned nil")
	}
}

func TestSSETransportStartEmptyURL(t *testing.T) {
	tr := NewSSETransport(SSEConfig{})
	err := tr.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for empty URL, got nil")
	}
}

func TestSSETransportDoubleStart(t *testing.T) {
	// Start with a URL that won't be connected to (we just test the guard).
	tr := NewSSETransport(SSEConfig{URL: "http://localhost:0"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	err := tr.Start(ctx)
	if err == nil {
		t.Fatal("expected error for double start, got nil")
	}
}

func TestSSETransportCloseUnstarted(t *testing.T) {
	tr := NewSSETransport(SSEConfig{URL: "http://localhost:0"})
	if err := tr.Close(); err != nil {
		t.Errorf("Close on unstarted: %v", err)
	}
}

func TestSSETransportSendBeforeStart(t *testing.T) {
	tr := NewSSETransport(SSEConfig{URL: "http://localhost:0"})
	msg := &JSONRPCMessage{JSONRPC: JSONRPCVersion, Method: "test"}
	err := tr.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for send before start, got nil")
	}
}

func TestSSETransportDefaultTimeout(t *testing.T) {
	tr := NewSSETransport(SSEConfig{URL: "http://localhost:0"})
	if tr.httpClient.Timeout == 0 {
		t.Error("expected default timeout to be set")
	}
}

func TestSSETransportCustomTimeout(t *testing.T) {
	tr := NewSSETransport(SSEConfig{URL: "http://localhost:0", Timeout: 5000000000}) // 5s
	if tr.httpClient.Timeout != 5000000000 {
		t.Errorf("timeout = %v, want 5s", tr.httpClient.Timeout)
	}
}

func TestNewTransportFromConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  TransportConfig
		wantErr bool
	}{
		{
			name: "valid stdio",
			config: TransportConfig{
				Type: "stdio",
				Stdio: &StdioConfig{
					Command: "echo",
				},
			},
			wantErr: false,
		},
		{
			name: "valid sse",
			config: TransportConfig{
				Type: "sse",
				SSE: &SSEConfig{
					URL: "http://localhost:0",
				},
			},
			wantErr: false,
		},
		{
			name: "stdio without config",
			config: TransportConfig{
				Type: "stdio",
			},
			wantErr: true,
		},
		{
			name: "sse without config",
			config: TransportConfig{
				Type: "sse",
			},
			wantErr: true,
		},
		{
			name: "unsupported type",
			config: TransportConfig{
				Type: "websocket",
			},
			wantErr: true,
		},
		{
			name:    "empty type",
			config:  TransportConfig{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, err := NewTransportFromConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && tr == nil {
				t.Error("expected non-nil transport")
			}
		})
	}
}

func TestNewTransportFromConfigReturnsCorrectType(t *testing.T) {
	tr, err := NewTransportFromConfig(TransportConfig{
		Type:  "stdio",
		Stdio: &StdioConfig{Command: "echo"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := tr.(*StdioTransport); !ok {
		t.Errorf("expected *StdioTransport, got %T", tr)
	}

	tr, err = NewTransportFromConfig(TransportConfig{
		Type: "sse",
		SSE:  &SSEConfig{URL: "http://localhost:0"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := tr.(*SSETransport); !ok {
		t.Errorf("expected *SSETransport, got %T", tr)
	}
}

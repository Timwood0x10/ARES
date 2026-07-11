// Package mcp is the official MCP (Model Context Protocol) adapter for ARES.
//
// It exposes ARES agents/tools over the MCP wire format so MCP-compatible
// clients (Claude Desktop, IDE plugins, …) can call into ARES.
//
// This is a placeholder skeleton. The real adapter will bind to the MCP
// protocol implementation; the stub returns ErrNotImplemented.
package mcp

import (
	"context"
	"errors"

	"github.com/Timwood0x10/ares/compat/protocol"
)

// ErrNotImplemented is returned by stub methods until the full binding is wired.
var ErrNotImplemented = errors.New("compat/protocol/mcp: not implemented yet")

// Adapter satisfies compat/protocol.ProtocolAdapter for MCP.
type Adapter struct{}

// New constructs an Adapter from a raw config map (currently unused).
func New(_ map[string]any) (*Adapter, error) { return &Adapter{}, nil }

// Serve handles a single inbound MCP request and returns the encoded response.
func (*Adapter) Serve(_ context.Context, _ []byte) ([]byte, error) {
	return nil, ErrNotImplemented
}

// Name returns the canonical protocol name.
func (*Adapter) Name() string { return "mcp" }

// ContentType returns the MIME type this adapter produces.
func (*Adapter) ContentType() string { return "application/json" }

// Compile-time interface assertion.
var _ protocol.ProtocolAdapter = (*Adapter)(nil)

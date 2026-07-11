// Package openaiapi is the official OpenAI-API-compatible protocol adapter for ARES.
//
// It exposes ARES agents/tools over the OpenAI Chat Completions wire format
// so any OpenAI-API-compatible client plugs into the ARES runtime.
//
// This is a placeholder skeleton. The real adapter will bind to the existing
// api/handler layer; the stub returns ErrNotImplemented.
package openaiapi

import (
	"context"
	"errors"

	"github.com/Timwood0x10/ares/compat/protocol"
)

// ErrNotImplemented is returned by stub methods until the full binding is wired.
var ErrNotImplemented = errors.New("compat/protocol/openai_api: not implemented yet")

// Adapter satisfies compat/protocol.ProtocolAdapter for the OpenAI API format.
type Adapter struct{}

// New constructs an Adapter from a raw config map (currently unused).
func New(_ map[string]any) (*Adapter, error) { return &Adapter{}, nil }

// Serve handles a single inbound OpenAI-format request and returns the encoded response.
func (*Adapter) Serve(_ context.Context, _ []byte) ([]byte, error) {
	return nil, ErrNotImplemented
}

// Name returns the canonical protocol name.
func (*Adapter) Name() string { return "openai_api" }

// ContentType returns the MIME type this adapter produces.
func (*Adapter) ContentType() string { return "application/json" }

// Compile-time interface assertion.
var _ protocol.ProtocolAdapter = (*Adapter)(nil)

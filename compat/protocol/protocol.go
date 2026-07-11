// Package protocol is the wire-protocol compatibility layer of ARES.
//
// It defines the unified ProtocolAdapter interface that all wire-protocol
// adapters implement, and hosts the official MCP and OpenAI-API adapters.
// Third-party protocol plugins register via compat.RegisterProtocol.
package protocol

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ProtocolAdapter is the unified interface for wire-protocol bridges.
//
// Implementations are registered via Register and looked up by name.
// Official implementations: "openai_api", "mcp". Third-party plugins may
// register additional protocols (http, grpc, trpc, …).
//
// A protocol adapter exposes ARES capabilities (agents, tools, retrieval)
// over a third-party wire format, so external clients can call into ARES
// using a protocol they already speak. Each adapter handles request decoding,
// dispatch to the appropriate ARES service, and response encoding.
type ProtocolAdapter interface {
	// Serve handles a single inbound request and returns the encoded response.
	// raw is the protocol-specific request payload (JSON, protobuf, …).
	// Returns the protocol-specific response payload or an error.
	Serve(ctx context.Context, raw []byte) ([]byte, error)

	// Name returns the canonical protocol name (e.g. "openai_api", "mcp").
	Name() string

	// ContentType returns the MIME type this adapter produces (e.g. "application/json").
	ContentType() string
}

// Factory constructs a ProtocolAdapter from a raw config map.
// The config schema is protocol-specific; official adapters document theirs.
type Factory func(config map[string]any) (ProtocolAdapter, error)

// Registry holds all registered protocol adapters by name.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Factory
}

// NewRegistry creates an empty protocol registry.
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Factory)}
}

// Register registers a protocol adapter factory by name.
// Returns an error if name is empty, factory is nil, or name is already registered.
func (r *Registry) Register(name string, factory Factory) error {
	if name == "" {
		return errors.New("compat/protocol: name must not be empty")
	}
	if factory == nil {
		return fmt.Errorf("compat/protocol: factory must not be nil for %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[name]; exists {
		return fmt.Errorf("compat/protocol: %q already registered", name)
	}
	r.adapters[name] = factory
	return nil
}

// Lookup returns the factory registered under name, or ErrNotFound.
func (r *Registry) Lookup(name string) (Factory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.adapters[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return f, nil
}

// Names returns all registered adapter names in arbitrary order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.adapters))
	for k := range r.adapters {
		names = append(names, k)
	}
	return names
}

// ErrNotFound is the sentinel returned by Lookup for unknown protocols.
var ErrNotFound = errors.New("compat/protocol: adapter not found")

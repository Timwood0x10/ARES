// Package compat is the ARES Compatibility Layer — the ecosystem entry point.
//
// ARES is "evolution included", not "batteries included". The compat layer
// provides the thin adapters that bind third-party ecosystem components
// (LLM providers, vector DBs, document loaders, wire protocols, tool shells)
// into ARES's internal runtime. ARES officially maintains only the 20% of
// components that 80% of users need (OpenAI, Ollama, pgvector, Markdown/PDF,
// MCP); everything else is a third-party plugin registered via the helpers
// in this package.
//
// Directory layout (per next_step.md):
//
//	compat/
//	    llm/        — LLM provider adapters (openai, ollama, anthropic, …)
//	    vector/     — Vector store adapters (pgvector, chroma, qdrant, …)
//	    loader/     — Document loaders (markdown, pdf, html, …)
//	    protocol/   — Wire protocol adapters (openai_api, mcp, http)
//	    tool/       — Tool registry and builtin tool adapters
//
// Registration entry points:
//
//	compat.RegisterLLM(name, factory)
//	compat.RegisterVector(name, factory)
//	compat.RegisterLoader(name, factory)
//	compat.RegisterProtocol(name, factory)
//	compat.RegisterTool(name, factory)
//
// Each subsystem keeps its own typed registry; this package only holds the
// shared registry plumbing and the top-level documentation.
package compat

import (
	"fmt"
	"sync"
)

// factory is the internal shape every per-subsystem factory registry uses.
// It is intentionally generic — each subsystem wraps it with a typed façade.
type factory = any

// registry is a thread-safe name→factory map shared by all subsystem registries.
type registry struct {
	mu      sync.RWMutex
	entries map[string]factory
}

func newRegistry() *registry {
	return &registry{entries: make(map[string]factory)}
}

func (r *registry) register(name string, f factory) error {
	if name == "" {
		return fmt.Errorf("compat: name must not be empty")
	}
	if f == nil {
		return fmt.Errorf("compat: factory must not be nil for %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("compat: %q already registered", name)
	}
	r.entries[name] = f
	return nil
}

func (r *registry) lookup(name string) (factory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.entries[name]
	return f, ok
}

func (r *registry) list() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for k := range r.entries {
		names = append(names, k)
	}
	return names
}

// Sentinel errors for the compat layer. Subsystems wrap these with context.
var (
	// ErrNotFound is returned when a requested component is not registered.
	ErrNotFound = fmt.Errorf("compat: component not found")
	// ErrAlreadyRegistered is returned when registering a duplicate name.
	ErrAlreadyRegistered = fmt.Errorf("compat: component already registered")
)

// Package llm is the LLM compatibility layer of ARES.
//
// It defines the unified LLMProvider interface that all LLM adapters implement,
// and hosts the official adapters maintained by the ARES team (OpenAI, Ollama).
// Third-party LLM plugins register via compat.RegisterLLM.
package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// LLMProvider is the unified interface for LLM inference services in ARES.
//
// Implementations are registered via RegisterLLM and looked up by name.
// Official implementations: "openai", "ollama". Third-party plugins may
// register additional providers (anthropic, gemini, qwen, …).
//
// The interface is deliberately minimal — it mirrors the subset of
// internal/llm.Client behavior that runtime components depend on:
// Generate, IsEnabled, GetProvider. Streaming is a future extension.
type LLMProvider interface {
	// Generate produces a single completion from the given prompt.
	Generate(ctx context.Context, prompt string) (string, error)

	// IsEnabled reports whether the provider is properly configured and usable.
	// A disabled provider returns false without error; callers should skip it.
	IsEnabled() bool

	// GetProvider returns the provider's canonical name (e.g. "openai", "ollama").
	GetProvider() string
}

// Factory constructs an LLMProvider from a raw config map.
// The config schema is provider-specific; official adapters document theirs.
type Factory func(config map[string]any) (LLMProvider, error)

// Registry holds all registered LLM providers by name.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Factory
}

// NewRegistry creates an empty LLM registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Factory)}
}

// Register registers an LLM provider factory by name.
// Returns an error if name is empty, factory is nil, or name is already registered.
func (r *Registry) Register(name string, factory Factory) error {
	if name == "" {
		return errors.New("compat/llm: name must not be empty")
	}
	if factory == nil {
		return fmt.Errorf("compat/llm: factory must not be nil for %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("compat/llm: %q already registered", name)
	}
	r.providers[name] = factory
	return nil
}

// Lookup returns the factory registered under name, or ErrNotFound.
func (r *Registry) Lookup(name string) (Factory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return f, nil
}

// Names returns all registered provider names in arbitrary order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for k := range r.providers {
		names = append(names, k)
	}
	return names
}

// ErrNotFound is the sentinel returned by Lookup for unknown providers.
var ErrNotFound = errors.New("compat/llm: provider not found")

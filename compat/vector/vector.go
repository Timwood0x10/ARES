// Package vector is the vector-store compatibility layer of ARES.
//
// It defines the unified VectorStore interface that all vector DB adapters
// implement, and hosts the official pgvector adapter. Third-party vector
// plugins (chroma, qdrant, sqlitevec, …) register via compat.RegisterVector.
package vector

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// VectorStore is the unified interface for vector similarity search backends.
//
// Implementations are registered via Register and looked up by name.
// Official implementation: "pgvector". Third-party plugins may register
// additional backends (chroma, qdrant, sqlitevec, …).
//
// The interface mirrors the subset of internal/storage/postgres/embedding
// behavior that retrieval depends on: Embed-via-Search, batch search, and
// a low-level Upsert for the write buffer. Health/Close are lifecycle hooks.
type VectorStore interface {
	// Search returns the top-k nearest neighbors for the given query vector,
	// filtered to tenantID. Each result includes the stored ID, its raw content,
	// a similarity score in [0,1], and arbitrary metadata.
	Search(ctx context.Context, query []float64, tenantID string, topK int) ([]Result, error)

	// Upsert inserts or updates a batch of vectors identified by ID.
	Upsert(ctx context.Context, tenantID string, items []Item) error

	// HealthCheck reports whether the backend is reachable and usable.
	HealthCheck(ctx context.Context) error

	// Close releases backend-specific resources (connections, buffers).
	Close() error
}

// Result is a single vector similarity search hit.
type Result struct {
	ID       string
	Content  string
	Score    float64
	Metadata map[string]string
}

// Item is a single vector upsert payload.
type Item struct {
	ID       string
	Vector   []float64
	Content  string
	Metadata map[string]string
}

// Factory constructs a VectorStore from a raw config map.
// The config schema is backend-specific; official adapters document theirs.
type Factory func(config map[string]any) (VectorStore, error)

// Registry holds all registered vector backends by name.
type Registry struct {
	mu       sync.RWMutex
	backends map[string]Factory
}

// NewRegistry creates an empty vector registry.
func NewRegistry() *Registry {
	return &Registry{backends: make(map[string]Factory)}
}

// Register registers a vector backend factory by name.
// Returns an error if name is empty, factory is nil, or name is already registered.
func (r *Registry) Register(name string, factory Factory) error {
	if name == "" {
		return errors.New("compat/vector: name must not be empty")
	}
	if factory == nil {
		return fmt.Errorf("compat/vector: factory must not be nil for %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.backends[name]; exists {
		return fmt.Errorf("compat/vector: %q already registered", name)
	}
	r.backends[name] = factory
	return nil
}

// Lookup returns the factory registered under name, or ErrNotFound.
func (r *Registry) Lookup(name string) (Factory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.backends[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return f, nil
}

// Names returns all registered backend names in arbitrary order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.backends))
	for k := range r.backends {
		names = append(names, k)
	}
	return names
}

// ErrNotFound is the sentinel returned by Lookup for unknown backends.
var ErrNotFound = errors.New("compat/vector: backend not found")

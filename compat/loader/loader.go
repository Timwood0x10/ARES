// Package loader is the document-loader compatibility layer of ARES.
//
// It defines the unified DocumentLoader interface that all format adapters
// implement, and hosts the official Markdown/HTML/PDF adapters. Third-party
// loader plugins (Office, LaTeX, CAD, …) register via compat.RegisterLoader.
package loader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Document is the unified output of every loader: raw text plus metadata.
type Document struct {
	// Source identifies the input (file path, URL, object key).
	Source string
	// Text is the extracted plain-text content, suitable for embedding.
	Text string
	// Metadata holds optional format-specific fields (title, author, page count, …).
	Metadata map[string]string
}

// DocumentLoader is the unified interface for format-specific document ingestion.
//
// Implementations are registered via Register and looked up by name.
// Official implementations: "markdown", "html", "pdf". Third-party plugins
// may register additional formats (docx, latex, dwg, …).
//
// A loader takes a readable byte stream plus a source identifier, extracts
// plain text, and returns a Document. Chunking is the caller's responsibility;
// loaders produce one Document per input stream.
type DocumentLoader interface {
	// Load reads all bytes from r, extracts plain text, and returns a Document.
	// source is stored on the Document for traceability and may be empty.
	Load(ctx context.Context, source string, r io.Reader) (*Document, error)

	// Name returns the canonical format name (e.g. "markdown", "pdf").
	Name() string

	// Extensions returns the file extensions this loader handles (e.g. [".md", ".markdown"]).
	// Used by auto-detection; order is unspecified.
	Extensions() []string
}

// Factory constructs a DocumentLoader from a raw config map.
// The config schema is format-specific; official adapters document theirs.
type Factory func(config map[string]any) (DocumentLoader, error)

// Registry holds all registered document loaders by name.
type Registry struct {
	mu      sync.RWMutex
	loaders map[string]Factory
}

// NewRegistry creates an empty loader registry.
func NewRegistry() *Registry {
	return &Registry{loaders: make(map[string]Factory)}
}

// Register registers a document loader factory by name.
// Returns an error if name is empty, factory is nil, or name is already registered.
func (r *Registry) Register(name string, factory Factory) error {
	if name == "" {
		return errors.New("compat/loader: name must not be empty")
	}
	if factory == nil {
		return fmt.Errorf("compat/loader: factory must not be nil for %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.loaders[name]; exists {
		return fmt.Errorf("compat/loader: %q already registered", name)
	}
	r.loaders[name] = factory
	return nil
}

// Lookup returns the factory registered under name, or ErrNotFound.
func (r *Registry) Lookup(name string) (Factory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.loaders[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return f, nil
}

// Names returns all registered loader names in arbitrary order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.loaders))
	for k := range r.loaders {
		names = append(names, k)
	}
	return names
}

// ErrNotFound is the sentinel returned by Lookup for unknown loaders.
var ErrNotFound = errors.New("compat/loader: loader not found")

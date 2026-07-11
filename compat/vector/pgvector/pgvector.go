// Package pgvector is the official pgvector-backed vector store adapter for ARES.
//
// It wraps github.com/Timwood0x10/ares/internal/storage/postgres under the
// compat/vector.VectorStore interface so a PostgreSQL+pgvector service plugs
// into the ARES runtime via compat.RegisterVector("pgvector", …).
//
// This is a placeholder skeleton. The real adapter will bind to
// internal/storage/postgres.Pool + repositories; the stub returns
// ErrNotImplemented so callers fail fast until the binding is wired.
package pgvector

import (
	"context"
	"errors"

	"github.com/Timwood0x10/ares/compat/vector"
)

// ErrNotImplemented is returned by stub methods until the full binding is wired.
var ErrNotImplemented = errors.New("compat/vector/pgvector: not implemented yet")

// Adapter satisfies compat/vector.VectorStore against pgvector.
type Adapter struct{}

// New constructs an Adapter from a raw config map.
//
// Recognized keys (subject to change once the real binding is wired):
//
//	dsn        string — PostgreSQL DSN.
//	table      string — vector table name (e.g. "experiences_1024").
//	dimension  int    — vector dimensionality.
func New(_ map[string]any) (*Adapter, error) {
	return &Adapter{}, nil
}

// Search returns the top-k nearest neighbors for the given query vector.
func (*Adapter) Search(_ context.Context, _ []float64, _ string, _ int) ([]vector.Result, error) {
	return nil, ErrNotImplemented
}

// Upsert inserts or updates a batch of vectors identified by ID.
func (*Adapter) Upsert(_ context.Context, _ string, _ []vector.Item) error {
	return ErrNotImplemented
}

// HealthCheck reports whether the backend is reachable and usable.
func (*Adapter) HealthCheck(_ context.Context) error { return ErrNotImplemented }

// Close releases backend-specific resources.
func (*Adapter) Close() error { return nil }

// Compile-time interface assertion.
var _ vector.VectorStore = (*Adapter)(nil)

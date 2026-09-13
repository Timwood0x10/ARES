// Package repositories provides fast contract tests for the vector-write
// guards, runnable without a database.
package repositories

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// TestUpdateEmbeddingRejectsEmptyVector locks the fail-fast guard shared by
// the four UpdateEmbedding implementations.
//
// Regression (docs/reviews/0.3.1-final-deep-review.md §5.3 S-3):
// UpdateEmbedding ran the embedding through FormatVector unconditionally, so
// an empty slice became the zero-dimension literal "[]". The write then failed
// with a raw pgvector dimension error — or, for tools.embedding (NOT NULL), a
// constraint violation. An explicit "set the vector" API must reject an empty
// vector up front with a typed error instead of leaking a SQL-level failure.
//
// The repositories are constructed with a nil handle on purpose: the guard
// must fire before any SQL runs, and a nil handle proves it did.
func TestUpdateEmbeddingRejectsEmptyVector(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "knowledge",
			call: func() error {
				return NewKnowledgeRepository(nil, nil).UpdateEmbedding(ctx, "tenant-1", "id-1", nil, "m", 1)
			},
		},
		{
			name: "experience",
			call: func() error {
				return NewExperienceRepository(nil).UpdateEmbedding(ctx, "tenant-1", "id-1", []float64{}, "m", 1)
			},
		},
		{
			name: "task_result",
			call: func() error {
				return NewTaskResultRepository(nil).UpdateEmbedding(ctx, "tenant-1", "id-1", nil, "m", 1)
			},
		},
		{
			name: "tool",
			call: func() error {
				return NewToolRepository(nil).UpdateEmbedding(ctx, "tenant-1", "id-1", nil, "m", 1)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("UpdateEmbedding with an empty vector must fail, got nil error")
			}
			if err != errors.ErrInvalidArgument {
				t.Fatalf("UpdateEmbedding empty vector error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

// TestUpdateEmbeddingRejectsEmptyTenant verifies the tenant guard still runs
// before the new vector guard, so the error identity for a missing tenant is
// unchanged.
func TestUpdateEmbeddingRejectsEmptyTenant(t *testing.T) {
	ctx := context.Background()

	err := NewExperienceRepository(nil).UpdateEmbedding(ctx, "", "id-1", []float64{1, 2}, "m", 1)
	if err == nil {
		t.Fatal("UpdateEmbedding with an empty tenant must fail, got nil error")
	}
	if err != postgres.ErrMissingTenantID {
		t.Fatalf("UpdateEmbedding empty tenant error = %v, want ErrMissingTenantID", err)
	}
}

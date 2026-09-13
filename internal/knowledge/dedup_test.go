package knowledge_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memstore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// TestFindDuplicateRequiresNamespace pins K-1's fail-closed half.
//
// Regression (docs/reviews/0.3.1-final-deep-review.md §5.5 K-1): FindDuplicate
// built its HybridSearchRequest without a Namespace, and every store keys
// namespace isolation on that field — so the duplicate search scanned every
// namespace. A caller in namespace A could have an object marked superseded (and
// therefore never stored) because namespace B happened to hold something
// similar: a silent fact loss, not a visible error.
//
// An unscoped duplicate search is never what a caller means, so it is refused.
func TestFindDuplicateRequiresNamespace(t *testing.T) {
	store := memstore.New()

	_, err := knowledge.FindDuplicate(context.Background(), store, knowledge.DuplicateQuery{
		Vector:    []float32{1, 0, 0},
		Model:     "test-model",
		Threshold: 0.5,
	})
	require.Error(t, err, "an unscoped duplicate search must fail closed")
	require.Contains(t, err.Error(), "namespace")
}

// TestDuplicateQueryCarriesNamespace documents the shape the fix relies on: the
// namespace is part of the query, so a caller cannot supply a vector without
// also stating which namespace it belongs to.
func TestDuplicateQueryCarriesNamespace(t *testing.T) {
	q := knowledge.DuplicateQuery{
		Namespace: "tenant-a",
		Vector:    []float32{1, 0, 0},
		Threshold: 0.9,
	}
	require.Equal(t, "tenant-a", q.Namespace)
}

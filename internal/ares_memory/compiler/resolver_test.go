package compiler

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resolverObj(id, ns, summary string) *knowledge.KnowledgeObject {
	return &knowledge.KnowledgeObject{
		ID:        id,
		Namespace: ns,
		Summary:   summary,
	}
}

// TestResolverDropsJaccardDuplicate verifies the core contract: a candidate
// whose summary is Jaccard-identical to an already-persisted object (but under
// a DIFFERENT id — so exact-ID dedup would miss it) is dropped.
func TestResolverDropsJaccardDuplicate(t *testing.T) {
	ctx := context.Background()
	store := memorystore.New()
	require.NoError(t, store.Save(ctx, resolverObj("e1", "ns", "ares uses patch for evolution")))

	r := NewResolver(store, 0) // 0 -> default threshold 0.85
	got, err := r.Resolve(ctx, "ns", []*knowledge.KnowledgeObject{
		resolverObj("e2", "ns", "ares uses patch for evolution"),
	})
	require.NoError(t, err)
	assert.Empty(t, got, "near-duplicate must be dropped")
}

// TestResolverKeepsDistinct verifies unrelated content is never dropped.
func TestResolverKeepsDistinct(t *testing.T) {
	ctx := context.Background()
	store := memorystore.New()
	require.NoError(t, store.Save(ctx, resolverObj("e1", "ns", "ares uses patch for evolution")))

	r := NewResolver(store, 0)
	got, err := r.Resolve(ctx, "ns", []*knowledge.KnowledgeObject{
		resolverObj("e2", "ns", "kubernetes schedules pods across nodes"),
	})
	require.NoError(t, err)
	require.Len(t, got, 1, "distinct object must be kept")
}

// TestResolverNilStoreNoop verifies a resolver over a nil store is a safe
// no-op, so callers can wire it unconditionally.
func TestResolverNilStoreNoop(t *testing.T) {
	ctx := context.Background()
	r := NewResolver(nil, 0)
	got, err := r.Resolve(ctx, "ns", []*knowledge.KnowledgeObject{
		resolverObj("e1", "ns", "anything"),
	})
	require.NoError(t, err)
	assert.Len(t, got, 1, "nil store must leave candidates unchanged")
}

// TestResolverThresholdControlsSensitivity proves the threshold is what makes
// two merely-similar (not identical) objects either distinct or duplicates.
// "rust compiles to native code" vs "rust compiles to webassembly" scores
// exactly 0.5 Jaccard, so a 0.5 threshold drops it while a 0.99 threshold keeps
// it.
func TestResolverThresholdControlsSensitivity(t *testing.T) {
	ctx := context.Background()
	store := memorystore.New()
	require.NoError(t, store.Save(ctx, resolverObj("e1", "ns", "rust compiles to native code")))

	near := []*knowledge.KnowledgeObject{
		resolverObj("e2", "ns", "rust compiles to webassembly"),
	}

	keptHigh, err := NewResolver(store, 0.99).Resolve(ctx, "ns", near)
	require.NoError(t, err)
	assert.Len(t, keptHigh, 1, "high threshold keeps a merely-similar object")

	keptLow, err := NewResolver(store, 0.5).Resolve(ctx, "ns", near)
	require.NoError(t, err)
	assert.Empty(t, keptLow, "low threshold drops a merely-similar object")
}

// TestResolverEmptyInput verifies empty candidate slices return cleanly.
func TestResolverEmptyInput(t *testing.T) {
	ctx := context.Background()
	got, err := NewResolver(memorystore.New(), 0).Resolve(ctx, "ns", nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

package knowledge

import (
	"context"
	"testing"
)

func TestInMemoryVectorIndex_UpsertAndSearch(t *testing.T) {
	idx := NewInMemoryVectorIndex()
	ctx := context.Background()

	// Three vectors along the same direction: a is parallel to q (score 1),
	// b is anti-parallel (score -1), c is orthogonal (score 0).
	q := []float32{1, 0}
	if err := idx.Upsert(ctx, "a", "m", q); err != nil {
		t.Fatalf("Upsert a: %v", err)
	}
	if err := idx.Upsert(ctx, "b", "m", []float32{-1, 0}); err != nil {
		t.Fatalf("Upsert b: %v", err)
	}
	if err := idx.Upsert(ctx, "c", "m", []float32{0, 1}); err != nil {
		t.Fatalf("Upsert c: %v", err)
	}

	hits, err := idx.Search(ctx, "m", q, 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(hits))
	}
	if hits[0].ObjectID != "a" || hits[0].Score != 1 {
		t.Errorf("expected a with score 1 first, got %+v", hits[0])
	}
	if hits[2].ObjectID != "b" || hits[2].Score != -1 {
		t.Errorf("expected b with score -1 last, got %+v", hits[2])
	}

	// TopK caps the result set to the most similar entries.
	top, _ := idx.Search(ctx, "m", q, 1)
	if len(top) != 1 || top[0].ObjectID != "a" {
		t.Errorf("expected top-1 = a, got %+v", top)
	}
}

func TestInMemoryVectorIndex_UpsertEmptyDeletes(t *testing.T) {
	idx := NewInMemoryVectorIndex()
	ctx := context.Background()

	if err := idx.Upsert(ctx, "a", "m", []float32{1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// An empty vector is a deletion, not a zero-length placeholder.
	if err := idx.Upsert(ctx, "a", "m", nil); err != nil {
		t.Fatalf("Upsert nil: %v", err)
	}
	hits, _ := idx.Search(ctx, "m", []float32{1, 0}, 10)
	if len(hits) != 0 {
		t.Errorf("expected 0 hits after empty upsert, got %d", len(hits))
	}
}

func TestInMemoryVectorIndex_UpsertReplaces(t *testing.T) {
	idx := NewInMemoryVectorIndex()
	ctx := context.Background()

	if err := idx.Upsert(ctx, "a", "m", []float32{1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := idx.Upsert(ctx, "a", "m", []float32{-1, 0}); err != nil {
		t.Fatalf("Upsert replace: %v", err)
	}
	hits, _ := idx.Search(ctx, "m", []float32{1, 0}, 1)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].Score != -1 {
		t.Errorf("expected replaced vector to score -1, got %f", hits[0].Score)
	}
}

func TestInMemoryVectorIndex_UpsertValidation(t *testing.T) {
	idx := NewInMemoryVectorIndex()
	ctx := context.Background()
	if err := idx.Upsert(ctx, "", "m", []float32{1}); err == nil {
		t.Error("expected error for empty objectID")
	}
	if err := idx.Upsert(ctx, "a", "", []float32{1}); err == nil {
		t.Error("expected error for empty model")
	}
}

func TestInMemoryVectorIndex_SearchEmptyInputs(t *testing.T) {
	idx := NewInMemoryVectorIndex()
	ctx := context.Background()

	if err := idx.Upsert(ctx, "a", "m", []float32{1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if hits, _ := idx.Search(ctx, "m", nil, 5); len(hits) != 0 {
		t.Errorf("expected 0 hits for nil query, got %d", len(hits))
	}
	if hits, _ := idx.Search(ctx, "m", []float32{1, 0}, 0); len(hits) != 0 {
		t.Errorf("expected 0 hits for topK=0, got %d", len(hits))
	}
	if hits, _ := idx.Search(ctx, "absent", []float32{1, 0}, 5); len(hits) != 0 {
		t.Errorf("expected 0 hits for absent model, got %d", len(hits))
	}
}

func TestInMemoryVectorIndex_Delete(t *testing.T) {
	idx := NewInMemoryVectorIndex()
	ctx := context.Background()

	if err := idx.Upsert(ctx, "a", "m", []float32{1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := idx.Upsert(ctx, "b", "m", []float32{1, 0}); err != nil {
		t.Fatalf("Upsert b: %v", err)
	}
	if err := idx.Delete(ctx, "a", "m"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	hits, _ := idx.Search(ctx, "m", []float32{1, 0}, 5)
	if len(hits) != 1 || hits[0].ObjectID != "b" {
		t.Errorf("expected only b to remain, got %+v", hits)
	}
	// Deleting a missing entry is a no-op.
	if err := idx.Delete(ctx, "missing", "m"); err != nil {
		t.Errorf("expected nil for deleting missing entry, got %v", err)
	}
}

// TestInMemoryVectorIndex_CallerCannotMutateStoredVector verifies the Upsert
// copy contract: mutating the caller's slice after hand-off must not affect
// stored recall.
func TestInMemoryVectorIndex_CallerCannotMutateStoredVector(t *testing.T) {
	idx := NewInMemoryVectorIndex()
	ctx := context.Background()

	v := []float32{1, 0}
	if err := idx.Upsert(ctx, "a", "m", v); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Mutate the caller slice after hand-off.
	v[0] = -1

	hits, _ := idx.Search(ctx, "m", []float32{1, 0}, 1)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].Score != 1 {
		t.Errorf("stored vector was mutated by caller; expected score 1, got %f", hits[0].Score)
	}
}

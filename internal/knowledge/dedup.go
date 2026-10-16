package knowledge

import (
	"context"
	"fmt"
)

// DuplicateQuery describes one duplicate lookup.
//
// It is a struct rather than five positional parameters because the fields are
// easy to transpose at the call site, and a transposed Namespace/model silently
// changes what "the same fact" means.
type DuplicateQuery struct {
	// Namespace scopes the search and is REQUIRED. Every store keys tenant
	// isolation on it; leaving it empty makes HybridSearch scan every
	// namespace, so an object could be marked superseded because a DIFFERENT
	// namespace held something similar — silently dropping a fact.
	Namespace string
	// Vector is the embedding to compare against.
	Vector []float32
	// Model restricts the comparison to representations produced by one
	// embedding model. Empty means "any model", which only makes sense when
	// every object in the namespace shares a model.
	Model string
	// Threshold is the minimum cosine similarity that counts as a duplicate.
	Threshold float64
}

// FindDuplicate returns an existing active object in the same namespace whose
// vector cosine similarity to q.Vector is >= q.Threshold (for q.Model), or nil
// if none. It reuses HybridSearch with the vector as QueryVector and inspects
// VectorScore (not FinalScore) so the threshold stays purely vector-based.
//
// Returns (nil, nil) when no active object reaches the threshold.
// Returns an error when q.Namespace is empty: an unscoped duplicate search is
// never what a caller means, and failing closed is the only safe reading.
func FindDuplicate(ctx context.Context, store KnowledgeStore, q DuplicateQuery) (*KnowledgeObject, error) {
	if q.Namespace == "" {
		return nil, fmt.Errorf("find duplicate: namespace is required")
	}
	// Relevance-only weights, explicitly: the duplicate THRESHOLD is already vector-only
	// (VectorScore below, not FinalScore), but the candidate WINDOW is ordered by
	// FinalScore, so a deployment that weighted recency could push an old near-duplicate
	// out of the ten candidates examined and let it through. Dedup is not a relevance
	// ranking, and it must not inherit a ranking decision.
	relevanceOnly := DefaultHybridWeights()
	req := HybridSearchRequest{
		Namespace:    q.Namespace,
		QueryVector:  q.Vector,
		Model:        q.Model,
		MinScore:     0,
		FinalK:       10,
		StatusFilter: []ObjectStatus{StatusActive},
		Weights:      &relevanceOnly,
	}
	results, err := store.HybridSearch(ctx, req)
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		if r.VectorScore >= q.Threshold {
			return r.Object, nil
		}
	}
	//nolint:nilnil // no duplicate is a non-error empty result, per FindDuplicate contract.
	return nil, nil
}

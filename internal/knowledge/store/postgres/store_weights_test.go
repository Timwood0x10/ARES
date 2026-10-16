package postgresstore

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TestHybridWeights_ResolutionOrder pins the three-level resolution, and it is the
// contract that keeps the recency term from being configurable-but-unapplied: a
// deployment sets the weights ONCE on the store (WithDefaultWeights) and every
// retrieval path through that store inherits them, while a caller that means something
// specific for its own query (dedup: relevance only) still overrides.
func TestHybridWeights_ResolutionOrder(t *testing.T) {
	deployment := knowledge.DefaultHybridWeights().WithRecency(0.1)
	store, err := New(nil, WithDefaultWeights(deployment))
	if err != nil {
		// New rejects a nil db; the resolution is a pure method, so build the store
		// directly instead of relaxing the constructor's contract for a test.
		store = &Store{}
		WithDefaultWeights(deployment)(store)
	}

	// 1. No request weights, no store default: the library default (relevance only).
	plain := &Store{}
	if got := plain.hybridWeights(knowledge.HybridSearchRequest{}); got != knowledge.DefaultHybridWeights() {
		t.Errorf("unconfigured store = %+v, want the library default %+v", got, knowledge.DefaultHybridWeights())
	}

	// 2. Store default applies when the request says nothing.
	if got := store.hybridWeights(knowledge.HybridSearchRequest{}); got.Recency != 0.1 {
		t.Errorf("store default was not applied: %+v", got)
	}

	// 3. An explicit request wins over the store default (this is what keeps dedup's
	//    relevance-only threshold safe from a deployment's ranking choice).
	relevanceOnly := knowledge.DefaultHybridWeights()
	if got := store.hybridWeights(knowledge.HybridSearchRequest{Weights: &relevanceOnly}); got.Recency != 0 {
		t.Errorf("request weights must win, got %+v", got)
	}
}

// TestWithDefaultWeights_CopiesTheValue: the option must not alias the caller's
// variable, or a caller that reuses and mutates its own struct would silently change
// every future search through this store.
func TestWithDefaultWeights_CopiesTheValue(t *testing.T) {
	weights := knowledge.DefaultHybridWeights().WithRecency(0.2)
	store := &Store{}
	WithDefaultWeights(weights)(store)

	weights.Recency = 0.9
	if store.defaultWeights == nil || store.defaultWeights.Recency != 0.2 {
		t.Errorf("stored weights = %+v, want the value as it was when the option was applied", store.defaultWeights)
	}
}

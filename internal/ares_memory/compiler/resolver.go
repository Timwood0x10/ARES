package compiler

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// defaultDedupThreshold is the Jaccard similarity at or above which two
// KnowledgeObject summaries are treated as the same piece of knowledge and the
// later one is discarded.
const defaultDedupThreshold = 0.85

// defaultDedupQueryLimit caps how many already-persisted objects are compared
// per Resolve call. The compiler store is small (per-namespace conversation
// knowledge), so this is a generous ceiling rather than a tuning knob.
const defaultDedupQueryLimit = 1000

// Resolver deduplicates KnowledgeObjects against knowledge already persisted in
// a shared store, so multiple pipelines writing to the same namespace do not
// accumulate near-duplicate objects. It is "collaborative" because the store is
// the shared collaboration point: every producer — the live compiler path, the
// legacy pipeline, future writers — is deduplicated against all prior writers,
// not only within a single pipeline.
//
// Similarity is the token-Jaccard score over object summaries (see tokenJaccard
// in km_distiller.go), reused here so there is a single tokenizer for the whole
// compiler. Exact-ID collisions are still handled by the store's idempotent
// Save; the Resolver adds fuzzy (semantically-similar, textually-different)
// dedup on top.
type Resolver struct {
	store      knowledge.KnowledgeStore
	threshold  float64
	queryLimit int
}

// NewResolver builds a Resolver over the given store. threshold is the Jaccard
// cutoff (defaults to defaultDedupThreshold when <= 0); queryLimit caps the
// compared existing objects per call (defaults to defaultDedupQueryLimit when
// <= 0). A nil store yields a no-op resolver: Resolve returns its input
// unchanged so callers can wire it unconditionally.
func NewResolver(store knowledge.KnowledgeStore, threshold float64) *Resolver {
	if threshold <= 0 {
		threshold = defaultDedupThreshold
	}
	if store == nil {
		return &Resolver{store: nil, threshold: threshold, queryLimit: defaultDedupQueryLimit}
	}
	return &Resolver{store: store, threshold: threshold, queryLimit: defaultDedupQueryLimit}
}

// Resolve filters candidates, dropping any whose summary is Jaccard-similar
// (>= threshold) to an object already persisted in the same namespace. The
// first writer wins: an existing object is kept and the duplicate candidate is
// discarded. Returns the surviving candidates; on a nil resolver, nil store, or
// empty input it returns the candidates unchanged and no error.
func (r *Resolver) Resolve(ctx context.Context, namespace string, candidates []*knowledge.KnowledgeObject) ([]*knowledge.KnowledgeObject, error) {
	if r == nil || r.store == nil || len(candidates) == 0 {
		return candidates, nil
	}
	existing, err := r.store.Query(ctx, knowledge.Query{Namespace: namespace, Limit: r.queryLimit})
	if err != nil {
		return nil, fmt.Errorf("resolver: query existing objects: %w", err)
	}
	if len(existing) == 0 {
		return candidates, nil
	}

	out := make([]*knowledge.KnowledgeObject, 0, len(candidates))
	for _, c := range candidates {
		if c == nil {
			continue
		}
		if r.isDuplicate(c, existing) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// isDuplicate reports whether candidate's summary is Jaccard-similar enough to
// any existing object to be considered the same knowledge. It falls back to the
// Normalized field when Summary is empty so the comparison never silently
// degenerates to a zero-vs-zero 1.0 match.
func (r *Resolver) isDuplicate(candidate *knowledge.KnowledgeObject, existing []*knowledge.KnowledgeObject) bool {
	candText := candidate.Summary
	if candText == "" {
		candText = candidate.Normalized
	}
	for _, e := range existing {
		if e == nil {
			continue
		}
		eText := e.Summary
		if eText == "" {
			eText = e.Normalized
		}
		if tokenJaccard(candText, eText) >= r.threshold {
			return true
		}
	}
	return false
}

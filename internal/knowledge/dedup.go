package knowledge

import "context"

// FindDuplicate returns an existing active object whose vector cosine similarity
// to vec is >= threshold (for the given model), or nil if none. It reuses
// HybridSearch with vec as the QueryVector and inspects VectorScore (not
// FinalScore) so the threshold is purely vector-based.
//
// Returns (nil, nil) when no active object reaches the threshold.
func FindDuplicate(ctx context.Context, store KnowledgeStore, vec []float32, model string, threshold float64) (*KnowledgeObject, error) {
	req := HybridSearchRequest{
		QueryVector:  vec,
		Model:        model,
		MinScore:     0,
		FinalK:       10,
		StatusFilter: []ObjectStatus{StatusActive},
	}
	results, err := store.HybridSearch(ctx, req)
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		if r.VectorScore >= threshold {
			return r.Object, nil
		}
	}
	//nolint:nilnil // no duplicate is a non-error empty result, per FindDuplicate contract.
	return nil, nil
}

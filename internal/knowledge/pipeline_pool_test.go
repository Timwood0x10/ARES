package knowledge

import (
	"context"
	"sync"
	"testing"
)

// candidateRecordingMatcher records the candidate pool each Match call sees.
type candidateRecordingMatcher struct {
	mu         sync.Mutex
	poolSizes  []int
	sawFirstID bool
}

func (m *candidateRecordingMatcher) Name() string { return "recording" }

func (m *candidateRecordingMatcher) Match(_ context.Context, _ *KnowledgeObject, candidates []*KnowledgeObject) (*ResolveResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.poolSizes = append(m.poolSizes, len(candidates))
	for _, c := range candidates {
		if c.ID == "obj-0000" {
			m.sawFirstID = true
		}
	}
	return &ResolveResult{IsNew: true}, nil
}

// TestPipelineResolvedPoolIsBounded locks REVIEW 2.5#28: the resolvedObjects
// candidate pool must stay bounded. It previously grew with every object
// ever processed, and the per-Process O(n) snapshot made the total pipeline
// cost O(n²).
func TestPipelineResolvedPoolIsBounded(t *testing.T) {
	matcher := &candidateRecordingMatcher{}
	p := NewKnowledgePipeline(nil, []EntityMatcher{matcher}, nil, nil)

	const n = maxResolvedCandidates * 3
	for i := 0; i < n; i++ {
		if _, err := p.Process(context.Background(), &KnowledgeObject{
			ID:      "obj-" + pad4(i),
			Summary: "s",
		}); err != nil {
			t.Fatalf("Process %d: %v", i, err)
		}
	}

	p.mu.RLock()
	poolLen := len(p.resolvedObjects)
	snapshotLen := len(p.candidates)
	orderLen := len(p.resolvedOrder) - p.orderHead
	p.mu.RUnlock()

	if poolLen > maxResolvedCandidates {
		t.Errorf("resolved pool grew to %d, cap is %d", poolLen, maxResolvedCandidates)
	}
	if orderLen > maxResolvedCandidates {
		t.Errorf("live order entries grew to %d, cap is %d", orderLen, maxResolvedCandidates)
	}
	// The published snapshot may overshoot by at most the compaction
	// threshold (half the cap of evicted/superseded entries).
	if snapshotLen > maxResolvedCandidates*3/2 {
		t.Errorf("published candidate snapshot %d exceeds the 1.5x cap bound %d",
			snapshotLen, maxResolvedCandidates*3/2)
	}
	// The backing order array must not grow without bound either.
	p.mu.RLock()
	backing := len(p.resolvedOrder)
	p.mu.RUnlock()
	if backing > 2*maxResolvedCandidates {
		t.Errorf("order backing array grew to %d (leak): trimmed prefix compaction is broken", backing)
	}
}

// TestPipelineCandidatesVisibleToLaterProcess verifies the pool still serves
// its purpose after bounding: objects processed earlier remain visible as
// matching candidates to later Process calls (within the window).
func TestPipelineCandidatesVisibleToLaterProcess(t *testing.T) {
	matcher := &candidateRecordingMatcher{}
	p := NewKnowledgePipeline(nil, []EntityMatcher{matcher}, nil, nil)

	if _, err := p.Process(context.Background(), &KnowledgeObject{ID: "obj-0000", Summary: "first"}); err != nil {
		t.Fatalf("Process first: %v", err)
	}
	for i := 1; i < 5; i++ {
		if _, err := p.Process(context.Background(), &KnowledgeObject{ID: "obj-" + pad4(i), Summary: "later"}); err != nil {
			t.Fatalf("Process %d: %v", i, err)
		}
	}
	if !matcher.sawFirstID {
		t.Error("the first processed object must be visible as a candidate to later Process calls")
	}
}

// TestPipelineUpsertSupersedesAndBounded verifies the upsert path: processing
// the same ID again replaces the stored version and does not grow the pool.
func TestPipelineUpsertSupersedesAndBounded(t *testing.T) {
	matcher := &candidateRecordingMatcher{}
	p := NewKnowledgePipeline(nil, []EntityMatcher{matcher}, nil, nil)

	for round := 0; round < 3; round++ {
		for i := 0; i < maxResolvedCandidates/2; i++ {
			if _, err := p.Process(context.Background(), &KnowledgeObject{
				ID: "obj-" + pad4(i), Summary: "r",
			}); err != nil {
				t.Fatalf("Process round %d idx %d: %v", round, i, err)
			}
		}
	}
	p.mu.RLock()
	poolLen := len(p.resolvedObjects)
	p.mu.RUnlock()
	if poolLen != maxResolvedCandidates/2 {
		t.Errorf("upserts must not grow the pool: got %d live entries, want %d",
			poolLen, maxResolvedCandidates/2)
	}
}

func pad4(i int) string {
	s := []byte("0000")
	n := i
	for pos := 3; pos >= 0 && n > 0; pos-- {
		s[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(s)
}

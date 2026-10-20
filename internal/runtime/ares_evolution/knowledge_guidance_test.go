package evolution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memstore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

func seedKnowledge(t *testing.T) *memstore.Store {
	t.Helper()
	store := memstore.New()
	objects := []*knowledge.KnowledgeObject{
		{ID: "akg:1", Type: knowledge.ObjectMemory, Namespace: "tenant-a",
			Summary: "the retry budget was exhausted by long polls", Tags: []string{"retry", "latency"},
			Status: knowledge.StatusActive, Confidence: 0.8},
		{ID: "akg:2", Type: knowledge.ObjectMemory, Namespace: "tenant-b",
			Summary: "another tenant's private fact", Status: knowledge.StatusActive, Confidence: 0.9},
	}
	knowledge.StampWriteSchema(objects...)
	require.NoError(t, store.Save(context.Background(), objects...))
	return store
}

// TestGA_ConsumesAKGForCandidateNarrowing is M10's boundary: knowledge may shape how candidates
// are PROPOSED, and the scope must be the caller's, not the store's.
func TestGA_ConsumesAKGForCandidateNarrowing(t *testing.T) {
	store := seedKnowledge(t)
	provider, err := NewKnowledgeGuidanceProvider(store, func(context.Context) string { return "tenant-a" }, 10)
	require.NoError(t, err)

	hints, err := provider.HintsForTask(context.Background(), "chat", 10)
	require.NoError(t, err)
	require.Len(t, hints, 1, "only the caller's namespace may be recalled")
	assert.Equal(t, "akg:1", hints[0].SourceExperienceIDs[0])
	assert.Contains(t, hints[0].Problem, "retry budget")
	assert.Equal(t, []string{"retry", "latency"}, hints[0].Constraints)
	assert.Equal(t, 0.8, hints[0].Confidence)
	assert.Empty(t, hints[0].Solution,
		"a fact says what IS; inventing a solution here would be knowledge grading candidates")

	// Unscoped callers get nothing rather than everyone's facts.
	unscoped, err := NewKnowledgeGuidanceProvider(store, func(context.Context) string { return "" }, 10)
	require.NoError(t, err)
	got, err := unscoped.HintsForTask(context.Background(), "chat", 10)
	require.NoError(t, err)
	assert.Empty(t, got, "an unscoped lookup must fail closed, not recall across tenants")

	// A provider without either dependency is refused at construction rather than at use.
	_, err = NewKnowledgeGuidanceProvider(nil, func(context.Context) string { return "x" }, 1)
	assert.Error(t, err)
	_, err = NewKnowledgeGuidanceProvider(store, nil, 1)
	assert.Error(t, err)
}

// TestGAKnowledgeSeam_NoFeedbackAmplification is the loop cut (M10): recording a strategy
// outcome must NOT write to the knowledge graph.
//
// Writing back would make the GA's own opinion into a fact, and the next proposal would be
// shaped by it — a self-reinforcing cycle where nothing later can distinguish "observed" from
// "the GA said so". Outcomes belong in the experiment log, which GA reads as history.
func TestGAKnowledgeSeam_NoFeedbackAmplification(t *testing.T) {
	ctx := context.Background()
	store := seedKnowledge(t)

	before, err := store.Query(ctx, knowledge.Query{Namespace: "tenant-a"})
	require.NoError(t, err)

	provider, err := NewKnowledgeGuidanceProvider(store, func(context.Context) string { return "tenant-a" }, 10)
	require.NoError(t, err)

	// The evolution loop reports what the candidate did...
	require.NoError(t, provider.RecordStrategyOutcome(ctx, StrategyOutcome{
		StrategyID: "cand-1", TaskType: "chat", Success: true, Score: 0.9,
		ExperienceIDs: []string{"ev-1"},
	}))

	// ...and the graph is unchanged: no fact was created from the GA's own verdict.
	after, err := store.Query(ctx, knowledge.Query{Namespace: "tenant-a"})
	require.NoError(t, err)
	assert.Len(t, after, len(before), "an outcome must not become a knowledge fact")

	// Hints therefore still derive only from pre-existing, externally-sourced facts.
	hints, err := provider.HintsForTask(ctx, "chat", 10)
	require.NoError(t, err)
	for _, h := range hints {
		assert.NotContains(t, h.Problem, "cand-1", "the candidate's own outcome must not come back as guidance")
	}
}

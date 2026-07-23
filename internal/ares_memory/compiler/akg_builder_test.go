package compiler

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// markerNormalizer appends a detectable token to Normalized so tests can prove
// the object actually passed through a pipeline stage.
type markerNormalizer struct{}

func (markerNormalizer) Name() string { return "test-normalizer" }

func (markerNormalizer) Normalize(_ context.Context, obj *knowledge.KnowledgeObject) (*knowledge.KnowledgeObject, error) {
	if obj == nil {
		return obj, nil
	}
	obj.Normalized += "[NORM]"
	return obj, nil
}

// markerSummarizer sets Summary to a detectable token derived from Normalized.
type markerSummarizer struct{}

func (markerSummarizer) Name() string { return "test-summarizer" }

func (markerSummarizer) Summarize(_ context.Context, obj *knowledge.KnowledgeObject) (*knowledge.KnowledgeObject, error) {
	if obj == nil {
		return obj, nil
	}
	obj.Summary = "[SUM]" + obj.Normalized
	return obj, nil
}

func newTestSubgraph() *SubGraph {
	return &SubGraph{
		Nodes: []*Node{
			{
				ID:         "n-fact",
				Type:       NodeFact,
				Confidence: 0.9,
				Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": "Patch for evolution"},
			},
			{
				ID:         "n-decision",
				Type:       NodeDecision,
				Confidence: 0.8,
				Attributes: map[string]any{"choice": "adopt Rust"},
			},
		},
	}
}

// TestAKGBuilderPipelineRefinement verifies Phase 2.1: when a pipeline is
// attached, every projected object is refined through it before persistence.
// The markers prove the object passed through both the normalizer and the
// summarizer (i.e. AKG's processing, not the raw node summary).
func TestAKGBuilderPipelineRefinement(t *testing.T) {
	ctx := context.Background()
	pipe := knowledge.NewKnowledgePipeline(
		[]knowledge.Normalizer{markerNormalizer{}},
		nil,
		nil,
		[]knowledge.Summarizer{markerSummarizer{}},
	)
	store := memorystore.New()
	builder := NewAKGBuilder(store).WithAKGPipeline(pipe)

	res, err := builder.Build(ctx, newTestSubgraph(), "test-ns")
	require.NoError(t, err)
	require.Len(t, res.Objects, 2, "both nodes must be built")

	for _, obj := range res.Objects {
		assert.Contains(t, obj.Normalized, "[NORM]", "object must pass through normalizer")
		assert.Contains(t, obj.Summary, "[SUM]", "object must pass through summarizer")
	}
	assert.Equal(t, 2, res.Saved, "both objects must be persisted when a store is set")

	// The refined objects must be readable from the shared store by a second
	// consumer, proving the pipeline output was actually persisted.
	got, gErr := store.Get(ctx, "n-fact")
	require.NoError(t, gErr)
	require.NotNil(t, got)
	assert.Contains(t, got.Summary, "[SUM]", "persisted object reflects pipeline output")
}

// TestAKGBuilderNilPipelineBackwardCompat verifies that without a pipeline the
// builder keeps its previous build-only-direct behavior: objects are projected
// straight from node attributes with no refinement. This guards against
// regressing the default (opt-out) path.
func TestAKGBuilderNilPipelineBackwardCompat(t *testing.T) {
	ctx := context.Background()
	builder := NewAKGBuilder(memorystore.New()) // no WithAKGPipeline

	res, err := builder.Build(ctx, newTestSubgraph(), "test-ns")
	require.NoError(t, err)
	require.Len(t, res.Objects, 2)

	for _, obj := range res.Objects {
		assert.NotContains(t, obj.Normalized, "[NORM]", "no pipeline => no refinement marker")
		assert.NotContains(t, obj.Summary, "[SUM]", "no pipeline => no refinement marker")
	}
}

// TestAKGBuilderSharedStoreReadable verifies Phase 2.2: the store the builder
// writes into is a shared pool — a separate reader holding the same instance
// can read the persisted objects back. This is the contract other AKG
// consumers (prompt injection, future runtime ingestion) depend on.
func TestAKGBuilderSharedStoreReadable(t *testing.T) {
	ctx := context.Background()
	// A single store instance stands in for the shared pool on Components.
	shared := memorystore.New()
	builder := NewAKGBuilder(shared)

	_, err := builder.Build(ctx, newTestSubgraph(), "test-ns")
	require.NoError(t, err)

	// Second consumer: reads from the SAME store instance.
	reader := shared
	obj, err := reader.Get(ctx, "n-decision")
	require.NoError(t, err)
	require.NotNil(t, obj, "second consumer must read back the persisted object")
	assert.Equal(t, "test-ns", obj.Namespace)
}

// TestAKGBuilderNilSubgraph verifies the early-return contract for empty input.
func TestAKGBuilderNilSubgraph(t *testing.T) {
	ctx := context.Background()
	builder := NewAKGBuilder(memorystore.New()).WithAKGPipeline(
		knowledge.NewKnowledgePipeline(nil, nil, nil, nil),
	)
	res, err := builder.Build(ctx, nil, "ns")
	require.NoError(t, err)
	assert.Empty(t, res.Objects)
	assert.Equal(t, 0, res.Saved)
}

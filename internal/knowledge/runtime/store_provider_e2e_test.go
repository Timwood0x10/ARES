package runtime_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	storeprov "github.com/Timwood0x10/ares/internal/knowledge/provider/store"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreProviderFlowsThroughRuntime proves the end-to-end closure: a
// KnowledgeObject persisted into a store by a producer (e.g. the Conversation
// Compiler) is pulled back into the AKG retrieval path when a store-backed
// provider is registered into the runtime. Without this, comp.KnowledgeStore
// is a dead-end cache. This mirrors the live path used by the agent's AKF
// tools and the evolution system (both run Execute on comp.KnowledgeRuntime).
func TestStoreProviderFlowsThroughRuntime(t *testing.T) {
	ctx := context.Background()

	// Producer side: persist a compiler object into the shared store.
	shared := memorystore.New()
	require.NoError(t, shared.Save(ctx, &knowledge.KnowledgeObject{
		ID:         "entity-ares",
		Namespace:  "conversation-compiler",
		Type:       knowledge.ObjectDocument,
		Summary:    "ARES uses Patch for runtime updates",
		Confidence: 0.9,
		Tags:       []string{"ares", "patch"},
	}))

	// Register a store-backed provider into the runtime's registry.
	storeProv, err := storeprov.NewStoreProvider(shared, storeprov.Config{
		Name:       "conversation-compiler-store",
		Namespace:  "conversation-compiler",
		IntentTags: []string{"conversation", "memory"},
	})
	require.NoError(t, err)

	reg := provider.NewProviderRegistry()
	require.NoError(t, reg.Register(storeProv))

	sd := planner.NewSourceDiscovery(reg, &minimalQueryPlanner{})
	p := planner.NewKnowledgePlanner()
	rt := knowledgeruntime.New(p, sd, reg, nil,
		[]knowledgeruntime.Linker{&knowledgeruntime.DefaultLinker{}},
		[]knowledgeruntime.Reducer{&knowledgeruntime.DefaultReducer{}})

	graph, err := rt.Execute(ctx, "summarize the conversation about ares",
		knowledge.TokenBudget{MaxTokens: 2000, ForGraph: 1000}, nil)
	require.NoError(t, err)
	require.NotNil(t, graph)

	// The persisted compiler object must surface in the retrieved graph.
	found := false
	for id, n := range graph.Nodes {
		if id == "entity-ares" || (n != nil && strings.Contains(strings.ToLower(n.Summary), "ares")) {
			found = true
			break
		}
	}
	assert.True(t, found, "compiler-persisted object must flow into the runtime graph")
}

// minimalQueryPlanner satisfies planner.QueryPlanner for the test runtime.
type minimalQueryPlanner struct{}

func (q *minimalQueryPlanner) PlanQuery(_ context.Context, req planner.KnowledgeRequirement, _, _ string) (*planner.QueryPlan, error) {
	return &planner.QueryPlan{Query: "test", QueryType: planner.QuerySQL, MaxResults: req.MaxResults}, nil
}

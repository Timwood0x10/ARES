package retriever

import (
	"context"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/pipeline"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	storeprov "github.com/Timwood0x10/ares/internal/knowledge/provider/store"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// TestAKGClosedLoopE2E verifies the production read-side closure of the AKG
// knowledge fabric:
//
//	KnowledgeStore (write path, e.g. Conversation Compiler)
//	  -> StoreProvider (registered in KnowledgeRuntime)
//	  -> runtime.Execute (Plan->Load->Pipeline->Link->Reduce)
//	  -> compiler.Compile
//	  -> prompt context that A2 injects into the leader agent.
//
// The StoreProvider is the exact component Phase 1 registered into the
// runtime; before A2 it was only exercised by tests. This test proves a
// persisted object actually flows back into the compiled prompt.
func TestAKGClosedLoopE2E(t *testing.T) {
	const marker = "CLOSURE_E2E_MARKER_AKG"

	memStore := memorystore.New()
	seeded := []*knowledge.KnowledgeObject{
		{
			ID:         "akg:" + marker + "_redis",
			Type:       knowledge.ObjectDecision,
			Namespace:  "agent-knowledge-fabric",
			Summary:    marker + " chose Redis for caching layer",
			Normalized: marker + " Redis is used as cache",
			Raw:        []byte("Decision: Use Redis for caching"),
			Confidence: 0.9,
			Tags:       []string{"redis", "cache", "decision"},
		},
		{
			ID:         "akg:" + marker + "_pg",
			Type:       knowledge.ObjectArchitecture,
			Namespace:  "agent-knowledge-fabric",
			Summary:    marker + " chose PostgreSQL for storage",
			Normalized: marker + " PostgreSQL is the primary DB",
			Raw:        []byte("Decision: Use PostgreSQL for persistence"),
			Confidence: 0.85,
			Tags:       []string{"postgres", "database", "decision"},
		},
	}
	if err := memStore.Save(context.Background(), seeded...); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	storeProv, err := storeprov.NewStoreProvider(memStore, storeprov.Config{
		Name:       "conversation-compiler-store",
		Namespace:  "",
		IntentTags: []string{"conversation", "recall", "agent", "context", "knowledge"},
		Limit:      200,
	})
	if err != nil {
		t.Fatalf("store provider: %v", err)
	}

	reg := provider.NewProviderRegistry()
	if err := reg.Register(storeProv); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	pipe := knowledge.NewKnowledgePipeline(
		[]knowledge.Normalizer{&pipeline.DefaultNormalizer{MaxRawBytes: 4096}},
		[]knowledge.EntityMatcher{&pipeline.DefaultEntityMatcher{MatchThreshold: 0.6}},
		[]knowledge.Validator{&pipeline.DefaultValidator{}},
		[]knowledge.Summarizer{&pipeline.DefaultSummarizer{MaxSummaryLen: 200}},
	)
	sd := planner.NewSourceDiscovery(reg, &testQueryPlanner{})
	pl := planner.NewKnowledgePlanner()
	rt := runtime.New(pl, sd, reg, pipe,
		[]runtime.Linker{&runtime.DefaultLinker{}},
		[]runtime.Reducer{&runtime.DefaultReducer{}},
	)

	comp := compiler.NewDefaultCompiler()
	ret := New(rt, comp)

	res, err := ret.Retrieve(context.Background(), Query{
		Text:    "how should we cache? " + marker,
		Formats: []compiler.Format{compiler.FormatPrompt},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if res == nil || res.Graph == nil {
		t.Fatal("expected non-nil result and graph")
	}

	// Retrieval worked: the seeded objects reached the graph.
	graphHasMarker := false
	for id := range res.Graph.Nodes {
		if strings.Contains(strings.ToLower(id), strings.ToLower(marker)) {
			graphHasMarker = true
			break
		}
	}
	if !graphHasMarker {
		t.Errorf("expected graph to contain seeded AKG node with marker %q", marker)
	}

	// Compile worked and the knowledge surfaced into the prompt (this is
	// exactly what A2 injects into the leader agent prompt).
	prompt, ok := res.Context.Formats[compiler.FormatPrompt]
	if !ok || prompt == "" {
		t.Fatal("expected non-empty FormatPrompt")
	}
	if !strings.Contains(prompt, marker) {
		t.Errorf("compiled prompt does not contain seeded marker %q; prompt head: %.200s", marker, prompt)
	}
}

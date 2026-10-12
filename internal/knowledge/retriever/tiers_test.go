package retriever_test

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/retriever"
	memstore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// TestRetrievalTier_L1_Lexicon pins that L1 (lexicon/keyword) finds exact
// keyword matches from the store.
func TestRetrievalTier_L1_Lexicon(t *testing.T) {
	t.Parallel()

	store := memstore.New()
	objects := []*knowledge.KnowledgeObject{
		{ID: "obj_redis", Type: knowledge.ObjectMemory, Namespace: "ns", Summary: "Redis for caching", Status: knowledge.StatusActive},
		{ID: "obj_postgres", Type: knowledge.ObjectMemory, Namespace: "ns", Summary: "PostgreSQL for persistence", Status: knowledge.StatusActive},
		{ID: "obj_auth", Type: knowledge.ObjectMemory, Namespace: "ns", Summary: "Auth via JWT tokens", Status: knowledge.StatusActive},
	}
	knowledge.StampWriteSchema(objects...)
	if err := store.Save(context.Background(), objects...); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tr := retriever.NewTieredRetriever(store, nil)

	result, tier, err := tr.Retrieve(context.Background(), retriever.Query{
		Text:       "Redis caching",
		Namespace:  "ns",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if tier != retriever.TierL1 {
		t.Errorf("expected tier L1, got %s", tier)
	}
	if result.Graph == nil || len(result.Graph.Nodes) == 0 {
		t.Fatal("expected non-empty graph from L1")
	}
	if _, ok := result.Graph.Nodes["obj_redis"]; !ok {
		t.Error("expected obj_redis in L1 results")
	}
}

// TestRetrievalTier_L1_NoMatchFallsToEmpty pins that L1 returns an empty
// graph when no keywords match, rather than erroring.
func TestRetrievalTier_L1_NoMatchFallsToEmpty(t *testing.T) {
	t.Parallel()

	store := memstore.New()
	obj := &knowledge.KnowledgeObject{
		ID: "obj1", Type: knowledge.ObjectMemory, Namespace: "ns",
		Summary: "Redis for caching", Status: knowledge.StatusActive,
	}
	knowledge.StampWriteSchema(obj)
	if err := store.Save(context.Background(), obj); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tr := retriever.NewTieredRetriever(store, nil)

	result, _, err := tr.Retrieve(context.Background(), retriever.Query{
		Text:       "completely unrelated query about quantum physics",
		Namespace:  "ns",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if result.Graph != nil && len(result.Graph.Nodes) > 0 {
		t.Errorf("expected empty graph for no match, got %d nodes", len(result.Graph.Nodes))
	}
}

// TestRetrievalTier_L1_RefusesUnscopedQuery pins the isolation guard: an L1
// search with no declared namespace must return nothing rather than scanning
// every namespace — a store drops its namespace predicate when it is empty, so
// an unscoped query would read across tenants/users.
func TestRetrievalTier_L1_RefusesUnscopedQuery(t *testing.T) {
	t.Parallel()

	store := memstore.New()
	obj := &knowledge.KnowledgeObject{
		ID: "obj1", Type: knowledge.ObjectMemory, Namespace: "ns",
		Summary: "Redis for caching", Status: knowledge.StatusActive,
	}
	knowledge.StampWriteSchema(obj)
	if err := store.Save(context.Background(), obj); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tr := retriever.NewTieredRetriever(store, nil)
	result, _, err := tr.Retrieve(context.Background(), retriever.Query{
		Text:       "Redis caching", // would match obj1, but no namespace declared
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if result.Graph != nil && len(result.Graph.Nodes) > 0 {
		t.Errorf("unscoped L1 must return nothing, got %d nodes", len(result.Graph.Nodes))
	}
}

// TestEvalHarness_PrecisionRecall pins that the eval harness correctly
// computes Precision@K and Recall@K from known data.
func TestEvalHarness_PrecisionRecall(t *testing.T) {
	t.Parallel()

	store := memstore.New()
	objects := []*knowledge.KnowledgeObject{
		{ID: "obj_redis", Type: knowledge.ObjectMemory, Namespace: "ns", Summary: "Redis for caching", Status: knowledge.StatusActive},
		{ID: "obj_postgres", Type: knowledge.ObjectMemory, Namespace: "ns", Summary: "PostgreSQL for persistence", Status: knowledge.StatusActive},
		{ID: "obj_auth", Type: knowledge.ObjectMemory, Namespace: "ns", Summary: "Auth via JWT", Status: knowledge.StatusActive},
	}
	knowledge.StampWriteSchema(objects...)
	if err := store.Save(context.Background(), objects...); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tr := retriever.NewTieredRetriever(store, nil)
	harness := retriever.NewEvalHarness(tr, nil)

	cases := []retriever.EvalCase{
		{
			Query:       "Redis caching",
			Namespace:   "ns",
			ExpectedIDs: []string{"obj_redis"},
		},
		{
			Query:       "PostgreSQL persistence",
			Namespace:   "ns",
			ExpectedIDs: []string{"obj_postgres"},
		},
	}

	report := harness.Run(context.Background(), cases)
	if len(report.Cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(report.Cases))
	}

	// Case 0: "Redis caching" should find obj_redis with high precision.
	c0 := report.Cases[0]
	if c0.PrecisionAtK < 0.5 {
		t.Errorf("case 0 PrecisionAtK = %v, want >= 0.5", c0.PrecisionAtK)
	}
	if c0.RecallAtK < 0.5 {
		t.Errorf("case 0 RecallAtK = %v, want >= 0.5", c0.RecallAtK)
	}
	if c0.RetrievalTier != "L1-lexicon" {
		t.Errorf("case 0 tier = %q, want %q", c0.RetrievalTier, "L1-lexicon")
	}
}

// TestRetrievalTier_String pins the String() representation of each tier.
func TestRetrievalTier_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tier retriever.RetrievalTier
		want string
	}{
		{retriever.TierL1, "L1-lexicon"},
		{retriever.TierL2, "L2-vector-graph"},
		{retriever.TierL3, "L3-llm-rerank"},
	}

	for _, tt := range tests {
		if got := tt.tier.String(); got != tt.want {
			t.Errorf("tier %d String() = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

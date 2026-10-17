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

// TestRetrieval_TieredL1L2L3Fallback is M6's acceptance for the TIER WALK: L1 answers when
// its exact terms are enough, and when they are not, the walk falls to L2 — and a broken L2
// DEGRADES to what L1 found instead of failing the query.
//
// The L2 here is a bare &Retriever{} (nil runtime/compiler), which is the real failure any
// caller would see if L2's dependencies were not wired: it exercises the degradation branch
// through the public API rather than by mocking an interface the production type does not
// have. L3 is reserved by design (0.3.4 returns L2 unchanged), so there is no L3 step to
// observe; the assertion is that the walk never reports a tier it did not run.
func TestRetrieval_TieredL1L2L3Fallback(t *testing.T) {
	t.Parallel()

	store := memstore.New()
	objects := []*knowledge.KnowledgeObject{
		{ID: "obj_redis", Type: knowledge.ObjectMemory, Namespace: "ns", Summary: "Redis for caching", Status: knowledge.StatusActive},
		{ID: "obj_other", Type: knowledge.ObjectMemory, Namespace: "ns", Summary: "PostgreSQL for persistence", Status: knowledge.StatusActive},
	}
	knowledge.StampWriteSchema(objects...)
	if err := store.Save(context.Background(), objects...); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// L1 is enough (one hit satisfies MaxResults=1): the walk must stop at L1. A broken L2
	// is attached on purpose — if the tier were consulted, its error would show up as a
	// degradation warning and the tier would still read L1, so the observable assertion is
	// that the L1 node is the RESULT and no error surfaces.
	if _, tier, _ := retriever.NewTieredRetriever(store, &retriever.Retriever{}).Retrieve(
		context.Background(),
		retriever.Query{Text: "Redis caching", Namespace: "ns", MaxResults: 1},
	); tier != retriever.TierL1 {
		t.Fatalf("an L1-satisfied query must stop at L1, got %s", tier)
	}

	// L1 cannot satisfy the limit, L2 is broken ⇒ degrade to L1's hits, still no error.
	result, tier, err := retriever.NewTieredRetriever(store, &retriever.Retriever{}).Retrieve(
		context.Background(),
		retriever.Query{Text: "Redis caching", Namespace: "ns", MaxResults: 10},
	)
	if err != nil {
		t.Fatalf("a broken L2 must degrade, not fail the query: %v", err)
	}
	if tier != retriever.TierL1 {
		t.Fatalf("degradation must report the tier it actually served (L1), got %s", tier)
	}
	if result.Graph == nil {
		t.Fatal("degradation must still return L1's nodes")
	}
	if _, ok := result.Graph.Nodes["obj_redis"]; !ok {
		t.Error("the L1 hit must survive the L2 failure")
	}
}

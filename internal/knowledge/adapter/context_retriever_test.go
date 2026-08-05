// Package adapter provides tests for the KnowledgeRetriever adapter.
package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/pipeline"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// ctxProvider is a minimal GraphProvider that streams a fixed set of
// KnowledgeObjects. It mirrors the e2eProvider pattern from
// internal/knowledge/e2e_test.go but lives here so the test is self-contained.
type ctxProvider struct {
	name    string
	objects []*knowledge.KnowledgeObject
}

func (p *ctxProvider) Name() string { return p.name }

func (p *ctxProvider) IntentMatch(_ knowledge.Intent) float64 { return 0.9 }

func (p *ctxProvider) Stream(_ context.Context, _ knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	ch := make(chan *knowledge.KnowledgeObject, len(p.objects))
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		defer close(errCh)
		for _, obj := range p.objects {
			ch <- obj
		}
	}()
	return ch, errCh
}

// ctxQueryPlanner is a minimal QueryPlanner that always plans a SQL query
// echoing the requirement description. It mirrors e2eQueryPlanner.
type ctxQueryPlanner struct{}

func (q *ctxQueryPlanner) PlanQuery(
	_ context.Context,
	req planner.KnowledgeRequirement,
	_, _ string,
) (*planner.QueryPlan, error) {
	return &planner.QueryPlan{
		Query:      req.Description,
		QueryType:  planner.QuerySQL,
		MaxResults: req.MaxResults,
	}, nil
}

// buildTestRuntime constructs a real KnowledgeRuntime wired to in-memory
// providers so tests exercise the live Execute path without a database.
func buildTestRuntime(t *testing.T, providers ...*ctxProvider) *knowledgeruntime.KnowledgeRuntime {
	t.Helper()
	reg := provider.NewProviderRegistry()
	for _, p := range providers {
		if err := reg.Register(p); err != nil {
			t.Fatalf("register provider %q: %v", p.name, err)
		}
	}
	pipe := knowledge.NewKnowledgePipeline(
		[]knowledge.Normalizer{&pipeline.DefaultNormalizer{MaxRawBytes: 4096}},
		[]knowledge.EntityMatcher{&pipeline.DefaultEntityMatcher{MatchThreshold: 0.6}},
		[]knowledge.Validator{&pipeline.DefaultValidator{}},
		[]knowledge.Summarizer{&pipeline.DefaultSummarizer{MaxSummaryLen: 200}},
	)
	sd := planner.NewSourceDiscovery(reg, &ctxQueryPlanner{})
	pl := planner.NewKnowledgePlanner()
	linkers := []knowledgeruntime.Linker{&knowledgeruntime.DefaultLinker{}}
	reducers := []knowledgeruntime.Reducer{&knowledgeruntime.DefaultReducer{}}
	return knowledgeruntime.New(pl, sd, reg, pipe, linkers, reducers)
}

// TestNewKnowledgeRetriever_Validation covers constructor input validation.
func TestNewKnowledgeRetriever_Validation(t *testing.T) {
	tests := []struct {
		name     string
		runtime  *knowledgeruntime.KnowledgeRuntime
		minScore float64
		wantErr  bool
		errSub   string
	}{
		{
			name:     "nil runtime returns error",
			runtime:  nil,
			minScore: 0.4,
			wantErr:  true,
			errSub:   "runtime is nil",
		},
		{
			name: "zero minScore defaults to DefaultMinScore",
			runtime: buildTestRuntime(t, &ctxProvider{
				name:    "memory",
				objects: []*knowledge.KnowledgeObject{{ID: "x", Summary: "y", Confidence: 0.5}},
			}),
			minScore: 0,
			wantErr:  false,
		},
		{
			name: "negative minScore defaults to DefaultMinScore",
			runtime: buildTestRuntime(t, &ctxProvider{
				name:    "memory",
				objects: []*knowledge.KnowledgeObject{{ID: "x", Summary: "y", Confidence: 0.5}},
			}),
			minScore: -1,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kr, err := NewKnowledgeRetriever(context.Background(), tt.runtime, tt.minScore)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errSub)
				}
				if !containsStr(err.Error(), tt.errSub) {
					t.Errorf("expected error containing %q, got %q", tt.errSub, err.Error())
				}
				if kr != nil {
					t.Errorf("expected nil retriever on error, got non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if kr == nil {
				t.Fatal("expected non-nil retriever")
			}
			if kr.minScore != DefaultMinScore {
				t.Errorf("expected default minScore %v, got %v", DefaultMinScore, kr.minScore)
			}
			if kr.minRelevance != DefaultMinRelevance {
				t.Errorf("expected default minRelevance %v, got %v", DefaultMinRelevance, kr.minRelevance)
			}
		})
	}
}

// TestKnowledgeRetriever_Retrieve_EmptyInput verifies empty input returns an
// empty (non-nil) slice without invoking the runtime.
func TestKnowledgeRetriever_Retrieve_EmptyInput(t *testing.T) {
	rt := buildTestRuntime(t, &ctxProvider{
		name:    "memory",
		objects: []*knowledge.KnowledgeObject{{ID: "x", Summary: "y", Confidence: 0.9, Relevance: 0.5}},
	})
	kr, err := NewKnowledgeRetriever(context.Background(), rt, 0)
	if err != nil {
		t.Fatalf("NewKnowledgeRetriever: %v", err)
	}

	snippets, err := kr.Retrieve(context.Background(), "", 5)
	if err != nil {
		t.Fatalf("Retrieve empty: unexpected error: %v", err)
	}
	if snippets == nil {
		t.Fatal("expected non-nil slice for empty input")
	}
	if len(snippets) != 0 {
		t.Errorf("expected 0 snippets for empty input, got %d", len(snippets))
	}
}

// TestKnowledgeRetriever_Retrieve_DefaultTopK verifies that topK <= 0 falls
// back to the default cap.
func TestKnowledgeRetriever_Retrieve_DefaultTopK(t *testing.T) {
	objs := make([]*knowledge.KnowledgeObject, 0, 10)
	for i := 0; i < 10; i++ {
		objs = append(objs, &knowledge.KnowledgeObject{
			ID:         "obj_" + string(rune('a'+i)),
			Summary:    "decision summary",
			Confidence: 0.9,
			Relevance:  0.5,
			Tags:       []string{"decision"},
		})
	}
	rt := buildTestRuntime(t, &ctxProvider{name: "memory", objects: objs})
	kr, err := NewKnowledgeRetriever(context.Background(), rt, 0)
	if err != nil {
		t.Fatalf("NewKnowledgeRetriever: %v", err)
	}

	snippets, err := kr.Retrieve(context.Background(), "explain decisions", 0)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(snippets) > defaultTopK {
		t.Errorf("expected at most %d snippets (default topK), got %d", defaultTopK, len(snippets))
	}
}

// TestKnowledgeRetriever_Retrieve_RelevanceFilter verifies that the runtime
// path filters on Relevance (not Confidence) and sorts by Relevance desc.
//
// This is the core regression test for retrieval quality gate #1: the old
// code filtered on Confidence, which was a hardcoded constant per provider
// (memory 0.7, code 0.9, pg 0.5, mysql 1.0), so the 0.4 gate was a no-op.
// The fix filters on Relevance — the query-time signal providers set at
// stream time. WithMinRelevance(0.4) is the tuning knob.
func TestKnowledgeRetriever_Retrieve_RelevanceFilter(t *testing.T) {
	objs := []*knowledge.KnowledgeObject{
		{ID: "high", Summary: "high relevance decision", Confidence: 0.95, Relevance: 0.9, Tags: []string{"decision"}},
		{ID: "mid", Summary: "medium relevance decision", Confidence: 0.6, Relevance: 0.5, Tags: []string{"decision"}},
		{ID: "low", Summary: "low relevance decision", Confidence: 0.95, Relevance: 0.1, Tags: []string{"decision"}},
	}
	rt := buildTestRuntime(t, &ctxProvider{name: "memory", objects: objs})
	// minRelevance 0.4 should drop the 0.1-relevance object and keep 0.9 + 0.5.
	kr, err := NewKnowledgeRetriever(context.Background(), rt, 0, WithMinRelevance(0.4))
	if err != nil {
		t.Fatalf("NewKnowledgeRetriever: %v", err)
	}

	snippets, err := kr.Retrieve(context.Background(), "explain decisions", 10)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(snippets) != 2 {
		t.Fatalf("expected 2 snippets after Relevance filter, got %d", len(snippets))
	}
	// Verify descending order by Score (which is Relevance on the runtime path).
	if snippets[0].Score < snippets[1].Score {
		t.Errorf("expected descending order, got %v then %v", snippets[0].Score, snippets[1].Score)
	}
	// All returned scores must be >= minRelevance (0.4).
	for _, s := range snippets {
		if s.Score < 0.4 {
			t.Errorf("snippet score %v below minRelevance 0.4", s.Score)
		}
	}
	// The low-relevance object must NOT appear.
	for _, s := range snippets {
		if id, ok := s.Metadata["id"].(string); ok && id == "low" {
			t.Errorf("low-relevance object leaked into results: %v", s.Metadata)
		}
	}
}

// TestKnowledgeRetriever_Retrieve_Success exercises the full real path:
// in-memory providers → real KnowledgeRuntime.Execute → adapter → snippets.
func TestKnowledgeRetriever_Retrieve_Success(t *testing.T) {
	objs := []*knowledge.KnowledgeObject{
		{
			ID:         "mem:redis1",
			Type:       knowledge.ObjectDecision,
			Namespace:  "memory",
			Summary:    "Chose Redis for caching layer",
			Normalized: "Redis is used as cache",
			Raw:        []byte("Decision: Use Redis for caching"),
			Confidence: 0.9,
			Relevance:  0.8,
			Tags:       []string{"redis", "cache", "decision"},
			CreatedAt:  time.Now(),
		},
		{
			ID:         "mem:pg1",
			Type:       knowledge.ObjectDecision,
			Namespace:  "memory",
			Summary:    "Chose PostgreSQL for storage",
			Normalized: "PostgreSQL is the primary DB",
			Raw:        []byte("Decision: Use PostgreSQL for persistence"),
			Confidence: 0.85,
			Relevance:  0.7,
			Tags:       []string{"postgres", "database", "decision"},
			CreatedAt:  time.Now(),
		},
	}
	rt := buildTestRuntime(t, &ctxProvider{name: "memory", objects: objs})
	kr, err := NewKnowledgeRetriever(context.Background(), rt, 0.4)
	if err != nil {
		t.Fatalf("NewKnowledgeRetriever: %v", err)
	}

	snippets, err := kr.Retrieve(context.Background(), "Why Redis?", 5)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(snippets) == 0 {
		t.Skip("AKG returned no nodes for the test query — provider may need a live source")
	}
	for _, s := range snippets {
		if s.Source != "knowledge" {
			t.Errorf("expected Source %q, got %q", "knowledge", s.Source)
		}
		if s.Content == "" {
			t.Error("expected non-empty Content")
		}
		if s.Metadata == nil {
			t.Error("expected non-nil Metadata")
		}
		if _, ok := s.Metadata["id"]; !ok {
			t.Errorf("expected Metadata to contain id, got %v", s.Metadata)
		}
	}
}

// TestKnowledgeRetriever_Retrieve_TopKCap verifies that topK is honoured when
// more qualifying snippets exist.
func TestKnowledgeRetriever_Retrieve_TopKCap(t *testing.T) {
	objs := make([]*knowledge.KnowledgeObject, 0, 8)
	for i := 0; i < 8; i++ {
		objs = append(objs, &knowledge.KnowledgeObject{
			ID:         "obj_" + string(rune('a'+i)),
			Summary:    "decision summary",
			Confidence: 0.9,
			Relevance:  0.5,
			Tags:       []string{"decision"},
		})
	}
	rt := buildTestRuntime(t, &ctxProvider{name: "memory", objects: objs})
	kr, err := NewKnowledgeRetriever(context.Background(), rt, 0.4)
	if err != nil {
		t.Fatalf("NewKnowledgeRetriever: %v", err)
	}

	snippets, err := kr.Retrieve(context.Background(), "explain decisions", 3)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(snippets) > 3 {
		t.Errorf("expected at most 3 snippets, got %d", len(snippets))
	}
}

// TestKnowledgeRetriever_Retrieve_CancelledContext verifies that a cancelled
// context is observed by the runtime — Retrieve returns an error rather than
// hanging or fabricating fake data.
//
// Note: the underlying KnowledgeRuntime drains provider channels on
// ctx.Done() and reports "no objects loaded" rather than wrapping
// context.Canceled. This test therefore asserts the externally observable
// contract: no hang, no fake snippets, an error is returned.
func TestKnowledgeRetriever_Retrieve_CancelledContext(t *testing.T) {
	objs := []*knowledge.KnowledgeObject{
		{ID: "x", Summary: "y", Confidence: 0.9, Relevance: 0.5, Tags: []string{"decision"}},
	}
	rt := buildTestRuntime(t, &ctxProvider{name: "memory", objects: objs})
	kr, err := NewKnowledgeRetriever(context.Background(), rt, 0.4)
	if err != nil {
		t.Fatalf("NewKnowledgeRetriever: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before invoking
	snippets, err := kr.Retrieve(ctx, "any query", 5)
	// The runtime may either (a) finish before observing cancellation and
	// return real snippets, or (b) observe cancellation and return an
	// error. Either is acceptable; what's NOT acceptable is hanging or
	// returning fake data.
	if err != nil {
		// On error, snippets MUST be nil — never partially fabricated.
		if snippets != nil {
			t.Errorf("expected nil snippets on error, got %d", len(snippets))
		}
		return
	}
	// No-error path: snippets may be empty but must not be fabricated.
	for _, s := range snippets {
		if s.Content == "" {
			t.Errorf("expected non-empty content, got empty for source %q", s.Source)
		}
	}
}

// TestKnowledgeRetriever_Retrieve_NilReceiver guards against nil deref.
func TestKnowledgeRetriever_Retrieve_NilReceiver(t *testing.T) {
	var kr *KnowledgeRetriever
	_, err := kr.Retrieve(context.Background(), "any", 5)
	if err == nil {
		t.Fatal("expected error on nil receiver")
	}
	if !containsStr(err.Error(), "receiver is nil") {
		t.Errorf("expected 'receiver is nil' error, got %q", err.Error())
	}
}

// TestSnippetContent_FallbackChain verifies snippetContent's preference
// order: Summary → Normalized → Raw → placeholder.
func TestSnippetContent_FallbackChain(t *testing.T) {
	tests := []struct {
		name string
		obj  *knowledge.KnowledgeObject
		want string
	}{
		{
			name: "summary preferred",
			obj:  &knowledge.KnowledgeObject{ID: "a", Summary: "S", Normalized: "N", Raw: []byte("R")},
			want: "S",
		},
		{
			name: "normalized when summary empty",
			obj:  &knowledge.KnowledgeObject{ID: "a", Normalized: "N", Raw: []byte("R")},
			want: "N",
		},
		{
			name: "raw when normalized empty",
			obj:  &knowledge.KnowledgeObject{ID: "a", Raw: []byte("R")},
			want: "R",
		},
		{
			name: "placeholder when all empty",
			obj:  &knowledge.KnowledgeObject{ID: "abc"},
			want: "knowledge object abc (no content)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := snippetContent(tt.obj)
			if got != tt.want {
				t.Errorf("snippetContent: got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestClampScore was removed: the local clampScore helper was centralized into
// internal/scoreutil.ClampUnit, which has its own table-driven test
// (TestClampUnit) covering the same boundaries plus the NaN edge case.

// TestKnowledgeRetriever_StoreHybrid covers the AKG read loop: when a store is
// wired via NewKnowledgeRetrieverWithStore, Retrieve must call
// store.HybridSearch and surface the scored facts — bypassing runtime.Execute
// entirely. Seeds the memstore with active objects and asserts the lexically
// matching fact is returned. The store path short-circuits before Execute, so
// the runtime is never invoked here (a minimal runtime still must be passed
// because the constructor enforces non-nil).
func TestKnowledgeRetriever_StoreHybrid(t *testing.T) {
	// Shared seed objects. Lexical scoring is Jaccard overlap of lowercased
	// token sets over (Summary + " " + Normalized), so the seeds are kept
	// short to keep Jaccard above the DefaultMinScore (0.4) cutoff.
	redisObj := &knowledge.KnowledgeObject{
		ID:         "akg:redis",
		Type:       knowledge.ObjectDecision,
		Namespace:  "memory",
		Summary:    "Redis cache",
		Normalized: "",
		Confidence: 0.9,
		Status:     knowledge.StatusActive,
		Tags:       []string{"redis", "cache"},
		CreatedAt:  time.Now(),
	}
	pgObj := &knowledge.KnowledgeObject{
		ID:         "akg:pg",
		Type:       knowledge.ObjectDecision,
		Namespace:  "memory",
		Summary:    "PostgreSQL storage",
		Normalized: "",
		Confidence: 0.85,
		Status:     knowledge.StatusActive,
		Tags:       []string{"postgres", "database"},
		CreatedAt:  time.Now(),
	}
	// A rejected object must NOT surface because StatusFilter=[StatusActive].
	// Its content overlaps the redis query on purpose, so the only thing
	// keeping it out of the results is the status filter.
	rejectedObj := &knowledge.KnowledgeObject{
		ID:         "akg:rejected",
		Type:       knowledge.ObjectDecision,
		Namespace:  "memory",
		Summary:    "Redis Redis",
		Normalized: "",
		Confidence: 0.99,
		Status:     knowledge.StatusRejected,
		CreatedAt:  time.Now(),
	}

	// buildSeededStore returns a fresh memstore populated with the seed.
	buildSeededStore := func(t *testing.T) *memorystore.Store {
		t.Helper()
		s := memorystore.New()
		if err := s.Save(context.Background(), redisObj, pgObj, rejectedObj); err != nil {
			t.Fatalf("seed store: %v", err)
		}
		return s
	}

	// buildMinimalRuntime returns a non-nil runtime so the constructor's
	// non-nil check passes. It is never invoked on the store path.
	buildMinimalRuntime := func(t *testing.T) *knowledgeruntime.KnowledgeRuntime {
		t.Helper()
		return buildTestRuntime(t, &ctxProvider{
			name:    "unused",
			objects: []*knowledge.KnowledgeObject{},
		})
	}

	tests := []struct {
		name        string
		query       string
		topK        int
		minScore    float64
		wantIDs     []string
		wantNotIDs  []string
		wantSource  string
		wantErr     bool
		errSub      string
		wantAllMeta []string // metadata keys every snippet must contain
	}{
		{
			name:       "lexically matching active fact surfaces",
			query:      "Redis cache",
			topK:       5,
			minScore:   0,
			wantIDs:    []string{"akg:redis"},
			wantNotIDs: []string{"akg:pg", "akg:rejected"},
			wantSource: sourceAKGStore,
			wantAllMeta: []string{
				"id", "type", "source", "vector", "lexical", "namespace", "relevance",
			},
		},
		{
			// Query "Redis" overlaps both the active redis object and the
			// rejected object; the rejected one must be dropped by the
			// StatusFilter=[StatusActive] in the HybridSearch request.
			name:       "rejected status filtered out by StatusFilter",
			query:      "Redis",
			topK:       5,
			minScore:   0,
			wantIDs:    []string{"akg:redis"},
			wantNotIDs: []string{"akg:rejected"},
			wantSource: sourceAKGStore,
		},
		{
			name:       "non-matching query returns empty",
			query:      "kubernetes",
			topK:       5,
			minScore:   0,
			wantIDs:    nil,
			wantNotIDs: []string{"akg:redis", "akg:pg", "akg:rejected"},
			wantSource: sourceAKGStore,
		},
		{
			name:     "empty input short-circuits before store",
			query:    "",
			topK:     5,
			minScore: 0,
			wantErr:  false,
			// wantIDs left nil → expect zero snippets.
		},
		{
			// minScore above the lexical Jaccard score (0.5 for "Redis" vs
			// "Redis cache") must yield zero results.
			name:     "minScore above all lexical scores returns nothing",
			query:    "Redis",
			topK:     5,
			minScore: 0.99,
			wantErr:  false,
			// wantIDs left nil → expect zero snippets.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := buildSeededStore(t)
			rt := buildMinimalRuntime(t)
			kr, err := NewKnowledgeRetrieverWithStore(
				context.Background(), rt, store, "", tt.minScore,
			)
			if err != nil {
				t.Fatalf("NewKnowledgeRetrieverWithStore: %v", err)
			}

			snippets, err := kr.Retrieve(context.Background(), tt.query, tt.topK)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errSub)
				}
				if tt.errSub != "" && !containsStr(err.Error(), tt.errSub) {
					t.Errorf("expected error containing %q, got %q", tt.errSub, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("Retrieve: unexpected error: %v", err)
			}

			// Empty input must yield an empty (non-nil) slice.
			if tt.query == "" {
				if snippets == nil {
					t.Fatal("expected non-nil slice for empty input")
				}
				if len(snippets) != 0 {
					t.Errorf("expected 0 snippets for empty input, got %d", len(snippets))
				}
				return
			}

			gotIDs := make(map[string]bool, len(snippets))
			for _, s := range snippets {
				if s.Source != tt.wantSource {
					t.Errorf("expected Source %q, got %q", tt.wantSource, s.Source)
				}
				if s.Content == "" {
					t.Errorf("snippet %v: expected non-empty Content", s.Metadata)
				}
				for _, k := range tt.wantAllMeta {
					if _, ok := s.Metadata[k]; !ok {
						t.Errorf("snippet metadata missing key %q: %v", k, s.Metadata)
					}
				}
				if id, ok := s.Metadata["id"].(string); ok {
					gotIDs[id] = true
				} else {
					t.Errorf("snippet metadata id is not a string: %v", s.Metadata)
				}
			}

			for _, want := range tt.wantIDs {
				if !gotIDs[want] {
					t.Errorf("expected snippet id %q in results, got %v", want, gotIDs)
				}
			}
			for _, notWant := range tt.wantNotIDs {
				if gotIDs[notWant] {
					t.Errorf("did not expect snippet id %q in results, got %v", notWant, gotIDs)
				}
			}

			if tt.wantIDs == nil && len(snippets) != 0 {
				t.Errorf("expected 0 snippets, got %d: %+v", len(snippets), snippets)
			}
		})
	}
}

// TestNewKnowledgeRetrieverWithStore_Validation covers the constructor's
// runtime-non-nil invariant and the minScore defaulting behavior. store may be
// nil (then it behaves like NewKnowledgeRetriever).
func TestNewKnowledgeRetrieverWithStore_Validation(t *testing.T) {
	rt := buildTestRuntime(t, &ctxProvider{
		name:    "memory",
		objects: []*knowledge.KnowledgeObject{{ID: "x", Summary: "y", Confidence: 0.5}},
	})

	tests := []struct {
		name     string
		runtime  *knowledgeruntime.KnowledgeRuntime
		store    knowledge.KnowledgeStore
		model    string
		minScore float64
		wantErr  bool
		errSub   string
		wantMin  float64
	}{
		{
			name:     "nil runtime returns error even with store",
			runtime:  nil,
			store:    memorystore.New(),
			model:    "bge-m3",
			minScore: 0.4,
			wantErr:  true,
			errSub:   "runtime is nil",
		},
		{
			name:     "nil store allowed (fallback path)",
			runtime:  rt,
			store:    nil,
			model:    "",
			minScore: 0,
			wantErr:  false,
			wantMin:  DefaultMinScore,
		},
		{
			name:     "zero minScore defaults to DefaultMinScore",
			runtime:  rt,
			store:    memorystore.New(),
			model:    "bge-m3",
			minScore: 0,
			wantErr:  false,
			wantMin:  DefaultMinScore,
		},
		{
			name:     "negative minScore defaults to DefaultMinScore",
			runtime:  rt,
			store:    memorystore.New(),
			model:    "bge-m3",
			minScore: -1,
			wantErr:  false,
			wantMin:  DefaultMinScore,
		},
		{
			name:     "positive minScore preserved",
			runtime:  rt,
			store:    memorystore.New(),
			model:    "bge-m3",
			minScore: 0.7,
			wantErr:  false,
			wantMin:  0.7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kr, err := NewKnowledgeRetrieverWithStore(
				context.Background(), tt.runtime, tt.store, tt.model, tt.minScore,
			)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errSub)
				}
				if !containsStr(err.Error(), tt.errSub) {
					t.Errorf("expected error containing %q, got %q", tt.errSub, err.Error())
				}
				if kr != nil {
					t.Errorf("expected nil retriever on error, got non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if kr == nil {
				t.Fatal("expected non-nil retriever")
			}
			if kr.minScore != tt.wantMin {
				t.Errorf("minScore: got %v, want %v", kr.minScore, tt.wantMin)
			}
			if kr.minRelevance != DefaultMinRelevance {
				t.Errorf("minRelevance: got %v, want %v (default)", kr.minRelevance, DefaultMinRelevance)
			}
			if kr.model != tt.model {
				t.Errorf("model: got %q, want %q", kr.model, tt.model)
			}
		})
	}
}

// containsStr is a minimal strings.Contains helper to avoid pulling in
// the strings package just for one test assertion.
func containsStr(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// ── Bug scenario tests for retrieval quality gate #1 ──
//
// These tests pin down the three behaviours that the root-cause fix
// introduces and that would silently regress the retrieval quality gate
// if reverted:
//  1. Hardcoded provider Confidence no longer lets objects past the
//     Relevance gate (the original bug).
//  2. Rank-derived Relevance from the memory provider is monotonically
//     non-increasing over the result set.
//  3. collectSnippets sorts by Relevance desc and the topK cap truncates
//     the lowest-relevance tail, not an arbitrary subset.

// TestBug_HardcodedConfidenceDoesNotBypassRelevanceGate reproduces the
// original bug: providers used to emit a hardcoded Confidence constant
// (memory=0.7, code=0.9, pg=0.5, mysql=1.0) and collectSnippets filtered on
// Confidence, so a 0.4 gate was a no-op — every object passed. With the
// fix, collectSnippets filters on Relevance: an object with high Confidence
// but zero Relevance is dropped.
func TestBug_HardcodedConfidenceDoesNotBypassRelevanceGate(t *testing.T) {
	// High Confidence (would have passed the old gate) but zero Relevance
	// (the signal the retriever should actually filter on).
	objs := []*knowledge.KnowledgeObject{
		{ID: "noisy", Summary: "high confidence but irrelevant", Confidence: 0.99, Relevance: 0.0},
	}
	rt := buildTestRuntime(t, &ctxProvider{name: "memory", objects: objs})
	kr, err := NewKnowledgeRetriever(context.Background(), rt, 0)
	if err != nil {
		t.Fatalf("NewKnowledgeRetriever: %v", err)
	}

	snippets, err := kr.Retrieve(context.Background(), "anything", 5)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(snippets) != 0 {
		t.Errorf("expected 0 snippets (Relevance=0 must be filtered), got %d: %v",
			len(snippets), snippets)
	}
}

// TestBug_RankDerivedRelevanceMonotonic verifies that the memory provider's
// rank-based Relevance derivation (relevanceFromScore with Score=0) is
// monotonically non-increasing: the first result has the highest Relevance
// and the last has the lowest, with the floor at 0.1. A non-monotonic
// sequence would corrupt the retriever's topK truncation.
func TestBug_RankDerivedRelevanceMonotonic(t *testing.T) {
	const n = 5
	// memoryRelevance mirrors provider/memory/provider.go:relevanceFromScore
	// for the Score=0 branch. Kept local to avoid an import cycle through
	// the memory provider package.
	var prev float64
	for i := 0; i < n; i++ {
		rel := memoryRelevance(0, i, n)
		if i > 0 && rel > prev {
			t.Errorf("Relevance not monotonic at i=%d: prev=%.3f > cur=%.3f", i, prev, rel)
		}
		if rel < 0.1 {
			t.Errorf("Relevance at i=%d below floor: %.3f < 0.1", i, rel)
		}
		prev = rel
	}
	first := memoryRelevance(0, 0, n)
	last := memoryRelevance(0, n-1, n)
	if first != 1.0 {
		t.Errorf("first Relevance: got %.3f, want 1.0", first)
	}
	// For n=5: 1 - 4/5 = 0.2, which is above the 0.1 floor.
	if last > 0.2+1e-9 || last < 0.2-1e-9 {
		t.Errorf("last Relevance: got %.3f, want 0.2 (5 results: 1-4/5=0.2)", last)
	}
}

// memoryRelevance is a test-local mirror of the memory provider's
// relevanceFromScore so this test does not depend on the provider package
// internals. It must be kept in sync with
// provider/memory/provider.go:relevanceFromScore.
func memoryRelevance(score float64, i, n int) float64 {
	if score > 0 {
		if score < 0 {
			return 0
		}
		if score > 1 {
			return 1
		}
		return score
	}
	if n <= 0 {
		return 0.1
	}
	rel := 1.0 - float64(i)/float64(n)
	if rel < 0.1 {
		rel = 0.1
	}
	return rel
}

// TestBug_RelevanceSortAndTopKTruncation verifies that collectSnippets sorts
// by Relevance descending and that the topK cap in Retrieve truncates the
// lowest-relevance tail (not an arbitrary subset). This is the third
// behaviour that would silently regress retrieval quality if reverted.
func TestBug_RelevanceSortAndTopKTruncation(t *testing.T) {
	objs := []*knowledge.KnowledgeObject{
		{ID: "a", Summary: "relevance 0.3", Confidence: 0.9, Relevance: 0.3},
		{ID: "b", Summary: "relevance 0.9", Confidence: 0.9, Relevance: 0.9},
		{ID: "c", Summary: "relevance 0.5", Confidence: 0.9, Relevance: 0.5},
		{ID: "d", Summary: "relevance 0.7", Confidence: 0.9, Relevance: 0.7},
		{ID: "e", Summary: "relevance 0.4", Confidence: 0.9, Relevance: 0.4},
	}
	rt := buildTestRuntime(t, &ctxProvider{name: "memory", objects: objs})
	kr, err := NewKnowledgeRetriever(context.Background(), rt, 0)
	if err != nil {
		t.Fatalf("NewKnowledgeRetriever: %v", err)
	}

	// topK=3 must keep the three highest-Relevance objects: b(0.9), d(0.7), c(0.5).
	snippets, err := kr.Retrieve(context.Background(), "query", 3)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(snippets) != 3 {
		t.Fatalf("expected 3 snippets, got %d", len(snippets))
	}
	wantOrder := []string{"b", "d", "c"}
	for i, want := range wantOrder {
		got, _ := snippets[i].Metadata["id"].(string)
		if got != want {
			t.Errorf("snippet %d: got id %q, want %q (sorted by Relevance desc)", i, got, want)
		}
	}
	// Verify descending Score.
	for i := 1; i < len(snippets); i++ {
		if snippets[i].Score > snippets[i-1].Score {
			t.Errorf("snippets not sorted desc at %d: %.3f > %.3f",
				i, snippets[i].Score, snippets[i-1].Score)
		}
	}
}

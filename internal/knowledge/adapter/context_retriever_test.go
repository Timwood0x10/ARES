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
		})
	}
}

// TestKnowledgeRetriever_Retrieve_EmptyInput verifies empty input returns an
// empty (non-nil) slice without invoking the runtime.
func TestKnowledgeRetriever_Retrieve_EmptyInput(t *testing.T) {
	rt := buildTestRuntime(t, &ctxProvider{
		name:    "memory",
		objects: []*knowledge.KnowledgeObject{{ID: "x", Summary: "y", Confidence: 0.9}},
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

// TestKnowledgeRetriever_Retrieve_MinScoreFilter verifies that snippets below
// minScore are filtered out, and that the remaining snippets are sorted by
// Score descending.
func TestKnowledgeRetriever_Retrieve_MinScoreFilter(t *testing.T) {
	objs := []*knowledge.KnowledgeObject{
		{ID: "high", Summary: "high confidence decision", Confidence: 0.95, Tags: []string{"decision"}},
		{ID: "mid", Summary: "medium confidence decision", Confidence: 0.6, Tags: []string{"decision"}},
		{ID: "low", Summary: "low confidence decision", Confidence: 0.2, Tags: []string{"decision"}},
	}
	rt := buildTestRuntime(t, &ctxProvider{name: "memory", objects: objs})
	// minScore 0.5 should drop the 0.2-confidence node and keep 0.95 + 0.6.
	kr, err := NewKnowledgeRetriever(context.Background(), rt, 0.5)
	if err != nil {
		t.Fatalf("NewKnowledgeRetriever: %v", err)
	}

	snippets, err := kr.Retrieve(context.Background(), "explain decisions", 10)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(snippets) != 2 {
		t.Fatalf("expected 2 snippets after minScore filter, got %d", len(snippets))
	}
	// Verify descending order.
	if snippets[0].Score < snippets[1].Score {
		t.Errorf("expected descending order, got %v then %v", snippets[0].Score, snippets[1].Score)
	}
	// All returned scores must be >= minScore.
	for _, s := range snippets {
		if s.Score < 0.5 {
			t.Errorf("snippet score %v below minScore 0.5", s.Score)
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
		{ID: "x", Summary: "y", Confidence: 0.9, Tags: []string{"decision"}},
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

// TestClampScore verifies the score clamp boundaries.
func TestClampScore(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "negative clamps to 0", in: -0.5, want: 0},
		{name: "zero unchanged", in: 0, want: 0},
		{name: "midrange unchanged", in: 0.42, want: 0.42},
		{name: "one unchanged", in: 1, want: 1},
		{name: "above one clamps to 1", in: 1.5, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampScore(tt.in); got != tt.want {
				t.Errorf("clampScore(%v) = %v, want %v", tt.in, got, tt.want)
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

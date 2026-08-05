package retriever

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// testProvider for retriever tests.
type testProvider struct {
	name    string
	objects []*knowledge.KnowledgeObject
}

func (p *testProvider) Name() string                           { return p.name }
func (p *testProvider) IntentMatch(_ knowledge.Intent) float64 { return 0.9 }
func (p *testProvider) Stream(_ context.Context, _ knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
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

// testQueryPlanner for tests.
type testQueryPlanner struct{}

func (q *testQueryPlanner) PlanQuery(_ context.Context, req planner.KnowledgeRequirement, _, _ string) (*planner.QueryPlan, error) {
	return &planner.QueryPlan{Query: req.Description, QueryType: planner.QuerySQL, MaxResults: req.MaxResults}, nil
}

func TestRetriever_EmptyQuery(t *testing.T) {
	r := New(nil, nil)
	_, err := r.Retrieve(context.Background(), Query{})
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestRetriever_NilRuntime(t *testing.T) {
	r := New(nil, compiler.NewDefaultCompiler())
	_, err := r.Retrieve(context.Background(), Query{Text: "test"})
	if err == nil {
		t.Error("expected error for nil runtime")
	}
}

func TestRetriever_FullPipeline(t *testing.T) {
	reg := provider.NewProviderRegistry()
	_ = reg.Register(&testProvider{
		name: "memory",
		objects: []*knowledge.KnowledgeObject{
			{ID: "d1", Type: knowledge.ObjectDecision, Summary: "Chose Redis", Confidence: 0.9, Tags: []string{"redis"}},
			{ID: "a1", Type: knowledge.ObjectArchitecture, Summary: "Cache layer with Redis", Confidence: 0.8, Tags: []string{"redis", "cache"}},
		},
	})
	sd := planner.NewSourceDiscovery(reg, &testQueryPlanner{})
	p := planner.NewKnowledgePlanner()
	rt := runtime.New(p, sd, reg, nil,
		[]runtime.Linker{&runtime.DefaultLinker{}},
		[]runtime.Reducer{&runtime.DefaultReducer{}},
	)

	comp := compiler.NewDefaultCompiler()
	ret := New(rt, comp)

	result, err := ret.Retrieve(context.Background(), Query{
		Text:       "Why Redis?",
		MaxResults: 10,
		MaxTokens:  2000,
		Formats:    []compiler.Format{compiler.FormatPrompt, compiler.FormatJSON},
	})
	if err != nil {
		t.Fatalf("Retrieve error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Context == nil {
		t.Fatal("expected non-nil compiled context")
	}
	if len(result.Context.Formats) != 2 {
		t.Errorf("expected 2 formats, got %d", len(result.Context.Formats))
	}
	if _, ok := result.Context.Formats[compiler.FormatPrompt]; !ok {
		t.Error("expected prompt format")
	}
	if _, ok := result.Context.Formats[compiler.FormatJSON]; !ok {
		t.Error("expected JSON format")
	}
	if result.Graph == nil {
		t.Fatal("expected non-nil graph")
	}
	if result.Query != "Why Redis?" {
		t.Errorf("expected query 'Why Redis?', got %q", result.Query)
	}
}

func TestRetriever_ResultsCapped(t *testing.T) {
	reg := provider.NewProviderRegistry()
	_ = reg.Register(&testProvider{
		name: "memory",
		objects: []*knowledge.KnowledgeObject{
			{ID: "a", Summary: "first", Confidence: 0.9},
			{ID: "b", Summary: "second", Confidence: 0.8},
			{ID: "c", Summary: "third", Confidence: 0.7},
		},
	})
	sd := planner.NewSourceDiscovery(reg, &testQueryPlanner{})
	p := planner.NewKnowledgePlanner()
	rt := runtime.New(p, sd, reg, nil, nil, []runtime.Reducer{&runtime.DefaultReducer{}})
	ret := New(rt, compiler.NewDefaultCompiler())

	result, err := ret.Retrieve(context.Background(), Query{
		Text:       "test",
		MaxResults: 2,
		MaxTokens:  200,
	})
	if err != nil {
		t.Fatalf("Retrieve error: %v", err)
	}
	if len(result.Graph.Nodes) > 2 {
		t.Errorf("expected at most 2 nodes, got %d", len(result.Graph.Nodes))
	}
}

// TestRetriever_TypesFilter verifies that Query.Types is honored: only nodes
// whose Type is in the requested set survive, and edges referencing dropped
// nodes are pruned. This guards against the bug where Types was silently
// ignored (the built intent was discarded and nil was passed to the runtime).
func TestRetriever_TypesFilter(t *testing.T) {
	reg := provider.NewProviderRegistry()
	_ = reg.Register(&testProvider{
		name: "memory",
		objects: []*knowledge.KnowledgeObject{
			{ID: "d1", Type: knowledge.ObjectDecision, Summary: "Chose Redis", Confidence: 0.9},
			{ID: "a1", Type: knowledge.ObjectArchitecture, Summary: "Cache layer", Confidence: 0.8},
			{ID: "c1", Type: knowledge.ObjectCode, Summary: "main.go", Confidence: 0.7},
		},
	})
	sd := planner.NewSourceDiscovery(reg, &testQueryPlanner{})
	p := planner.NewKnowledgePlanner()
	rt := runtime.New(p, sd, reg, nil, nil, []runtime.Reducer{&runtime.DefaultReducer{}})
	ret := New(rt, compiler.NewDefaultCompiler())

	// Restrict to a single type.
	result, err := ret.Retrieve(context.Background(), Query{
		Text:       "Why Redis?",
		Types:      []knowledge.ObjectType{knowledge.ObjectDecision},
		MaxResults: 10,
		MaxTokens:  2000,
	})
	if err != nil {
		t.Fatalf("Retrieve error: %v", err)
	}
	if len(result.Graph.Nodes) != 1 {
		t.Fatalf("expected 1 node after type filter, got %d", len(result.Graph.Nodes))
	}
	for _, obj := range result.Graph.Nodes {
		if obj.Type != knowledge.ObjectDecision {
			t.Errorf("expected only decision nodes, got type %q", obj.Type)
		}
	}
	if len(result.Graph.Edges) != 0 {
		t.Errorf("expected 0 edges (no surviving pair), got %d", len(result.Graph.Edges))
	}

	// Restrict to multiple types.
	result, err = ret.Retrieve(context.Background(), Query{
		Text:       "Why Redis?",
		Types:      []knowledge.ObjectType{knowledge.ObjectDecision, knowledge.ObjectArchitecture},
		MaxResults: 10,
		MaxTokens:  2000,
	})
	if err != nil {
		t.Fatalf("Retrieve error: %v", err)
	}
	gotTypes := make(map[knowledge.ObjectType]bool)
	for _, obj := range result.Graph.Nodes {
		gotTypes[obj.Type] = true
	}
	if len(result.Graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes after type filter, got %d", len(result.Graph.Nodes))
	}
	if !gotTypes[knowledge.ObjectDecision] || !gotTypes[knowledge.ObjectArchitecture] {
		t.Errorf("expected decision+architecture nodes, got %v", gotTypes)
	}

	// Empty Types returns all types (no filtering).
	result, err = ret.Retrieve(context.Background(), Query{
		Text:       "Why Redis?",
		MaxResults: 10,
		MaxTokens:  2000,
	})
	if err != nil {
		t.Fatalf("Retrieve error: %v", err)
	}
	if len(result.Graph.Nodes) != 3 {
		t.Errorf("expected 3 nodes with no type filter, got %d", len(result.Graph.Nodes))
	}
}

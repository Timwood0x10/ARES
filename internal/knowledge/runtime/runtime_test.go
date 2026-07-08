package runtime

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
)

func TestDefaultLinker(t *testing.T) {
	linker := &DefaultLinker{}
	objects := []*knowledge.KnowledgeObject{
		{ID: "a", Tags: []string{"redis", "cache"}},
		{ID: "b", Tags: []string{"redis", "db"}},
		{ID: "c", Tags: []string{"cache", "memcached"}},
	}

	edges, err := linker.Link(context.Background(), objects)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}

	if len(edges) == 0 {
		t.Error("expected at least one edge from tag-based linking")
	}

	// Verify edges connect valid nodes.
	for _, e := range edges {
		if e.From != "a" && e.From != "b" && e.From != "c" {
			t.Errorf("unexpected from node: %s", e.From)
		}
		if e.Name != knowledge.RelBelongsTo {
			t.Errorf("expected %s relation, got %s", knowledge.RelBelongsTo, e.Name)
		}
	}
}

func TestDefaultLinkerEmptyObjects(t *testing.T) {
	linker := &DefaultLinker{}
	edges, err := linker.Link(context.Background(), nil)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges for nil input, got %d", len(edges))
	}
}

func TestDefaultReducer(t *testing.T) {
	reducer := &DefaultReducer{}
	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"high":   {ID: "high", Summary: "high confidence node", Confidence: 0.9},
			"medium": {ID: "medium", Summary: "medium confidence", Confidence: 0.5},
			"low":    {ID: "low", Summary: "low confidence node", Confidence: 0.1},
		},
		Edges: []knowledge.Relation{
			{From: "high", To: "low", Name: "related"},
		},
	}

	budget := knowledge.TokenBudget{ForGraph: 100} // ~2 nodes max
	reduced, err := reducer.Reduce(context.Background(), graph, budget)
	if err != nil {
		t.Fatalf("Reduce error: %v", err)
	}

	if len(reduced.Nodes) > 2 {
		t.Errorf("expected at most 2 nodes after pruning, got %d", len(reduced.Nodes))
	}

	// High confidence node should be kept.
	if _, ok := reduced.Nodes["high"]; !ok {
		t.Error("expected high-confidence node to be kept")
	}
}

func TestDefaultReducerZeroBudgetKeepsAll(t *testing.T) {
	reducer := &DefaultReducer{}
	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"a": {ID: "a", Summary: "node a", Confidence: 0.8},
			"b": {ID: "b", Summary: "node b", Confidence: 0.6},
			"c": {ID: "c", Summary: "node c", Confidence: 0.4},
		},
	}

	// Budget unset (ForGraph == 0): the reducer must not collapse the graph
	// to a single node (B16 regression). All nodes must be retained.
	reduced, err := reducer.Reduce(context.Background(), graph, knowledge.TokenBudget{})
	if err != nil {
		t.Fatalf("Reduce error: %v", err)
	}
	if len(reduced.Nodes) != 3 {
		t.Errorf("expected all 3 nodes retained when budget is unset, got %d", len(reduced.Nodes))
	}
}

func TestDefaultReducerWithinBudget(t *testing.T) {
	reducer := &DefaultReducer{}
	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"a": {ID: "a", Summary: "node a", Confidence: 0.8},
		},
	}

	budget := knowledge.TokenBudget{ForGraph: 10000}
	reduced, err := reducer.Reduce(context.Background(), graph, budget)
	if err != nil {
		t.Fatalf("Reduce error: %v", err)
	}

	if len(reduced.Nodes) != 1 {
		t.Errorf("expected 1 node (within budget), got %d", len(reduced.Nodes))
	}
}

func TestDefaultReducerEmptyGraph(t *testing.T) {
	reducer := &DefaultReducer{}
	result, err := reducer.Reduce(context.Background(), nil, knowledge.TokenBudget{})
	if err != nil {
		t.Fatalf("Reduce error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil graph")
	}
}

func TestLazyGraphCreate(t *testing.T) {
	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"a": {ID: "a", Summary: "node a", Confidence: 0.9},
			"b": {ID: "b", Summary: "node b", Confidence: 0.8},
		},
		Edges: []knowledge.Relation{
			{From: "a", To: "b", Name: "depends_on"},
		},
	}

	lg := NewLazyGraph(graph, nil)
	if lg.NodeCount() != 2 {
		t.Errorf("expected 2 nodes, got %d", lg.NodeCount())
	}

	node := lg.GetNode("a")
	if node == nil {
		t.Fatal("expected node 'a'")
	}
	if node.Summary != "node a" {
		t.Errorf("expected summary 'node a', got '%s'", node.Summary)
	}
	if !node.IsExpanded() {
		t.Error("expected node to be expanded (data was pre-loaded)")
	}
}

func TestLazyGraphFromSummaries(t *testing.T) {
	expandCalled := false
	summaries := map[string]string{
		"a": "summary a",
		"b": "summary b",
	}

	lg := NewLazyGraphFromSummaries(summaries, nil,
		func(_ context.Context, id string) (*knowledge.KnowledgeObject, error) {
			expandCalled = true
			return &knowledge.KnowledgeObject{ID: id, Summary: "loaded: " + id, Confidence: 1.0}, nil
		})

	if lg.NodeCount() != 2 {
		t.Errorf("expected 2 nodes, got %d", lg.NodeCount())
	}

	// Node should not be expanded yet.
	node := lg.GetNode("a")
	if node.IsExpanded() {
		t.Error("expected node to be unexpanded initially")
	}

	// Expand on demand.
	obj, err := lg.ExpandNode(context.Background(), "a")
	if err != nil {
		t.Fatalf("ExpandNode error: %v", err)
	}
	if obj == nil {
		t.Fatal("expected loaded object")
	}
	if !expandCalled {
		t.Error("expected expandFn to be called")
	}
	if obj.Summary != "loaded: a" {
		t.Errorf("expected 'loaded: a', got '%s'", obj.Summary)
	}

	// Second expansion should be no-op.
	_, err = lg.ExpandNode(context.Background(), "a")
	if err != nil {
		t.Fatalf("second ExpandNode error: %v", err)
	}
}

func TestLazyGraphExpandNotFound(t *testing.T) {
	lg := NewLazyGraph(&knowledge.WorkingGraph{Nodes: map[string]*knowledge.KnowledgeObject{}}, nil)
	_, err := lg.ExpandNode(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent node")
	}
}

func TestLazyGraphCounts(t *testing.T) {
	lg := NewLazyGraphFromSummaries(map[string]string{"a": "sa", "b": "sb", "c": "sc"}, nil, nil)
	if lg.NodeCount() != 3 {
		t.Errorf("expected 3 nodes, got %d", lg.NodeCount())
	}
	if lg.LoadedCount() != 0 {
		t.Errorf("expected 0 loaded nodes initially, got %d", lg.LoadedCount())
	}
}

func TestLazyGraphNilSafe(t *testing.T) {
	var lg *LazyGraph
	if lg.NodeCount() != 0 {
		t.Error("expected 0 from nil graph")
	}
	if lg.GetNode("x") != nil {
		t.Error("expected nil from nil graph")
	}
}

func TestKnowledgeRuntimeEmptyGoal(t *testing.T) {
	reg := provider.NewProviderRegistry()
	_ = reg.Register(&testGraphProvider{name: "test"})
	sd := planner.NewSourceDiscovery(reg, &testQueryPlanner{})
	p := planner.NewKnowledgePlanner()

	rt := New(p, sd, reg, nil, nil, nil)
	_, err := rt.Execute(context.Background(), "", knowledge.TokenBudget{}, nil)
	if err == nil {
		t.Error("expected error for empty goal")
	}
}

func TestKnowledgeRuntimeFullPipeline(t *testing.T) {
	reg := provider.NewProviderRegistry()
	_ = reg.Register(&testGraphProvider{
		name: "memory",
		objects: []*knowledge.KnowledgeObject{
			{ID: "d1", Type: knowledge.ObjectDecision, Summary: "Chose Redis", Confidence: 0.9, Tags: []string{"redis"}},
			{ID: "d2", Type: knowledge.ObjectDecision, Summary: "Chose Postgres", Confidence: 0.8, Tags: []string{"postgres"}},
		},
	})

	sd := planner.NewSourceDiscovery(reg, &testQueryPlanner{})
	p := planner.NewKnowledgePlanner()

	rt := New(p, sd, reg, nil, []Linker{&DefaultLinker{}}, []Reducer{&DefaultReducer{}})
	graph, err := rt.Execute(context.Background(), "Why Redis?", knowledge.TokenBudget{MaxTokens: 2000, ForGraph: 1000}, nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if graph == nil {
		t.Fatal("expected non-nil graph")
	}
	if len(graph.Nodes) == 0 {
		t.Error("expected at least one node")
	}
}

// testQueryPlanner for runtime tests.
type testQueryPlanner struct{}

func (q *testQueryPlanner) PlanQuery(_ context.Context, req planner.KnowledgeRequirement, _, _ string) (*planner.QueryPlan, error) {
	return &planner.QueryPlan{Query: "test", QueryType: planner.QuerySQL, MaxResults: req.MaxResults}, nil
}

// testGraphProvider for runtime tests.
type testGraphProvider struct {
	name    string
	objects []*knowledge.KnowledgeObject
}

func (p *testGraphProvider) Name() string                           { return p.name }
func (p *testGraphProvider) IntentMatch(_ knowledge.Intent) float64 { return 0.9 }
func (p *testGraphProvider) Stream(_ context.Context, _ knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
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

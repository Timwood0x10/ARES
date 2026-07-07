package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

func TestMCPToolsRegistration(t *testing.T) {
	reg := provider.NewProviderRegistry()
	_ = reg.Register(testProvider("test"))
	sd := planner.NewSourceDiscovery(reg, &testQP{})
	p := planner.NewKnowledgePlanner()
	rt := runtime.New(p, sd, reg, nil, nil, nil)
	comp := &testCompiler{}

	svc := NewAKFService(rt, comp)
	tools := svc.Tools()

	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	if !toolNames["build_graph"] {
		t.Error("expected build_graph tool")
	}
	if !toolNames["compile_context"] {
		t.Error("expected compile_context tool")
	}
}

func TestBuildGraphMissingGoal(t *testing.T) {
	svc := NewAKFService(nil, nil)
	_, err := svc.handleBuildGraph(context.Background(), `{}`)
	if err == nil {
		t.Error("expected error for missing goal")
	}
}

func TestBuildGraphInvalidJSON(t *testing.T) {
	svc := NewAKFService(nil, nil)
	_, err := svc.handleBuildGraph(context.Background(), `not json`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestCompileContextMissingGoal(t *testing.T) {
	svc := NewAKFService(nil, nil)
	_, err := svc.handleCompileContext(context.Background(), `{}`)
	if err == nil {
		t.Error("expected error for missing goal")
	}
}

func TestCompileContextInvalidJSON(t *testing.T) {
	svc := NewAKFService(nil, nil)
	_, err := svc.handleCompileContext(context.Background(), `bad input`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestBuildGraphWithRealRuntime(t *testing.T) {
	reg, sd, p := setupTestRuntime()
	rt := runtime.New(p, sd, reg, nil, nil, nil)
	svc := NewAKFService(rt, &testCompiler{})

	input, _ := json.Marshal(map[string]any{
		"goal":       "Why Redis?",
		"max_tokens": 2000,
		"for_graph":  1000,
	})

	result, err := svc.handleBuildGraph(context.Background(), string(input))
	if err != nil {
		t.Fatalf("BuildGraph error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	nodes, ok := parsed["nodes"].(float64)
	if !ok || nodes <= 0 {
		t.Errorf("expected positive nodes count, got %v", nodes)
	}
}

func TestCompileContextWithRealRuntime(t *testing.T) {
	reg, sd, p := setupTestRuntime()
	rt := runtime.New(p, sd, reg, nil, nil, nil)
	svc := NewAKFService(rt, &testCompiler{})

	input, _ := json.Marshal(map[string]any{
		"goal":       "Why Redis?",
		"formats":    []string{"prompt"},
		"max_tokens": 5000,
		"for_graph":  3000,
	})

	result, err := svc.handleCompileContext(context.Background(), string(input))
	if err != nil {
		t.Fatalf("CompileContext error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	// Check that the compiler was called (testCompiler returns a mock).
	tokens, ok := parsed["output_tokens"].(float64)
	if !ok || tokens <= 0 {
		t.Errorf("expected positive output tokens, got %v", tokens)
	}
}

// ── Test helpers ────────────────────────────────────

func testProvider(name string) *mockProvider {
	return &mockProvider{name: name, score: 0.9}
}

type mockProvider struct {
	name    string
	score   float64
	objects []*knowledge.KnowledgeObject
}

func (p *mockProvider) Name() string                           { return p.name }
func (p *mockProvider) IntentMatch(_ knowledge.Intent) float64 { return p.score }
func (p *mockProvider) Stream(_ context.Context, _ knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
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

type testQP struct{}

func (q *testQP) PlanQuery(_ context.Context, _ planner.KnowledgeRequirement, _, _ string) (*planner.QueryPlan, error) {
	return &planner.QueryPlan{Query: "test", QueryType: planner.QuerySQL, MaxResults: 10}, nil
}

type testCompiler struct{}

func (c *testCompiler) Compile(_ context.Context, graph *knowledge.WorkingGraph, cfg compiler.CompileConfig) (*compiler.CompiledContext, error) {
	formats := make(map[compiler.Format]string)
	for _, f := range cfg.Formats {
		switch f {
		case compiler.FormatPrompt:
			formats[f] = "mock prompt output"
		case compiler.FormatJSON:
			formats[f] = `{"mock": "json"}`
		case compiler.FormatMarkdown:
			formats[f] = "# mock markdown"
		}
	}
	return &compiler.CompiledContext{
		Formats: formats,
		Metrics: compiler.CompileMetrics{
			InputNodes:   len(graph.Nodes),
			InputEdges:   len(graph.Edges),
			OutputTokens: 100,
		},
	}, nil
}

func setupTestRuntime() (*provider.ProviderRegistry, planner.SourceDiscovery, planner.KnowledgePlanner) {
	reg := provider.NewProviderRegistry()
	_ = reg.Register(&mockProvider{
		name:  "memory",
		score: 0.9,
		objects: []*knowledge.KnowledgeObject{
			{ID: "d1", Type: knowledge.ObjectDecision, Summary: "Test decision", Confidence: 0.9},
		},
	})
	sd := planner.NewSourceDiscovery(reg, &testQP{})
	p := planner.NewKnowledgePlanner()
	return reg, sd, p
}

func TestToolNames(t *testing.T) {
	tools := (&AKFService{}).Tools()
	for _, tool := range tools {
		if tool.Name == "" {
			t.Error("tool name should not be empty")
		}
		if tool.Description == "" {
			t.Error("tool description should not be empty")
		}
		if tool.Execute == nil {
			t.Error("tool execute should not be nil")
		}
	}
}

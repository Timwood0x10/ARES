package compiler

import (
	"context"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

func TestDefaultCompilerEmptyGraph(t *testing.T) {
	c := NewDefaultCompiler()
	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{},
		Edges: nil,
	}

	cfg := CompileConfig{Formats: []Format{FormatPrompt}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if ctx == nil {
		t.Fatal("expected non-nil CompileContext")
	}
}

func TestDefaultCompilerNilGraph(t *testing.T) {
	c := NewDefaultCompiler()
	_, err := c.Compile(context.Background(), nil, CompileConfig{})
	if err == nil {
		t.Error("expected error for nil graph")
	}
}

func TestDefaultCompilerPromptFormat(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatPrompt}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	prompt, ok := ctx.Formats[FormatPrompt]
	if !ok {
		t.Fatal("expected prompt format")
	}
	if !strings.Contains(prompt, "Knowledge Context") {
		t.Error("expected 'Knowledge Context' in prompt output")
	}
	if !strings.Contains(prompt, "redis") {
		t.Error("expected node 'redis' in prompt output")
	}
	if !strings.Contains(prompt, "Relations") {
		t.Error("expected Relations section in prompt output")
	}
}

func TestDefaultCompilerMarkdownFormat(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatMarkdown}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	md, ok := ctx.Formats[FormatMarkdown]
	if !ok {
		t.Fatal("expected markdown format")
	}
	if !strings.Contains(md, "redis") {
		t.Error("expected node data in markdown output")
	}
}

func TestDefaultCompilerJSONFormat(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatJSON}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	jsonOut, ok := ctx.Formats[FormatJSON]
	if !ok {
		t.Fatal("expected json format")
	}
	if !strings.Contains(jsonOut, `"nodes"`) {
		t.Error("expected 'nodes' in JSON output")
	}
	if !strings.Contains(jsonOut, `"edges"`) {
		t.Error("expected 'edges' in JSON output")
	}
}

func TestDefaultCompilerMultipleFormats(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatPrompt, FormatJSON}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	if len(ctx.Formats) != 2 {
		t.Errorf("expected 2 formats, got %d", len(ctx.Formats))
	}
	if _, ok := ctx.Formats[FormatPrompt]; !ok {
		t.Error("expected prompt format")
	}
	if _, ok := ctx.Formats[FormatJSON]; !ok {
		t.Error("expected json format")
	}
}

func TestDefaultCompilerMaxNodes(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatPrompt}, MaxNodes: 1}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	prompt := ctx.Formats[FormatPrompt]
	// Should only contain one of the three nodes.
	count := strings.Count(prompt, "conf:")
	if count > 2 {
		t.Errorf("expected at most 1 node in output, found ~%d", count)
	}
}

func TestDefaultCompilerMetrics(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatPrompt, FormatJSON}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	if ctx.Metrics.InputNodes != 3 {
		t.Errorf("expected 3 input nodes, got %d", ctx.Metrics.InputNodes)
	}
	if ctx.Metrics.InputEdges != 1 {
		t.Errorf("expected 1 input edge, got %d", ctx.Metrics.InputEdges)
	}
	if ctx.Metrics.OutputTokens <= 0 {
		t.Errorf("expected positive output tokens, got %d", ctx.Metrics.OutputTokens)
	}
}

func TestDefaultCompilerUnknownFormat(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{"unknown"}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if len(ctx.Formats) != 0 {
		t.Errorf("expected 0 formats for unknown format, got %d", len(ctx.Formats))
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		min   int
		max   int
	}{
		{"hello world", 2, 3},
		{"", 0, 0},
		{strings.Repeat("a", 100), 24, 26},
	}

	for _, tt := range tests {
		got := estimateTokens(tt.input)
		if got < tt.min || got > tt.max {
			t.Errorf("estimateTokens(%q) = %d, want between %d and %d", tt.input, got, tt.min, tt.max)
		}
	}
}

// testGraph creates a simple graph for testing.
func testGraph() *knowledge.WorkingGraph {
	return &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"redis": {ID: "redis", Type: knowledge.ObjectDecision, Summary: "Chose Redis for caching", Confidence: 0.9},
			"pg":    {ID: "pg", Type: knowledge.ObjectDecision, Summary: "Chose PostgreSQL for storage", Confidence: 0.8},
			"cache": {ID: "cache", Type: knowledge.ObjectArchitecture, Summary: "Cache layer architecture", Confidence: 0.7},
		},
		Edges: []knowledge.Relation{
			{From: "cache", To: "redis", Name: knowledge.RelDependsOn, Score: 0.9},
		},
	}
}

package compiler

import (
	"context"
	"fmt"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// makeGraph builds a WorkingGraph with n nodes and roughly 2n edges.
// Edges alternate between decided_by and depends_on so the compiler formats
// a realistic mix of relationships.
func makeGraph(n int) *knowledge.WorkingGraph {
	nodes := make(map[string]*knowledge.KnowledgeObject, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("node-%d", i)
		nodes[id] = &knowledge.KnowledgeObject{
			ID:        id,
			Type:      knowledge.ObjectDecision,
			Summary:   fmt.Sprintf("Decision node %d about cache strategy", i),
			Tags:      []string{"cache", "redis"},
			Confidence: 0.9,
		}
	}

	edges := make([]knowledge.Relation, 0, 2*n)
	relNames := []string{knowledge.RelDecidedBy, knowledge.RelDependsOn}
	for i := 0; i < n; i++ {
		from := fmt.Sprintf("node-%d", i)
		to := fmt.Sprintf("node-%d", (i+1)%n)
		edges = append(edges, knowledge.Relation{
			From: from,
			To:   to,
			Name: relNames[i%len(relNames)],
			Score: 0.85,
		})
	}

	return &knowledge.WorkingGraph{
		Nodes: nodes,
		Edges: edges,
	}
}

func BenchmarkDefaultCompiler_PromptFormat(b *testing.B) {
	c := NewDefaultCompiler()
	ctx := context.Background()
	cfg := CompileConfig{Formats: []Format{FormatPrompt}}

	for _, n := range []int{10, 50, 100, 500} {
		graph := makeGraph(n)
		b.Run(fmt.Sprintf("nodes_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := c.Compile(ctx, graph, cfg)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDefaultCompiler_MarkdownFormat(b *testing.B) {
	c := NewDefaultCompiler()
	ctx := context.Background()
	cfg := CompileConfig{Formats: []Format{FormatMarkdown}}

	for _, n := range []int{10, 50, 100, 500} {
		graph := makeGraph(n)
		b.Run(fmt.Sprintf("nodes_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := c.Compile(ctx, graph, cfg)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDefaultCompiler_JSONFormat(b *testing.B) {
	c := NewDefaultCompiler()
	ctx := context.Background()
	cfg := CompileConfig{Formats: []Format{FormatJSON}}

	for _, n := range []int{10, 50, 100, 500} {
		graph := makeGraph(n)
		b.Run(fmt.Sprintf("nodes_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := c.Compile(ctx, graph, cfg)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDefaultCompiler_AllFormats(b *testing.B) {
	c := NewDefaultCompiler()
	ctx := context.Background()
	cfg := CompileConfig{Formats: []Format{FormatPrompt, FormatMarkdown, FormatJSON, FormatXML, FormatToolSchema}}

	for _, n := range []int{10, 50, 100, 500} {
		graph := makeGraph(n)
		b.Run(fmt.Sprintf("nodes_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := c.Compile(ctx, graph, cfg)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

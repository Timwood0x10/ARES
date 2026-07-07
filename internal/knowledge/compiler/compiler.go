// Package compiler compiles a WorkingGraph into multiple output formats
// (Prompt, Markdown, JSON, XML, ToolSchema) for LLM consumption.
package compiler

import (
	"context"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// Format specifies the output format for compilation.
type Format string

const (
	FormatPrompt   Format = "prompt"
	FormatMarkdown Format = "markdown"
	FormatJSON     Format = "json"
)

// CompileConfig controls the compilation process.
type CompileConfig struct {
	Formats    []Format `json:"formats"`     // Output formats to generate
	MaxTokens  int      `json:"max_tokens"`  // Max token budget for output
	IncludeRaw bool     `json:"include_raw"` // Include Raw content
	MaxNodes   int      `json:"max_nodes"`   // Max nodes to include (0 = all)
	MaxEdges   int      `json:"max_edges"`   // Max edges to include (0 = all)
}

// CompiledContext is the result of compilation, containing the graph data
// formatted as one or more output formats.
type CompiledContext struct {
	Intent  string            `json:"intent"`
	Formats map[Format]string `json:"formats"`
	Metrics CompileMetrics    `json:"metrics"`
}

// CompileMetrics tracks compilation statistics.
type CompileMetrics struct {
	InputNodes       int     `json:"input_nodes"`
	InputEdges       int     `json:"input_edges"`
	OutputTokens     int     `json:"output_tokens"`
	CompressionRatio float64 `json:"compression_ratio"`
}

// Compiler compiles a WorkingGraph into LLM-ready contexts.
type Compiler interface {
	// Compile converts a WorkingGraph into the requested formats.
	Compile(ctx context.Context, graph *knowledge.WorkingGraph, cfg CompileConfig) (*CompiledContext, error)
}

// DefaultCompiler is the default implementation of Compiler.
type DefaultCompiler struct{}

// NewDefaultCompiler creates a new DefaultCompiler.
func NewDefaultCompiler() *DefaultCompiler {
	return &DefaultCompiler{}
}

// Compile converts the graph into the requested output formats.
func (c *DefaultCompiler) Compile(_ context.Context, graph *knowledge.WorkingGraph, cfg CompileConfig) (*CompiledContext, error) {
	if graph == nil {
		return nil, fmt.Errorf("graph cannot be nil")
	}

	formats := make(map[Format]string)
	metrics := CompileMetrics{
		InputNodes: len(graph.Nodes),
		InputEdges: len(graph.Edges),
	}

	for _, f := range cfg.Formats {
		var content string
		var err error
		switch f {
		case FormatPrompt:
			content, err = c.formatPrompt(graph, cfg)
		case FormatMarkdown:
			content, err = c.formatMarkdown(graph, cfg)
		case FormatJSON:
			content, err = c.formatJSON(graph, cfg)
		default:
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("format %s: %w", f, err)
		}
		formats[f] = content
		metrics.OutputTokens += estimateTokens(content)
	}

	if metrics.InputNodes > 0 {
		metrics.CompressionRatio = float64(metrics.OutputTokens) / float64(metrics.InputNodes*50)
	}

	return &CompiledContext{
		Formats: formats,
		Metrics: metrics,
	}, nil
}

// formatPrompt generates a structured prompt for LLM chat completion.
func (c *DefaultCompiler) formatPrompt(graph *knowledge.WorkingGraph, cfg CompileConfig) (string, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "## Knowledge Context\n\n")

	// Nodes section.
	nodeCount := len(graph.Nodes)
	if cfg.MaxNodes > 0 && cfg.MaxNodes < nodeCount {
		nodeCount = cfg.MaxNodes
	}

	fmt.Fprintf(&b, "### Nodes (%d)\n\n", nodeCount)
	count := 0
	for _, obj := range graph.Nodes {
		if cfg.MaxNodes > 0 && count >= cfg.MaxNodes {
			break
		}
		summary := obj.Summary
		if summary == "" {
			summary = obj.ID
		}
		fmt.Fprintf(&b, "- **%s**", obj.ID)
		if obj.Type != "" {
			fmt.Fprintf(&b, " [%s]", obj.Type)
		}
		fmt.Fprintf(&b, ": %s", summary)
		if obj.Confidence > 0 {
			fmt.Fprintf(&b, " (conf: %.2f)", obj.Confidence)
		}
		b.WriteString("\n")
		count++
	}

	// Edges section.
	if len(graph.Edges) > 0 {
		edgeCount := len(graph.Edges)
		if cfg.MaxEdges > 0 && cfg.MaxEdges < edgeCount {
			edgeCount = cfg.MaxEdges
		}
		fmt.Fprintf(&b, "\n### Relations (%d)\n\n", edgeCount)
		for i, e := range graph.Edges {
			if cfg.MaxEdges > 0 && i >= cfg.MaxEdges {
				break
			}
			fmt.Fprintf(&b, "- %s --[%s]--> %s", e.From, e.Name, e.To)
			if e.Score > 0 {
				fmt.Fprintf(&b, " (score: %.2f)", e.Score)
			}
			b.WriteString("\n")
		}
	}

	return b.String(), nil
}

// formatMarkdown generates a human-readable markdown document.
func (c *DefaultCompiler) formatMarkdown(graph *knowledge.WorkingGraph, cfg CompileConfig) (string, error) {
	return c.formatPrompt(graph, cfg) // Same content for now, just semantic difference
}

// formatJSON generates a JSON-serializable representation.
func (c *DefaultCompiler) formatJSON(graph *knowledge.WorkingGraph, cfg CompileConfig) (string, error) {
	var b strings.Builder
	b.WriteString("{\n  \"nodes\": [\n")

	first := true
	count := 0
	for _, obj := range graph.Nodes {
		if cfg.MaxNodes > 0 && count >= cfg.MaxNodes {
			break
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&b, "    {\"id\":%q,\"type\":%q,\"summary\":%q,\"confidence\":%.2f}",
			obj.ID, obj.Type, obj.Summary, obj.Confidence)
		count++
	}

	b.WriteString("\n  ],\n  \"edges\": [\n")
	first = true
	for i, e := range graph.Edges {
		if cfg.MaxEdges > 0 && i >= cfg.MaxEdges {
			break
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&b, "    {\"from\":%q,\"to\":%q,\"name\":%q,\"score\":%.2f}",
			e.From, e.To, e.Name, e.Score)
	}

	b.WriteString("\n  ]\n}\n")
	return b.String(), nil
}

// estimateTokens provides a rough estimate of token count.
func estimateTokens(s string) int {
	// Rough estimate: ~4 chars per token for English text.
	return len(s) / 4
}

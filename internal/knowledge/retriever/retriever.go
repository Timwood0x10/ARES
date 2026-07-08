// Package retriever implements AKG.md §8 — Intent-driven knowledge retrieval.
//
// Unlike traditional TopK vector search, the Retriever uses the full AKF
// pipeline (Plan → Load → Pipeline → Link → Reduce → Compile) to produce
// LLM-ready context from a natural language query. Embedding is used only
// as a fallback signal, not the primary retrieval mechanism.
//
// Flow:
//
//	Query + Intent
//	    │
//	    ▼
//	KnowledgePlanner (generates requirements from intent)
//	    │
//	    ▼
//	SourceDiscovery (maps requirements to providers)
//	    │
//	    ▼
//	KnowledgeRuntime (Load → Pipeline → Link → Reduce)
//	    │
//	    ▼
//	WorkingGraph
//	    │
//	    ▼
//	Compiler (Prompt / Markdown / JSON / XML / ToolSchema)
//	    │
//	    ▼
//	CompiledContext
package retriever

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// Query is a retrieval request.
type Query struct {
	// Text is the natural language query (e.g. "Why did we choose Redis?").
	Text string `json:"text"`

	// Types restricts retrieval to specific object types (empty = all types).
	Types []knowledge.ObjectType `json:"types,omitempty"`

	// MaxResults caps the number of nodes in the result graph (0 = default 50).
	MaxResults int `json:"max_results,omitempty"`

	// MaxTokens caps the total LLM context tokens (0 = default 4000).
	MaxTokens int `json:"max_tokens,omitempty"`

	// TokenBudgetForGraph is the portion of MaxTokens allocated to graph
	// context (0 = default 60% of MaxTokens).
	TokenBudgetForGraph int `json:"token_budget_for_graph,omitempty"`

	// Formats specifies which compiler output formats to generate.
	// Defaults to [Prompt] when empty.
	Formats []compiler.Format `json:"formats,omitempty"`
}

// Result is the output of a retrieval operation.
type Result struct {
	// Context contains the compiled output in each requested format.
	Context *compiler.CompiledContext `json:"context"`

	// Graph is the full WorkingGraph (useful for inspection or re-compilation).
	Graph *knowledge.WorkingGraph `json:"graph,omitempty"`

	// Query is the original query for traceability.
	Query string `json:"query"`
}

// Retriever implements AKG.md §8: Intent → Graph → Expand → Prune → Compile.
// It wraps the KnowledgeRuntime and Compiler into a single query interface.
type Retriever struct {
	runtime  *runtime.KnowledgeRuntime
	compiler compiler.Compiler
}

// New creates a Retriever backed by the given KnowledgeRuntime and Compiler.
func New(rt *runtime.KnowledgeRuntime, comp compiler.Compiler) *Retriever {
	return &Retriever{
		runtime:  rt,
		compiler: comp,
	}
}

// Retrieve executes the full AKF retrieval pipeline for a query.
//
// Steps:
//  1. Build an Intent from the query and scope constraints.
//  2. Run KnowledgeRuntime.Execute (Plan → Load → Pipeline → Link → Reduce).
//  3. Compile the resulting WorkingGraph into the requested output formats.
//  4. Return the compiled context + graph.
func (r *Retriever) Retrieve(ctx context.Context, query Query) (*Result, error) {
	if query.Text == "" {
		return nil, fmt.Errorf("retriever: query text is required")
	}

	if r.runtime == nil {
		return nil, fmt.Errorf("retriever: runtime is nil")
	}
	if r.compiler == nil {
		return nil, fmt.Errorf("retriever: compiler is nil")
	}

	// Build budget from query parameters.
	maxResults := query.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}
	maxTokens := query.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4000
	}
	forGraph := query.TokenBudgetForGraph
	if forGraph <= 0 {
		forGraph = maxTokens * 60 / 100 // 60% for graph
	}
	budget := knowledge.TokenBudget{
		MaxTokens: maxTokens,
		ForGraph:  forGraph,
		Reserved:  maxTokens - forGraph,
	}

	// Build intent from query.
	intent := knowledge.Intent{
		Goal: query.Text,
		Scope: knowledge.Scope{
			MaxObjects: maxResults,
			Types:      query.Types,
		},
		Budget: budget,
	}
	_ = intent // used by runtime.Execute internally

	// Run KnowledgeRuntime: Plan → Load → Pipeline → Link → Reduce.
	graph, err := r.runtime.Execute(ctx, query.Text, budget, nil)
	if err != nil {
		return nil, fmt.Errorf("retriever: execute: %w", err)
	}

	// Compile the graph into the requested formats.
	formats := query.Formats
	if len(formats) == 0 {
		formats = []compiler.Format{compiler.FormatPrompt}
	}
	cfg := compiler.CompileConfig{
		Formats:   formats,
		MaxTokens: maxTokens,
		MaxNodes:  maxResults,
	}
	compiled, err := r.compiler.Compile(ctx, graph, cfg)
	if err != nil {
		return nil, fmt.Errorf("retriever: compile: %w", err)
	}

	return &Result{
		Context: compiled,
		Graph:   graph,
		Query:   query.Text,
	}, nil
}

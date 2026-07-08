// Package mcp provides MCP tool definitions for ARES Knowledge Fabric (AKF).
// These tools expose BuildGraph, CompileContext, and QueryKnowledge as
// standard MCP tools that can be registered with any MCP server.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// Tools returns the AKF MCP tool definitions that can be registered
// with the MCP server. Each tool is a function with a string input
// (JSON-encoded parameters) and returns a JSON-formatted result.
type Tool struct {
	Name        string
	Description string
	Execute     func(ctx context.Context, input string) (string, error)
}

// AKFService wraps the KnowledgeRuntime and Compiler for MCP access.
type AKFService struct {
	Runtime  *runtime.KnowledgeRuntime
	Compiler compiler.Compiler
}

// NewAKFService creates an AKFService with the given runtime and compiler.
func NewAKFService(rt *runtime.KnowledgeRuntime, comp compiler.Compiler) *AKFService {
	return &AKFService{
		Runtime:  rt,
		Compiler: comp,
	}
}

// buildGraphParams is the JSON input for the BuildGraph tool.
type buildGraphParams struct {
	Goal      string `json:"goal"`
	MaxTokens int    `json:"max_tokens"`
	ForGraph  int    `json:"for_graph"`
}

// compileContextParams is the JSON input for the CompileContext tool.
type compileContextParams struct {
	Goal      string   `json:"goal"`
	Formats   []string `json:"formats"`
	MaxTokens int      `json:"max_tokens"`
	ForGraph  int      `json:"for_graph"`
}

// queryKnowledgeParams is the JSON input for the QueryKnowledge tool.
type queryKnowledgeParams struct {
	Text      string   `json:"text"`
	Types     []string `json:"types,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Limit     int      `json:"limit"`
	MaxTokens int      `json:"max_tokens,omitempty"`
}

// Tools returns all AKF MCP tools.
func (s *AKFService) Tools() []Tool {
	return []Tool{
		{
			Name:        "build_graph",
			Description: "Build a knowledge graph for a given goal. Returns nodes and edges.",
			Execute:     s.handleBuildGraph,
		},
		{
			Name:        "compile_context",
			Description: "Build and compile knowledge context into Prompt/JSON for LLM consumption.",
			Execute:     s.handleCompileContext,
		},
		{
			Name:        "query_knowledge",
			Description: "Query knowledge objects by type, tag, or text search through all providers.",
			Execute:     s.handleQueryKnowledge,
		},
	}
}

// handleBuildGraph executes the AKF pipeline and returns the raw graph.
func (s *AKFService) handleBuildGraph(ctx context.Context, input string) (string, error) {
	var params buildGraphParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	if params.Goal == "" {
		return "", fmt.Errorf("goal is required")
	}
	if params.MaxTokens <= 0 {
		params.MaxTokens = 2000
	}
	if params.ForGraph <= 0 {
		params.ForGraph = 1000
	}

	budget := knowledge.TokenBudget{
		MaxTokens: params.MaxTokens,
		ForGraph:  params.ForGraph,
		Reserved:  params.MaxTokens - params.ForGraph,
	}

	graph, err := s.Runtime.Execute(ctx, params.Goal, budget, nil)
	if err != nil {
		return "", fmt.Errorf("build graph: %w", err)
	}

	result := map[string]any{
		"nodes":      len(graph.Nodes),
		"edges":      len(graph.Edges),
		"node_ids":   nodeIDs(graph),
		"edge_count": len(graph.Edges),
	}
	data, _ := json.Marshal(result)
	return string(data), nil
}

// handleCompileContext builds the graph and compiles it into requested formats.
func (s *AKFService) handleCompileContext(ctx context.Context, input string) (string, error) {
	var params compileContextParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	if params.Goal == "" {
		return "", fmt.Errorf("goal is required")
	}
	if params.MaxTokens <= 0 {
		params.MaxTokens = 5000
	}
	if params.ForGraph <= 0 {
		params.ForGraph = 3000
	}

	budget := knowledge.TokenBudget{
		MaxTokens: params.MaxTokens,
		ForGraph:  params.ForGraph,
		Reserved:  params.MaxTokens - params.ForGraph,
	}

	graph, err := s.Runtime.Execute(ctx, params.Goal, budget, nil)
	if err != nil {
		return "", fmt.Errorf("build graph: %w", err)
	}

	var formats []compiler.Format
	for _, f := range params.Formats {
		formats = append(formats, compiler.Format(f))
	}
	if len(formats) == 0 {
		formats = []compiler.Format{compiler.FormatPrompt}
	}

	cfg := compiler.CompileConfig{Formats: formats}
	compiled, err := s.Compiler.Compile(ctx, graph, cfg)
	if err != nil {
		return "", fmt.Errorf("compile: %w", err)
	}

	result := map[string]any{
		"formats":       compiled.Formats,
		"input_nodes":   compiled.Metrics.InputNodes,
		"input_edges":   compiled.Metrics.InputEdges,
		"output_tokens": compiled.Metrics.OutputTokens,
	}
	data, _ := json.Marshal(result)
	return string(data), nil
}

// nodeIDs extracts node IDs from a graph.
func nodeIDs(g *knowledge.WorkingGraph) []string {
	ids := make([]string, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	return ids
}

// handleQueryKnowledge performs a text/type/tag query via the runtime.
func (s *AKFService) handleQueryKnowledge(ctx context.Context, input string) (string, error) {
	var params queryKnowledgeParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}

	budget := knowledge.TokenBudget{
		MaxTokens: params.MaxTokens,
		ForGraph:  params.Limit * 100,
	}
	if params.MaxTokens <= 0 {
		budget.MaxTokens = 5000
		budget.ForGraph = 3000
	}
	budget.Reserved = budget.MaxTokens - budget.ForGraph

	goal := params.Text
	if goal == "" {
		goal = "query"
	}

	graph, err := s.Runtime.Execute(ctx, goal, budget, nil)
	if err != nil {
		return "", fmt.Errorf("query: %w", err)
	}

	result := map[string]any{
		"nodes":      len(graph.Nodes),
		"edges":      len(graph.Edges),
		"node_ids":   nodeIDs(graph),
		"edge_count": len(graph.Edges),
	}
	data, _ := json.Marshal(result)
	return string(data), nil
}

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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
	// store is the optional persistence layer for distilled KnowledgeObjects.
	// When nil, distill_memory computes the quality score but skips Save/
	// Promote (backward compatibility with pre-0.2.9 callers).
	store knowledge.KnowledgeStore
	// gate is the quality gate used to score distilled objects. Always set
	// via the constructors; a zero-value gate would still function but
	// DefaultQualityGateConfig carries the 0.2.9 thresholds.
	gate knowledge.QualityGateConfig
	// extractor extracts rule-based Relations from each distilled object's
	// text before the quality gate runs, so the ExtractionScore boost
	// applies identically to the DistillBridge path. Always non-nil after
	// construction.
	extractor *knowledge.RelationExtractor
}

// NewAKFService creates an AKFService with the given runtime and compiler.
// No store is wired, so distill_memory scores objects but does not persist
// them. The default quality gate (DefaultQualityGateConfig) and the default
// relation extractor are applied.
func NewAKFService(rt *runtime.KnowledgeRuntime, comp compiler.Compiler) *AKFService {
	return &AKFService{
		Runtime:   rt,
		Compiler:  comp,
		store:     nil,
		gate:      knowledge.DefaultQualityGateConfig(),
		extractor: knowledge.NewRelationExtractor(),
	}
}

// NewAKFServiceWithStore creates an AKFService backed by a KnowledgeStore so
// distill_memory persists candidate objects and promotes them to active when
// their final confidence clears the gate's MinFinalScore threshold.
//
// Args:
//   - rt:    shared KnowledgeRuntime (may be nil for tool-only tests).
//   - comp:  compiler used by compile_context.
//   - store: persistence layer; pass nil to skip persistence.
//   - gate:  quality gate thresholds; pass DefaultQualityGateConfig() when
//     unsure.
func NewAKFServiceWithStore(
	rt *runtime.KnowledgeRuntime,
	comp compiler.Compiler,
	store knowledge.KnowledgeStore,
	gate knowledge.QualityGateConfig,
) *AKFService {
	return &AKFService{
		Runtime:   rt,
		Compiler:  comp,
		store:     store,
		gate:      gate,
		extractor: knowledge.NewRelationExtractor(),
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

// distillMemoryParams is the JSON input for the DistillMemory tool.
type distillMemoryParams struct {
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
	Type    string   `json:"type,omitempty"`
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
		{
			Name:        "distill_memory",
			Description: "Convert a text memory into KnowledgeObjects via the pipeline.",
			Execute:     s.handleDistillMemory,
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

// handleDistillMemory converts a text memory into a KnowledgeObject, runs it
// through the quality gate, and (when a store is wired) persists it as a
// candidate before promoting it to active when its confidence clears the
// gate threshold. The previous implementation constructed the object but
// never called store.Save and hardcoded Confidence to 1.0 — this fix closes
// that pseudo-write bug.
func (s *AKFService) handleDistillMemory(ctx context.Context, input string) (string, error) {
	var params distillMemoryParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	if params.Content == "" {
		return "", fmt.Errorf("content is required")
	}

	objType := knowledge.ObjectMemory
	if params.Type != "" {
		objType = knowledge.ObjectType(params.Type)
	}

	now := time.Now()
	obj := &knowledge.KnowledgeObject{
		ID:         fmt.Sprintf("mem_%d", now.UnixNano()),
		Type:       objType,
		Summary:    params.Content,
		Normalized: params.Content,
		Tags:       params.Tags,
		Status:     knowledge.StatusCandidate,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Relation extraction must run before the quality gate so the
	// ExtractionScore boost applies identically to the DistillBridge path
	// (phase 3.5). Without this the same content scores lower via MCP than
	// via the bridge.
	obj.Relations = s.extractor.Extract(obj)

	// Quality gate: evaluate multi-dimensional quality and fold it into a
	// single Confidence score. Confidence is derived from the gate rather
	// than hardcoded to 1.0 so downstream ranking reflects real signal.
	q := s.gate.Evaluate(obj)
	obj.Quality = q
	obj.Confidence = s.gate.ComputeFinal(q)

	// Persist when a store is wired. The object is saved as a candidate
	// first; if its final score clears the gate threshold it is then
	// promoted to active. Promotion is best-effort — a failure leaves the
	// object persisted as a candidate rather than failing the whole call.
	if s.store != nil {
		if err := s.store.Save(ctx, obj); err != nil {
			return "", fmt.Errorf("distill memory: save object %s: %w", obj.ID, err)
		}
		if obj.Confidence >= s.gate.MinFinalScore {
			if err := s.store.Promote(ctx, obj.ID, obj.Quality); err == nil {
				obj.Status = knowledge.StatusActive
			} else {
				// best-effort: promotion failure leaves the object persisted as
				// a candidate; log so the failure is observable rather than silent.
				slog.Warn("akf service: promote object",
					"object_id", obj.ID, "error", err)
			}
		}
	}

	result := map[string]any{
		"object_id":  obj.ID,
		"type":       obj.Type,
		"summary":    obj.Summary,
		"status":     obj.Status,
		"confidence": obj.Confidence,
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
// Rebuilds the graph from the runtime each call — when a KnowledgeStore is
// wired into AKFService, this can query the store directly for better performance.
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

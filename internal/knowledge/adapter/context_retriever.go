// Package adapter provides bridges between existing ARES subsystems and AKF.
//
// This file implements KnowledgeRetriever — an adapter that exposes the AKG
// (Adaptive Knowledge Graph) via the ContextRetriever interface so the
// chat-loop context builder can inject AKG knowledge into the LLM prompt.
package adapter

import (
	"context"
	"fmt"
	"sort"

	"github.com/Timwood0x10/ares/internal/knowledge"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// ContextSnippet matches context.ContextSnippet in internal/ares_memory/context.
// Kept as a local struct to avoid the import cycle knowledge → ares_memory
// (ares_memory already depends on knowledge via distillation). The main agent
// adapts this shape to the canonical ares_memory/context.ContextSnippet.
type ContextSnippet struct {
	Source   string
	Content  string
	Score    float64
	Metadata map[string]any
}

// ContextRetriever retrieves relevant knowledge snippets for a given input.
// Local interface mirroring ares_memory/context.ContextRetriever to keep the
// knowledge package free of the ares_memory import dependency.
type ContextRetriever interface {
	Retrieve(ctx context.Context, input string, topK int) ([]ContextSnippet, error)
}

// DefaultMinScore is the minimum Confidence score for a KnowledgeObject to be
// returned as a ContextSnippet when no explicit minScore is provided.
const DefaultMinScore = 0.4

// defaultTopK is the default maximum number of snippets returned by Retrieve
// when the caller passes topK <= 0.
const defaultTopK = 5

// defaultBudget is the token budget used when calling KnowledgeRuntime.Execute.
// It is sized for chat-loop consumption: small enough to fit a typical prompt
// window, large enough to surface a handful of relevant nodes.
var defaultBudget = knowledge.TokenBudget{
	MaxTokens: 4000,
	ForGraph:  2400, // 60% for graph nodes
	Reserved:  1600, // 40% reserved for LLM reasoning
}

// defaultMaxConcurrentProviders caps parallel provider loads during Execute.
const defaultMaxConcurrentProviders = 5

// KnowledgeRetriever adapts the AKG (KnowledgeRuntime) to the ContextRetriever
// interface. It runs the AKF pipeline (Plan → Load → Link → Reduce) for the
// input query and converts the resulting KnowledgeObjects into ContextSnippets
// ready for injection into the LLM prompt.
//
// The underlying KnowledgeRuntime is responsible for its own internal locking
// (see runtime.loadAndProcess); this adapter holds no mutable state and is
// safe for concurrent use across goroutines.
type KnowledgeRetriever struct {
	runtime  *knowledgeruntime.KnowledgeRuntime
	minScore float64
}

// NewKnowledgeRetriever creates a KnowledgeRetriever backed by the given
// KnowledgeRuntime.
//
// Args:
//   - ctx: context reserved for future initialization I/O (currently unused
//     but kept to satisfy §4.3 constructor conventions).
//   - runtime: AKG KnowledgeRuntime. Must be non-nil.
//   - minScore: minimum Confidence score for a snippet to be returned.
//     Pass 0 (or any value <= 0) to use DefaultMinScore (0.4).
//
// Returns:
//   - retriever: ready to serve Retrieve calls.
//   - err: wrapped error if runtime is nil.
func NewKnowledgeRetriever(
	_ context.Context,
	runtime *knowledgeruntime.KnowledgeRuntime,
	minScore float64,
) (*KnowledgeRetriever, error) {
	if runtime == nil {
		return nil, fmt.Errorf("knowledge retriever: runtime is nil")
	}
	if minScore <= 0 {
		minScore = DefaultMinScore
	}
	return &KnowledgeRetriever{
		runtime:  runtime,
		minScore: minScore,
	}, nil
}

// Retrieve queries the AKG for knowledge entries matching the input and
// returns them as ContextSnippets sorted by Score descending.
//
// Args:
//   - ctx: cancellation context. Honoured by KnowledgeRuntime.Execute.
//   - input: natural language query. Empty input returns an empty slice
//     with nil error.
//   - topK: maximum number of snippets to return. Defaults to 5 when <= 0.
//
// Returns:
//   - snippets: at most topK ContextSnippets with Score >= minScore, sorted
//     by Score descending. Empty (not nil) when no matches qualify.
//   - err: wrapped error if the AKG pipeline fails.
func (r *KnowledgeRetriever) Retrieve(
	ctx context.Context,
	input string,
	topK int,
) ([]ContextSnippet, error) {
	if r == nil {
		return nil, fmt.Errorf("knowledge retriever: receiver is nil")
	}
	if r.runtime == nil {
		return nil, fmt.Errorf("knowledge retriever: runtime is nil")
	}
	if input == "" {
		return []ContextSnippet{}, nil
	}
	if topK <= 0 {
		topK = defaultTopK
	}

	// Run the AKG pipeline: Plan → Load → Link → Reduce.
	// KnowledgeRuntime.Execute accepts natural-language text directly — no
	// embedder is required because providers stream their own object sets
	// based on the planner's Intent matching.
	cfg := &knowledgeruntime.Config{
		MaxConcurrentProviders: defaultMaxConcurrentProviders,
	}
	graph, err := r.runtime.Execute(ctx, input, defaultBudget, cfg)
	if err != nil {
		return nil, fmt.Errorf("knowledge retriever: execute: %w", err)
	}
	if graph == nil || len(graph.Nodes) == 0 {
		return []ContextSnippet{}, nil
	}

	snippets := r.collectSnippets(graph.Nodes)
	// Sort by Score descending (stable enough — ties keep insertion order).
	sort.SliceStable(snippets, func(i, j int) bool {
		return snippets[i].Score > snippets[j].Score
	})

	// Cap at topK.
	if len(snippets) > topK {
		snippets = snippets[:topK]
	}
	return snippets, nil
}

// collectSnippets converts the runtime's KnowledgeObject map into a slice of
// ContextSnippets, applying the minScore filter and skipping nil entries.
// Non-blocking: pure transformation, no I/O.
func (r *KnowledgeRetriever) collectSnippets(
	nodes map[string]*knowledge.KnowledgeObject,
) []ContextSnippet {
	snippets := make([]ContextSnippet, 0, len(nodes))
	for _, obj := range nodes {
		if obj == nil {
			continue
		}
		score := clampScore(obj.Confidence)
		if score < r.minScore {
			continue
		}
		snippets = append(snippets, ContextSnippet{
			Source:  "knowledge",
			Content: snippetContent(obj),
			Score:   score,
			Metadata: map[string]any{
				"id":        obj.ID,
				"type":      string(obj.Type),
				"namespace": obj.Namespace,
				"tags":      obj.Tags,
				"version":   obj.Version,
			},
		})
	}
	return snippets
}

// snippetContent returns the most informative text for a KnowledgeObject.
// Preference order: Summary (LLM-friendly) → Normalized (cleaned text) →
// Raw (original bytes) → fallback placeholder. Never returns empty for a
// non-nil object.
func snippetContent(obj *knowledge.KnowledgeObject) string {
	if obj.Summary != "" {
		return obj.Summary
	}
	if obj.Normalized != "" {
		return obj.Normalized
	}
	if len(obj.Raw) > 0 {
		return string(obj.Raw)
	}
	return fmt.Sprintf("knowledge object %s (no content)", obj.ID)
}

// clampScore clamps a Confidence score to the [0, 1] range so downstream
// callers can rely on a normalized Score even when providers return values
// slightly outside the expected band.
func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Ensure KnowledgeRetriever satisfies the local ContextRetriever interface.
var _ ContextRetriever = (*KnowledgeRetriever)(nil)

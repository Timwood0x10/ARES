package adapter

import (
	"context"
	"fmt"
	"sort"

	"github.com/Timwood0x10/ares/internal/knowledge"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	"github.com/Timwood0x10/ares/internal/scoreutil"
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

// Shared metadata keys and source identifiers used by both the store path and
// the runtime path (and their tests). Centralised so the literals are not
// repeated across the package (goconst).
const (
	sourceAKGStore   = "akg_store"
	metaKeyType      = "type"
	metaKeyNamespace = "namespace"
)

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
//
// When a KnowledgeStore is wired (store != nil) Retrieve takes the AKG read
// loop: HybridSearch against the store's AKG-distilled facts instead of
// re-running provider streaming. With store == nil it falls back to the
// original runtime.Execute path. The model field names the embedding model
// the store should compare against (empty = lexical-only search).
type KnowledgeRetriever struct {
	runtime  *knowledgeruntime.KnowledgeRuntime
	store    knowledge.KnowledgeStore // optional; nil = fall back to runtime.Execute
	model    string                   // embedding model name for HybridSearch
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

// NewKnowledgeRetrieverWithStore creates a KnowledgeRetriever that takes the
// AKG read loop: when store is non-nil, Retrieve calls store.HybridSearch to
// read AKG-distilled facts instead of re-running provider streaming via
// runtime.Execute. Pass store == nil to behave like NewKnowledgeRetriever
// (fall back to runtime.Execute).
//
// Args:
//   - ctx: context reserved for future initialization I/O (currently unused
//     but kept to satisfy §4.3 constructor conventions).
//   - runtime: AKG KnowledgeRuntime. Must be non-nil even when store is set,
//     because the fallback path needs it. The store path itself does not call
//     runtime.Execute.
//   - store: optional KnowledgeStore. nil = fall back to runtime.Execute.
//   - model: embedding model name passed to HybridSearch (selects which
//     Representation the store compares against). Empty = lexical-only.
//   - minScore: minimum Confidence score for a snippet to be returned.
//     Pass 0 (or any value <= 0) to use DefaultMinScore (0.4). For the store
//     path this is forwarded as HybridSearchRequest.MinScore; the store layer
//     applies the filter, so Retrieve does NOT filter again.
//
// Returns:
//   - retriever: ready to serve Retrieve calls.
//   - err: wrapped error if runtime is nil.
func NewKnowledgeRetrieverWithStore(
	_ context.Context,
	runtime *knowledgeruntime.KnowledgeRuntime,
	store knowledge.KnowledgeStore,
	model string,
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
		store:    store,
		model:    model,
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

	// AKG read loop: when a KnowledgeStore is wired, read AKG-distilled facts
	// via HybridSearch instead of re-running provider streaming. The store
	// layer applies MinScore and the TopK/FinalK caps, so we do not filter or
	// cap again here. Vector recall (TopK) is over-fetched 3x relative to the
	// caller's topK so the FinalK ranking has a richer candidate pool.
	if r.store != nil {
		req := knowledge.HybridSearchRequest{
			Query:        input,
			TopK:         topK * 3,
			FinalK:       topK,
			MinScore:     r.minScore,
			Model:        r.model,
			StatusFilter: []knowledge.ObjectStatus{knowledge.StatusActive},
		}
		scored, err := r.store.HybridSearch(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("knowledge retriever: hybrid search: %w", err)
		}
		snippets := make([]ContextSnippet, 0, len(scored))
		for _, s := range scored {
			if s.Object == nil {
				continue
			}
			snippets = append(snippets, ContextSnippet{
				Source:  sourceAKGStore,
				Content: snippetContent(s.Object),
				Score:   s.FinalScore,
				Metadata: map[string]any{
					"id":             s.Object.ID,
					metaKeyType:      string(s.Object.Type),
					"source":         sourceAKGStore,
					"vector":         s.VectorScore,
					"lexical":        s.LexicalScore,
					metaKeyNamespace: s.Object.Namespace,
				},
			})
		}
		return snippets, nil
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
		score := scoreutil.ClampUnit(obj.Confidence)
		if score < r.minScore {
			continue
		}
		snippets = append(snippets, ContextSnippet{
			Source:  "knowledge",
			Content: snippetContent(obj),
			Score:   score,
			Metadata: map[string]any{
				"id":             obj.ID,
				metaKeyType:      string(obj.Type),
				metaKeyNamespace: obj.Namespace,
				"tags":           obj.Tags,
				"version":        obj.Version,
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

// Ensure KnowledgeRetriever satisfies the local ContextRetriever interface.
var _ ContextRetriever = (*KnowledgeRetriever)(nil)

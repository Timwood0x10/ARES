package retriever

import (
	"context"
	"log/slog"
	"strings"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// RetrievalTier identifies the retrieval layer that produced a result (M6).
type RetrievalTier int

const (
	// TierL1 is the lexicon/keyword layer: exact term matches and lexical
	// overlap. Cheapest, fastest, highest precision for known terms.
	TierL1 RetrievalTier = 1
	// TierL2 is the vector+graph layer: semantic similarity plus graph-walk
	// expansion. Medium cost, good recall for paraphrased queries.
	TierL2 RetrievalTier = 2
	// TierL3 is the LLM rerank layer: cross-encoder or LLM-based reranking
	// of L1+L2 candidates. Highest cost, best precision for ambiguous
	// queries. Reserved as an extension point — 0.3.4 implements L1+L2 only.
	TierL3 RetrievalTier = 3
)

// String returns a human-readable tier name for logging.
func (t RetrievalTier) String() string {
	switch t {
	case TierL1:
		return "L1-lexicon"
	case TierL2:
		return "L2-vector-graph"
	case TierL3:
		return "L3-llm-rerank"
	default:
		return "unknown"
	}
}

// TierResult is the result of one retrieval tier.
type TierResult struct {
	// Tier is the layer that produced this result.
	Tier RetrievalTier `json:"tier"`
	// Objects is the recalled knowledge objects.
	Objects []*knowledge.KnowledgeObject `json:"objects"`
	// ElapsedMS is the time spent in this tier (for observability).
	ElapsedMS int64 `json:"elapsed_ms"`
}

// TODO(tech-debt): TieredRetriever has no production caller yet — it is only
// referenced by the (also unwired) EvalHarness, so the runtime/retriever path
// still goes through Retriever directly. Wire it in before treating M6's
// tiers as shipped. It also pulls the whole store into memory per query and is
// currently unscoped by namespace (see lexiconSearch) — do NOT wire it into a
// per-user recall path before that isolation gap is closed.
//
// TieredRetriever implements multi-level retrieval (M6).
//
// L1 (lexicon): exact keyword matches against Summary/Normalized.
// L2 (vector+graph): delegates to the existing Retriever (which uses
// HybridSearch via the runtime) for semantic recall.
// L3 (LLM rerank): reserved as an extension point; the 0.3.4 implementation
// returns L2 results unchanged.
type TieredRetriever struct {
	store knowledge.KnowledgeStore
	// L2Retriever is the existing Retriever for semantic+graph recall.
	// When nil, L2 is skipped and only L1 results are returned.
	L2Retriever *Retriever
}

// NewTieredRetriever creates a TieredRetriever backed by the given store.
// The L2 retriever is optional; pass nil to use L1 only.
func NewTieredRetriever(store knowledge.KnowledgeStore, l2 *Retriever) *TieredRetriever {
	return &TieredRetriever{
		store:       store,
		L2Retriever: l2,
	}
}

// Retrieve executes multi-level retrieval for a query (M6).
//
// L1 runs first: cheap keyword match. If L1 finds enough results (>= limit),
// L2 is skipped — the query was answerable by exact terms.
// L2 runs when L1 is insufficient: semantic recall + graph walk.
// L3 (LLM rerank) is reserved; 0.3.4 returns L2 results unchanged.
func (tr *TieredRetriever) Retrieve(ctx context.Context, query Query) (*Result, RetrievalTier, error) {
	limit := query.MaxResults
	if limit <= 0 {
		limit = 50
	}

	// L1: lexicon/keyword, scoped to the declared namespace. See
	// lexiconSearch for why an unscoped query is refused.
	l1Objects := tr.lexiconSearch(ctx, query.Namespace, query.Text, limit)

	// If L1 found enough, return without the cost of L2.
	if len(l1Objects) >= limit {
		graph := &knowledge.WorkingGraph{
			Nodes: objectsToMap(l1Objects),
		}
		return &Result{
			Graph: graph,
			Query: query.Text,
		}, TierL1, nil
	}

	// L2: vector + graph walk (via existing Retriever).
	if tr.L2Retriever != nil {
		result, err := tr.L2Retriever.Retrieve(ctx, query)
		if err != nil {
			// L2 failure: degrade to what L1 found rather than failing the
			// query — but log it, otherwise a persistent L2 outage shows up
			// only as an invisible recall regression.
			slog.Warn("tiered retriever: L2 failed, serving L1 only",
				"query", query.Text, "error", err)
			graph := &knowledge.WorkingGraph{
				Nodes: objectsToMap(l1Objects),
			}
			return &Result{Graph: graph, Query: query.Text}, TierL1, nil
		}
		// Merge L1 results into L2 (L1 hits are high-precision, keep them).
		merged := mergeL1IntoL2(l1Objects, result.Graph)
		result.Graph = merged
		return result, TierL2, nil
	}

	// No L2: return L1 results.
	graph := &knowledge.WorkingGraph{
		Nodes: objectsToMap(l1Objects),
	}
	return &Result{Graph: graph, Query: query.Text}, TierL1, nil
}

// lexiconSearch performs a keyword-based search against the store (L1).
// It splits the query into keywords and matches objects whose Summary or
// Normalized contains ALL the keywords (AND semantics). This is the cheapest
// retrieval path and the highest-precision for known terms.
func (tr *TieredRetriever) lexiconSearch(ctx context.Context, namespace, queryText string, limit int) []*knowledge.KnowledgeObject {
	if tr.store == nil || queryText == "" {
		return nil
	}
	if namespace == "" {
		// Refuse to search unscoped: the store drops its namespace predicate
		// for an empty namespace, so this would recall across tenants/users
		// (isolation leak). Failing closed keeps L1 inside the caller's
		// namespace; callers must declare it via Query.Namespace.
		slog.Warn("tiered retriever: L1 skipped, no namespace declared")
		return nil
	}
	keywords := tokenizeKeywords(queryText)
	if len(keywords) == 0 {
		return nil
	}

	// TODO(tech-debt): this pulls the whole namespace into memory and filters
	// in Go. A production implementation should use the store's lexical search
	// (HybridSearch) or a full-text index; the full scan is only a fallback.
	objects, err := tr.store.Query(ctx, knowledge.Query{Namespace: namespace, Limit: 0})
	if err != nil {
		return nil
	}

	var matched []*knowledge.KnowledgeObject
	for _, obj := range objects {
		if obj.Status != knowledge.StatusActive && obj.Status != "" {
			continue
		}
		text := strings.ToLower(obj.Summary + " " + obj.Normalized)
		if containsAllKeywords(text, keywords) {
			matched = append(matched, obj)
			if len(matched) >= limit {
				break
			}
		}
	}
	return matched
}

// tokenizeKeywords splits a query into lowercase keyword tokens.
func tokenizeKeywords(s string) []string {
	var tokens []string
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.TrimFunc(w, func(r rune) bool {
			return !isAlphaNum(r)
		})
		if w != "" && len(w) > 1 {
			tokens = append(tokens, w)
		}
	}
	return tokens
}

// isAlphaNum returns true for alphanumeric runes (ASCII only for simplicity).
func isAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// containsAllKeywords checks whether text contains all keywords (AND).
func containsAllKeywords(text string, keywords []string) bool {
	for _, kw := range keywords {
		if !strings.Contains(text, kw) {
			return false
		}
	}
	return true
}

// objectsToMap converts a slice of objects into a map keyed by ID.
func objectsToMap(objects []*knowledge.KnowledgeObject) map[string]*knowledge.KnowledgeObject {
	m := make(map[string]*knowledge.KnowledgeObject, len(objects))
	for _, obj := range objects {
		m[obj.ID] = obj
	}
	return m
}

// mergeL1IntoL2 ensures L1 hits appear in the L2 graph (deduplicated by ID).
func mergeL1IntoL2(l1Objects []*knowledge.KnowledgeObject, l2Graph *knowledge.WorkingGraph) *knowledge.WorkingGraph {
	if l2Graph == nil {
		return &knowledge.WorkingGraph{Nodes: objectsToMap(l1Objects)}
	}
	if l2Graph.Nodes == nil {
		l2Graph.Nodes = make(map[string]*knowledge.KnowledgeObject)
	}
	for _, obj := range l1Objects {
		if _, exists := l2Graph.Nodes[obj.ID]; !exists {
			l2Graph.Nodes[obj.ID] = obj
		}
	}
	return l2Graph
}

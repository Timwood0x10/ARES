package ares_bootstrap

import (
	"context"
	"sort"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	"github.com/Timwood0x10/ares/internal/knowledge/retriever"
	memctx "github.com/Timwood0x10/ares/internal/runtime/memory/context"
	"github.com/Timwood0x10/ares/internal/tenantctx"
	"github.com/Timwood0x10/ares/internal/userctx"
)

// tieredContextRetriever adapts the tiered knowledge retriever (M6) to the memory manager's
// ContextRetriever interface.
//
// Two shapes meet here and do NOT line up on their own:
//   - the tiered path returns a *retriever.Result — a knowledge GRAPH plus the tier that
//     served it;
//   - the manager wants []ContextSnippet{Source, Content, Score, Metadata}, sorted by score
//     descending, with an empty input yielding an empty slice and a nil error.
//
// The namespace is resolved PER REQUEST (explicit > tenant > user > configured fallback),
// not fixed at construction: the existing knowledge retriever learned that the hard way —
// hard-coding one namespace made a write to tenant A unreadable from tenant B.
//
// Failure behaviour: an error from the tiered path is returned (never a half-filled slice),
// because the caller's contract is "never a non-nil slice with an error". A tier that
// degraded on its own (L2 down → L1 results) reports that through Metadata rather than
// through an error: the snippets are real, only less complete.
type tieredContextRetriever struct {
	tiered *retriever.TieredRetriever
	// fallbackNamespace applies when neither tenant nor user context is present — the
	// same last resort the single-shot knowledge retriever uses.
	fallbackNamespace string
	// defaultTopK applies when the caller passes topK <= 0.
	defaultTopK int
	// minScore is the minimum snippet Score (object Confidence) to return. The tiered path
	// applies no threshold of its own, so without this the flag would silently LOWER the
	// quality bar the single-shot retriever enforces (adapter.DefaultMinScore, 0.4) and put
	// knowledge into prompts that the other path would have filtered out.
	minScore float64
}

// newTieredContextRetriever wires the tiered retriever into the memory manager's interface.
func newTieredContextRetriever(tiered *retriever.TieredRetriever, fallbackNamespace string, defaultTopK int, minScore float64) *tieredContextRetriever {
	if defaultTopK <= 0 {
		defaultTopK = 5
	}
	if minScore <= 0 {
		// Same fallback as adapter.NewKnowledgeRetriever: an unset threshold means "use the
		// documented default", not "accept everything".
		minScore = adapter.DefaultMinScore
	}
	return &tieredContextRetriever{
		tiered:            tiered,
		fallbackNamespace: fallbackNamespace,
		defaultTopK:       defaultTopK,
		minScore:          minScore,
	}
}

// Retrieve implements memctx.ContextRetriever.
func (t *tieredContextRetriever) Retrieve(ctx context.Context, input string, topK int) ([]memctx.ContextSnippet, error) {
	// Interface contract, stated by the interface itself: empty input is empty output with
	// a nil error. Returning an error here would make an empty prompt look like a fault.
	if input == "" {
		return nil, nil
	}
	if topK <= 0 {
		topK = t.defaultTopK
	}
	ns := knowledge.ResolveUserNamespace(tenantctx.From(ctx), userctx.From(ctx), t.fallbackNamespace)

	result, tier, err := t.tiered.Retrieve(ctx, retriever.Query{
		Text:       input,
		Namespace:  ns,
		MaxResults: topK,
	})
	if err != nil {
		return nil, err
	}
	if result == nil || result.Graph == nil || len(result.Graph.Nodes) == 0 {
		return nil, nil
	}

	snippets := make([]memctx.ContextSnippet, 0, len(result.Graph.Nodes))
	for _, obj := range result.Graph.Nodes {
		if obj == nil || obj.Summary == "" {
			// The manager filters empty content downstream anyway; dropping it here keeps
			// the "Content is non-empty for any snippet that survives" promise local.
			continue
		}
		if obj.Confidence < t.minScore {
			// The tiered path has no threshold of its own; see the minScore field for why
			// dropping this here is what keeps the flag from lowering the quality bar.
			continue
		}
		snippets = append(snippets, memctx.ContextSnippet{
			Source:  "knowledge",
			Content: obj.Summary,
			Score:   obj.Confidence,
			Metadata: map[string]any{
				"tier":      tier.String(),
				"object_id": obj.ID,
				"namespace": obj.Namespace,
				"type":      string(obj.Type),
			},
		})
	}

	// Contract: sorted by Score descending. The tiered path merges two sources (L1 hits are
	// high-precision, L2 adds semantic recall), so the merged order is not sorted by
	// construction and must be fixed here rather than assumed.
	sort.SliceStable(snippets, func(i, j int) bool { return snippets[i].Score > snippets[j].Score })

	// Contract: at most topK. The merge is NOT bounded — L1 hits are added to whatever L2
	// returned — so without this a caller asking for 5 could receive the whole graph.
	if len(snippets) > topK {
		snippets = snippets[:topK]
	}
	return snippets, nil
}

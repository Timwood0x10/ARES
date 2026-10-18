package evolution

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// KnowledgeGuidanceProvider turns AKG facts into evolution hints (M10).
//
// It is a GuidanceProvider, and that interface is the whole boundary: hints shape how
// candidates are PROPOSED (prompt shaping, parameter suggestions, constraints worth avoiding).
// Nothing this type returns can reach DimensionScores — fitness comes from execution evidence
// (M9), and a knowledge edge is not a measurement of a strategy. A relation saying "X caused
// Y" cannot prove that a candidate is better than the active one.
//
// The namespace is resolved per call and REQUIRED. knowledge.Query drops its namespace
// predicate when the namespace is empty, so a scoped miss here returns no hints rather than
// another tenant's facts.
type KnowledgeGuidanceProvider struct {
	store knowledge.KnowledgeStore
	// namespace resolves the caller's scope per request; nil means "never scoped", which
	// makes every lookup return nothing (see the type doc).
	namespace func(ctx context.Context) string
	limit     int
}

// NewKnowledgeGuidanceProvider wires AKG facts into candidate generation.
//
// namespace must resolve the tenant/user scope at request time. It is a function rather than a
// string because a deployment serves several tenants and a fixed value was exactly the bug the
// AKG read path had to fix once already.
func NewKnowledgeGuidanceProvider(store knowledge.KnowledgeStore, namespace func(ctx context.Context) string, limit int) (*KnowledgeGuidanceProvider, error) {
	if store == nil {
		return nil, fmt.Errorf("knowledge guidance: store is required")
	}
	if namespace == nil {
		return nil, fmt.Errorf("knowledge guidance: namespace resolver is required")
	}
	if limit <= 0 {
		limit = defaultKnowledgeHintLimit
	}
	return &KnowledgeGuidanceProvider{store: store, namespace: namespace, limit: limit}, nil
}

// defaultKnowledgeHintLimit caps how many facts a single proposal may be shaped by. A hint list
// that grows with the corpus would turn "narrow the search space" into a second retrieval
// problem.
const defaultKnowledgeHintLimit = 10

// HintsForTask implements GuidanceProvider.
func (p *KnowledgeGuidanceProvider) HintsForTask(ctx context.Context, taskType string, limit int) ([]EvolutionHint, error) {
	if p == nil || p.store == nil {
		return nil, nil
	}
	ns := p.namespace(ctx)
	if ns == "" {
		// Fail closed: an unscoped query would recall across tenants. Returning no hints is
		// the safe degradation — the GA simply proposes from experience alone.
		return nil, nil
	}
	if limit <= 0 {
		limit = p.limit
	}

	objects, err := p.store.Query(ctx, knowledge.Query{
		Namespace: ns,
		Types:     []knowledge.ObjectType{knowledge.ObjectMemory, knowledge.ObjectUser},
		Limit:     limit,
	})
	if err != nil {
		return nil, fmt.Errorf("knowledge guidance: query namespace %q: %w", ns, err)
	}

	hints := make([]EvolutionHint, 0, len(objects))
	for _, obj := range objects {
		if obj == nil || obj.Summary == "" {
			continue
		}
		hints = append(hints, EvolutionHint{
			ID:       "akg:" + obj.ID,
			TaskType: taskType,
			// The fact itself is the problem statement the hint speaks to; the solution half
			// stays empty because AKG records what IS, not what to do about it. Inventing a
			// solution here would be exactly the "knowledge grades candidates" mistake.
			Problem: obj.Summary,
			// Tags are the labels that narrowed the space when the fact was stored.
			Constraints:         obj.Tags,
			Confidence:          obj.Confidence,
			SourceExperienceIDs: []string{obj.ID},
		})
	}
	return hints, nil
}

// RecordStrategyOutcome implements GuidanceProvider by doing NOTHING, deliberately.
//
// This is the loop cut (M10). Writing GA outcomes back into the knowledge graph would create
// the cycle the plan warns about: GA changes a strategy → the change becomes knowledge → the
// next proposal is shaped by it → the strategy reinforces itself. The graph would fill with
// facts whose only source is the GA's own opinion, and nothing in a later retrieval could tell
// them apart from observed reality.
//
// Outcomes belong in the ExperimentLog, which the GA reads as HISTORY ("what did we try and
// what happened"), never as an input that can re-propose the candidate that produced it.
//
// Returning nil rather than an error is the contract: the caller's job is to record the
// outcome somewhere, and refusing here would fail the evolution step for a design choice.
func (p *KnowledgeGuidanceProvider) RecordStrategyOutcome(context.Context, StrategyOutcome) error {
	return nil
}

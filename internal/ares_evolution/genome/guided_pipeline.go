package genome

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// GuidedPipeline ties together Reflection, Hypothesis, and Knowledge Distillation
// into the existing mutation pipeline. It implements the full loop:
//
//	History → Reflect → Hypothesize → Guide Mutation → Record Outcome → Distill Knowledge
type GuidedPipeline struct {
	reflector     Reflector
	hypothesisGen *HypothesisGenerator
	knowledgeBase *KnowledgeBase
	distiller     *KnowledgeDistiller
}

// NewGuidedPipeline creates a complete intelligence pipeline.
// Any nil component degrades gracefully (skips that stage).
func NewGuidedPipeline(
	reflector Reflector,
	hypothesisGen *HypothesisGenerator,
	knowledgeBase *KnowledgeBase,
) *GuidedPipeline {
	p := &GuidedPipeline{
		reflector:     reflector,
		hypothesisGen: hypothesisGen,
		knowledgeBase: knowledgeBase,
	}
	if knowledgeBase != nil {
		p.distiller = NewKnowledgeDistiller(knowledgeBase)
	}
	return p
}

// RunReflectionCycle executes one full intelligence cycle:
// 1. Analyze history → generate reflection
// 2. Convert reflection → mutation hypotheses
// 3. Distill observations → knowledge base
//
// Note: RunReflectionCycle captures a snapshot of population state (history + agents)
// and then passes it to the LLM reflector. During the LLM call, other goroutines
// may evolve the population further (via EvolveOnIdle). The reflection is therefore
// based on a stale snapshot — this is intentional "async feedback" design:
// the system doesn't block evolution waiting for LLM analysis. If synchronous
// reflection is needed, callers should pause evolution before invoking this method.
func (p *GuidedPipeline) RunReflectionCycle(ctx context.Context, pop *Population) []MutationHypothesis {
	if p.reflector == nil || p.hypothesisGen == nil || pop == nil {
		return nil
	}

	history := pop.History()
	agents, _ := pop.Snapshot()

	// Step 1: Reflect.
	// Reflection proceeds even without history — the reflector may use
	// agent-level data or other context.
	ref, err := p.reflector.Reflect(ctx, history, agents)
	if err != nil {
		slog.WarnContext(ctx, "[GuidedPipeline] reflection failed", "error", err)
		return nil
	}

	if ref == nil {
		return nil
	}

	slog.InfoContext(ctx, "[GuidedPipeline] reflection completed",
		"summary", ref.Summary,
		"patterns", len(ref.Patterns),
		"recommendations", len(ref.Recommendations),
		"confidence", ref.Confidence,
	)

	// Step 2: Generate hypotheses.
	hypotheses := p.hypothesisGen.Generate(ctx, ref)
	if len(hypotheses) > 0 {
		slog.InfoContext(ctx, "[GuidedPipeline] generated mutation hypotheses",
			"count", len(hypotheses),
		)
	}

	// Step 3: Distill into knowledge base.
	if p.distiller != nil {
		p.distiller.DistillFromHistory(history)
	}

	return hypotheses
}

// HypothesisHintProvider implements mutation.HintProvider by converting
// mutation hypotheses into evolution hints. This bridges the intelligence
// layer into the existing guided mutation pipeline.
type HypothesisHintProvider struct {
	pipeline      *GuidedPipeline
	pop           *Population
	minConfidence float64
}

// NewHypothesisHintProvider creates a hint provider backed by the intelligence pipeline.
func NewHypothesisHintProvider(pipeline *GuidedPipeline, pop *Population, minConfidence float64) *HypothesisHintProvider {
	if minConfidence <= 0 {
		minConfidence = 0.4
	}
	return &HypothesisHintProvider{
		pipeline:      pipeline,
		pop:           pop,
		minConfidence: minConfidence,
	}
}

// HintsForTask generates evolution hints by running a reflection cycle and
// converting hypotheses into the standard EvolutionHint format.
func (h *HypothesisHintProvider) HintsForTask(ctx context.Context, taskType string, limit int) ([]mutation.EvolutionHint, error) {
	hypotheses := h.pipeline.RunReflectionCycle(ctx, h.pop)
	if len(hypotheses) == 0 {
		return []mutation.EvolutionHint{}, nil
	}

	hints := make([]mutation.EvolutionHint, 0, len(hypotheses))
	for _, hyp := range hypotheses {
		if hyp.Confidence < h.minConfidence {
			continue
		}
		hint := mutation.EvolutionHint{
			ID:         fmt.Sprintf("hyp-%d", time.Now().UnixNano()),
			TaskType:   taskType,
			Problem:    hyp.Rationale,
			Confidence: hyp.Confidence,
		}

		// Map hypothesis target to hint fields.
		switch hyp.TargetType {
		case "param":
			hint.ParamHints = map[string]float64{hyp.TargetKey: 0.0}
			if hyp.SuggestedValue != nil {
				if v, ok := hyp.SuggestedValue.(float64); ok {
					hint.ParamHints[hyp.TargetKey] = v
				}
			}
		case "prompt":
			if hyp.SuggestedValue != nil {
				if s, ok := hyp.SuggestedValue.(string); ok {
					hint.PromptSnippets = []string{s}
				}
			}
		case "tool":
			hint.PreferredTools = []string{hyp.TargetKey}
		}

		hints = append(hints, hint)
		if len(hints) >= limit {
			break
		}
	}

	return hints, nil
}

// RecordStrategyOutcome is a no-op for hypothesis-based providers since
// outcomes are already recorded through the knowledge base distillation.
func (h *HypothesisHintProvider) RecordStrategyOutcome(ctx context.Context, outcome mutation.StrategyOutcome) error {
	return nil
}

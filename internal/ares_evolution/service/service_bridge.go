// Package evolution — bridge types and conversion helpers.
package evolution

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

type apiGuidanceBridge struct {
	provider GuidanceProvider
}

func (b *apiGuidanceBridge) HintsForTask(ctx context.Context, taskType string, limit int) ([]evolution.EvolutionHint, error) {
	hints, err := b.provider.HintsForTask(ctx, taskType, limit)
	if err != nil || hints == nil {
		return nil, err
	}
	out := make([]evolution.EvolutionHint, len(hints))
	for i, h := range hints {
		out[i] = evolution.EvolutionHint{
			TaskType: h.TaskType, Problem: h.Problem, Solution: h.Solution,
			Constraints: h.Constraints,
		}
	}
	return out, nil
}

func (b *apiGuidanceBridge) RecordStrategyOutcome(ctx context.Context, outcome evolution.StrategyOutcome) error {
	return nil
}

type apiMemoryBridge struct {
	provider MemoryExperienceProvider
}

func (b *apiMemoryBridge) FindSimilar(ctx context.Context, taskType string, limit int) (int, float64, error) {
	if b.provider != nil {
		return b.provider.FindSimilar(ctx, taskType, limit)
	}
	return 0, 0, nil
}

type llmClientAdapter struct {
	inner interface{ Generate(ctx context.Context, prompt string) (string, error) }
}

func (a *llmClientAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	return a.inner.Generate(ctx, prompt)
}

func toAPIStrategy(s *mutation.Strategy) *Strategy {
	if s == nil {
		return nil
	}
	return &Strategy{
		ID: s.ID, Version: s.Version, Score: s.Score,
		ParentID: s.ParentID, PromptTemplate: s.PromptTemplate,
		MutationType: s.StrategyMutationType.String(),
	}
}

func toInternalStrategy(s *Strategy) *mutation.Strategy {
	if s == nil {
		return nil
	}
	return &mutation.Strategy{
		ID: s.ID, Version: s.Version, Score: s.Score,
		ParentID: s.ParentID, PromptTemplate: s.PromptTemplate,
		StrategyMutationType: mutation.ParseMutationType(s.MutationType),
	}
}

func cloneDimensionScores(src map[string]float64) map[string]float64 {
	if src == nil {
		return nil
	}
	dst := make(map[string]float64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func toAPILineage(l interface{}) StrategyLineage { return StrategyLineage{} }

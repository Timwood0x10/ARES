// Package evolution provides GA mode integration for the DreamCycle orchestrator.
// This file contains the GA-specific evolution path and adapters.
package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// genomeMutatorAdapter adapts evolution.MutatorInterface to genome.MutatorInterface.
// It handles the type conversion between evolution.Strategy and mutation.Strategy.
type genomeMutatorAdapter struct {
	inner MutatorInterface
}

// Mutate adapts the evolution.MutatorInterface to genome.MutatorInterface.
// Converts *mutation.Strategy to evolution.Strategy before calling inner.Mutate,
// then converts []evolution.Strategy back to []*mutation.Strategy.
func (a *genomeMutatorAdapter) Mutate(ctx context.Context, parent *mutation.Strategy, n int) ([]*mutation.Strategy, error) {
	evoParent := mutationToEvolutionStrategy(parent)
	children, err := a.inner.Mutate(ctx, evoParent, n)
	if err != nil {
		return nil, err
	}
	result := make([]*mutation.Strategy, len(children))
	for i, child := range children {
		ms := evolutionToMutationStrategy(child)
		result[i] = &ms
	}
	return result, nil
}

// runGAEvolution executes the full genetic algorithm path.
// Score population → evolve (selection/crossover/mutation) → deploy best.
func (dc *DreamCycle) runGAEvolution(ctx context.Context, cycleCtx context.Context, data CallbackData) error {
	if dc.population == nil {
		return fmt.Errorf("GA population not initialized; ensure GA config is set")
	}

	gen := dc.population.CurrentGeneration()
	slog.InfoContext(ctx, "[DreamCycle] Starting GA evolution cycle",
		"agent_id", data.AgentID,
		"generation", gen,
		"population_size", len(dc.population.Agents))

	// Check termination conditions: max generations or target fitness reached.
	if dc.config.MaxGenerations > 0 && gen >= dc.config.MaxGenerations {
		slog.InfoContext(ctx, "[DreamCycle] Max generations reached, stopping",
			"generation", gen,
			"max_generations", dc.config.MaxGenerations)
		return nil
	}
	if dc.config.TargetFitness > 0 && dc.population.BestEverScore() >= dc.config.TargetFitness {
		slog.InfoContext(ctx, "[DreamCycle] Target fitness reached, stopping",
			"score", dc.population.BestEverScore(),
			"target", dc.config.TargetFitness)
		return nil
	}

	// Step 1: Score all agents using the tester.
	scorer := dc.buildGAScorer(ctx)
	dc.population.ScoreAgents(scorer)

	// Step 2: Run one GA generation (selection → crossover → mutation).
	genMutator := &genomeMutatorAdapter{inner: dc.mutator}
	if dc.config.SteadyState {
		if err := dc.population.EvolveSteadyState(ctx, genMutator, dc.crosser, dc.config.SteadyStateReplaceRate); err != nil {
			return fmt.Errorf("GA steady-state evolve: %w", err)
		}
	} else {
		if err := dc.population.Evolve(ctx, genMutator, dc.crosser); err != nil {
			return fmt.Errorf("GA evolve: %w", err)
		}
	}

	// Step 3: Get the best strategy from the population.
	best := dc.population.BestStrategy()
	if best == nil {
		slog.WarnContext(ctx, "[DreamCycle] GA evolution produced no best strategy")
		return nil
	}

	// Step 4: Deploy the best strategy.
	winnerStrategy := mutationToEvolutionStrategy(best)
	winner := &candidateResult{
		strategy:         winnerStrategy,
		winRate:          best.Score,
		scoreImprovement: best.Score - dc.population.BestEverScore(),
	}

	// Record lineage.
	if dc.genealogy != nil {
		lineage := StrategyLineage{
			ChildID:          best.ID,
			MutationType:     "ga_evolution",
			WinRate:          best.Score,
			ScoreImprovement: winner.scoreImprovement,
			ChildScore:       best.Score,
			Timestamp:        time.Now().Unix(),
		}
		if err := dc.genealogy.Record(ctx, lineage); err != nil {
			slog.ErrorContext(ctx, "[DreamCycle] Failed to record GA lineage", "error", err)
		}
	}

	// parent for deployWinner is the best strategy from the previous generation.
	parent := mutation.Strategy{
		ID:    best.ID,
		Score: best.Score,
	}
	return dc.deployWinner(ctx, cycleCtx, data, winner, parent)
}

// buildGAScorer creates a scoring function that evaluates each strategy
// using the arena tester against the current best strategy.
func (dc *DreamCycle) buildGAScorer(ctx context.Context) func(*mutation.Strategy) float64 {
	return func(s *mutation.Strategy) float64 {
		if genome.IsScoreEvaluated(s.Score) {
			return s.Score
		}

		evolvedStrategy := mutationToEvolutionStrategy(s)
		baseline := dc.getBestStrategyOrDefault()

		result, err := dc.tester.Run(ctx, RegressionConfig{
			Candidate:         evolvedStrategy,
			Baseline:          baseline,
			TaskSampleSize:    dc.config.TaskSampleSize,
			AdaptiveBatchSize: 5,
		})
		if err != nil {
			slog.WarnContext(ctx, "[DreamCycle] GA scorer failed",
				"strategy_id", s.ID, "error", err)
			return -1
		}

		return result.CandidateScore
	}
}

// getBestStrategyOrDefault returns the best strategy from the population
// or the current active strategy as a fallback baseline.
func (dc *DreamCycle) getBestStrategyOrDefault() Strategy {
	if dc.population != nil {
		best := dc.population.BestStrategy()
		if best != nil {
			return mutationToEvolutionStrategy(best)
		}
	}
	if dc.stateManager != nil {
		if cur := dc.stateManager.Current(); cur != nil {
			return mutationToEvolutionStrategy(cur)
		}
	}
	return defaultRootStrategy()
}

package evolution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// candidateResult holds an evaluated candidate strategy with its test results.
type candidateResult struct {
	strategy         Strategy
	winRate          float64
	scoreImprovement float64
}

// initGAPopulation initializes a genome.Population from the current active strategy
// and GA configuration. Called during NewDreamCycle when EvolutionMode is GA.
func (dc *DreamCycle) initGAPopulation(ctx context.Context) error {
	parent, err := dc.getCurrentStrategy(ctx)
	if err != nil {
		return fmt.Errorf("get current strategy for GA population: %w", err)
	}

	base := evolutionToMutationStrategy(parent)
	genMutator := &genomeMutatorAdapter{inner: dc.mutator}
	pop, err := genome.NewPopulation(ctx, &base, genMutator,
		genome.WithPopulationSize(dc.config.PopulationSize),
		genome.WithEliteCount(dc.config.EliteCount),
		genome.WithMutationRate(dc.config.MutationRate),
		genome.WithSurvivalRate(dc.config.SurvivalRate),
		genome.WithSelectionStrategy(dc.config.SelectionStrategy),
		genome.WithTournamentSelection(dc.config.TournamentSize),
	)
	if err != nil {
		return fmt.Errorf("new population: %w", err)
	}
	dc.population = pop
	return nil
}

// runESEvolution executes the existing (1+λ) evolution strategy path.
// Mutate parent → test candidates → deploy best.
func (dc *DreamCycle) runESEvolution(ctx context.Context, cycleCtx context.Context, data CallbackData, taskCount int64) error {
	popGen := 0
	popSize := 0
	if dc.population != nil {
		popGen = dc.population.CurrentGeneration()
		// Stats().Size is read under the population RLock; doEvolve can
		// replace p.Agents wholesale under the write lock (REVIEW #55).
		popSize = dc.population.Stats().Size
	}
	slog.InfoContext(ctx, "[DreamCycle] Starting ES evolution cycle",
		"agent_id", data.AgentID,
		"task_count", taskCount,
		"trigger", dc.scheduler.TriggerMode().String(),
		"generation", popGen,
		"population_size", popSize)

	// Pre-evolution guardrail check.
	var currentBest float64
	if dc.stateManager != nil {
		if cur := dc.stateManager.Current(); cur != nil {
			currentBest = cur.Score
		}
	}
	if dc.guardrails != nil {
		gen := 0
		unevaluatedCount := 0
		popSize := 0
		if dc.population != nil {
			gen = dc.population.CurrentGeneration()
			agents, _ := dc.population.Snapshot()
			popSize = len(agents)
			for _, a := range agents {
				if !genome.IsScoreEvaluated(a.Score) {
					unevaluatedCount++
				}
			}
		}
		preResult := dc.guardrails.PreEvolveCheck(ctx, currentBest, gen, popSize, unevaluatedCount)
		if preResult.ShouldStop {
			slog.WarnContext(ctx, "[DreamCycle] Pre-evolution guardrails prevent cycle",
				"events", len(preResult.Events))
			return nil
		}
	}

	// Step 1: Get current active strategy as parent for mutation.
	parent, err := dc.getCurrentStrategy(ctx)
	if err != nil {
		slog.WarnContext(ctx, "[DreamCycle] Failed to get active strategy", "error", err)
		return nil
	}
	if parent.ID == "" {
		slog.WarnContext(ctx, "[DreamCycle] No active strategy available, skipping")
		return nil
	}

	// Step 2: Generate candidate mutations.
	candidates, err := dc.mutator.Mutate(cycleCtx, parent, dc.config.MaxMutations)
	if err != nil {
		return fmt.Errorf("mutate strategy: %w", err)
	}
	if len(candidates) == 0 {
		slog.InfoContext(ctx, "[DreamCycle] No candidates generated")
		return nil
	}

	// Step 3: Test each candidate in arena and find best winner.
	winner, err := dc.findWinner(ctx, candidates, parent, data.AgentID)
	if errors.Is(err, ErrAllCandidatesRejected) {
		slog.InfoContext(ctx, "[DreamCycle] No candidate passed win rate threshold",
			"min_win_rate", dc.config.MinWinRate)
		dc.recordFailure(ctx, parent)
		return nil
	}
	if err != nil {
		return fmt.Errorf("arena regression: %w", err)
	}
	if winner == nil {
		slog.InfoContext(ctx, "[DreamCycle] No candidate passed win rate threshold",
			"min_win_rate", dc.config.MinWinRate)
		dc.recordFailure(ctx, parent)
		return nil
	}

	// Step 4: Record lineage.
	if dc.genealogy != nil {
		lineage := StrategyLineage{
			ParentID:         parent.ID,
			ChildID:          winner.strategy.ID,
			MutationType:     "dream_cycle",
			WinRate:          winner.winRate,
			ScoreImprovement: winner.scoreImprovement,
			ParentScore:      parent.Score,
			ChildScore:       winner.scoreImprovement + parent.Score,
			Timestamp:        time.Now().Unix(),
		}
		if err := dc.genealogy.Record(ctx, lineage); err != nil {
			slog.ErrorContext(ctx, "[DreamCycle] Failed to record lineage",
				"error", err)
		}
	}

	// Convert parent Strategy to mutation.Strategy for deployWinner.
	parentMut := evolutionToMutationStrategy(parent)
	return dc.deployWinner(ctx, cycleCtx, data, winner, parentMut)
}

// deployWinner handles the common deployment logic for both ES and GA paths.
// It runs post-evolution guardrails, shadow evaluation, and deploys via stateManager.
func (dc *DreamCycle) deployWinner(
	ctx context.Context,
	cycleCtx context.Context,
	data CallbackData,
	winner *candidateResult,
	parent mutation.Strategy,
) error {
	if winner == nil {
		return nil
	}

	// Set cooldown at entry so every early-return path (guardrail reject,
	// shadow reject, nil winner conversion) also respects it. Previously
	// only the successful-deploy path set lastCycle, letting rejected
	// candidates re-trigger immediately.
	defer func() {
		dc.mu.Lock()
		dc.lastCycle = time.Now()
		dc.mu.Unlock()
	}()

	// Post-evolution guardrail check.
	if dc.guardrails != nil {
		gen := 0
		var lineageShares map[string]int
		if dc.population != nil {
			gen = dc.population.CurrentGeneration()
			if agents, _ := dc.population.Snapshot(); len(agents) > 0 {
				lineageShares = computeLineageShares(agents)
			}
		}
		postResult := dc.guardrails.PostEvolveCheckForSource(ctx, "dream_cycle", winner.winRate, gen, lineageShares)
		if postResult.ShouldStop {
			slog.WarnContext(ctx, "[DreamCycle] Post-evolution guardrails block deploy",
				"winner_id", winner.strategy.ID,
				"win_rate", winner.winRate,
				"events", len(postResult.Events))
			return nil
		}
	}

	// Shadow evaluation before deployment.
	if dc.shadowEvaluator != nil {
		mtnWinner := winnerToMutationStrategy(winner)
		if mtnWinner == nil {
			slog.ErrorContext(ctx, "[DreamCycle] winnerToMutationStrategy returned nil, skipping shadow")
			return nil
		}
		parentMutation := parent
		dc.shadowEvaluator.SetActiveStrategy(&parentMutation)
		dc.shadowEvaluator.StartShadow(mtnWinner)

		if dc.shadowEvaluator.HasIndependentScorer() {
			dc.shadowEvaluator.Evaluate(ctx)
		} else {
			dc.shadowEvaluator.RecordResult(parent.Score, winner.scoreImprovement+parent.Score)
		}

		shouldDeploy, report := dc.shadowEvaluator.ShouldDeployLoose()
		// Keep the shadow win-rate gauge current on every comparison
		// batch so /metrics reflects the live shadow gate state instead of a
		// permanently zero gauge.
		if dc.metrics != nil && report != nil {
			dc.metrics.SetEvolutionShadowWinRate(report.WinRate)
		}
		// LOOSE contract (DreamCycle defers on insufficient data;
		// the lifecycle shadow gate uses the STRICT ShouldDeploy): StartShadow
		// resets the results, so the first call here carries a single
		// comparison (total < MinSamples), and a strict reading would veto
		// every early deployment. ShouldDeployLoose therefore returns
		// shouldDeploy=true with an "insufficient samples" report when it
		// cannot judge yet — surface that explicitly, and only block
		// deployment when enough samples actually show the shadow strategy
		// underperforming.
		//
		// The insufficiency bar is RAW comparisons (decisive + ties).
		// TotalComparisons alone is decisive-only since B-3, so an all-tie
		// wall would read as 0 < MinSamples and flip to "proceed" — the
		// opposite of the pre-B-3 rejection it replaced. Counting ties in the
		// sample-size check keeps "not enough data → defer" separate from
		// "enough data, all ties → reject" (the latter is ShouldDeployLoose's
		// total==0 rejection).
		insufficient := report == nil || report.TotalComparisons+report.TieCount < dc.shadowEvaluator.minSamples
		switch {
		case insufficient:
			reason := "insufficient samples, proceeding with deployment"
			if report != nil && report.Recommendation != "" {
				reason = report.Recommendation
			}
			slog.InfoContext(ctx, "[DreamCycle] Shadow evaluation cannot conclude, proceeding",
				"candidate_id", winner.strategy.ID,
				"active_id", parent.ID,
				"reason", reason)
			if dc.metrics != nil {
				dc.metrics.RecordEvolutionShadow("insufficient-samples")
			}
		case !shouldDeploy:
			slog.InfoContext(ctx, "[DreamCycle] Shadow evaluation rejects deployment",
				"candidate_id", winner.strategy.ID,
				"active_id", parent.ID,
				"win_rate", report.WinRate,
				"threshold", dc.shadowEvaluator.minWinRate,
				"reason", report.Recommendation)
			if dc.metrics != nil {
				dc.metrics.RecordEvolutionShadow("rejected")
			}
			return nil
		default:
			if dc.metrics != nil {
				dc.metrics.RecordEvolutionShadow("promoted")
			}
		}
	}

	// Deploy via ActiveStrategyManager.
	if dc.stateManager != nil {
		mtnWinner := winnerToMutationStrategy(winner)
		if mtnWinner == nil {
			slog.ErrorContext(ctx, "[DreamCycle] winnerToMutationStrategy returned nil, skipping deploy")
			return nil
		}
		if err := ValidateStrategySize(mtnWinner); err != nil {
			slog.ErrorContext(ctx, "[DreamCycle] Winning strategy exceeds size limits",
				"winner_id", winner.strategy.ID,
				"error", err)
			return nil
		}
		if err := dc.stateManager.Deploy(cycleCtx, mtnWinner); err != nil {
			slog.ErrorContext(ctx, "[DreamCycle] Failed to deploy winning strategy",
				"winner_id", winner.strategy.ID, "error", err)
			return nil
		}
		slog.InfoContext(ctx, "[DreamCycle] Winning strategy deployed",
			"winner_id", winner.strategy.ID,
			"win_rate", winner.winRate)
		if dc.metrics != nil {
			dc.metrics.RecordEvolutionDeploy("success")
			dc.metrics.SetEvolutionScore(winner.strategy.ID, winner.winRate)
		}

		// Record outcome for hint provider.
		if dc.hintProvider != nil {
			outcome := mutation.StrategyOutcome{
				StrategyID:   winner.strategy.ID,
				TaskType:     data.AgentID,
				Success:      true,
				Score:        winner.winRate,
				MutationType: "ga_evolution",
				Timestamp:    time.Now(),
			}
			if err := dc.hintProvider.RecordStrategyOutcome(cycleCtx, outcome); err != nil {
				slog.WarnContext(ctx, "[DreamCycle] Failed to record strategy outcome",
					"error", err)
			}
		}
	}

	slog.InfoContext(ctx, "[DreamCycle] Evolution cycle complete",
		"winner_id", winner.strategy.ID,
		"win_rate", winner.winRate,
		"score_improvement", winner.scoreImprovement,
		"trigger", dc.scheduler.TriggerMode().String())

	return nil
}

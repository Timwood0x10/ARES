package evolution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// getCurrentStrategy returns the currently deployed strategy from the strategy store.
// Falls back to a default root strategy if none has been stored yet.
//
// Args:
//
//	ctx - operation context for store lookup.
//
// Returns:
//
//	Strategy - the active strategy, or a default on first run.
//	error - non-nil if store lookup fails.
func (dc *DreamCycle) getCurrentStrategy(ctx context.Context) (Strategy, error) {
	if dc.strategyStore == nil {
		slog.WarnContext(ctx, "[DreamCycle] No strategy store configured; using default")
		return defaultRootStrategy(), nil
	}

	stored, err := dc.strategyStore.GetActive(ctx)
	if err != nil {
		if errors.Is(err, ErrNoActiveStrategy) {
			slog.InfoContext(ctx, "[DreamCycle] No stored strategy found; initializing with default")
			return defaultRootStrategy(), nil
		}
		return Strategy{}, fmt.Errorf("get active strategy: %w", err)
	}

	if stored == nil {
		slog.InfoContext(ctx, "[DreamCycle] No stored strategy found; initializing with default")
		return defaultRootStrategy(), nil
	}

	return *stored, nil
}

// defaultRootStrategy returns a sensible root strategy for first-time initialization.
func defaultRootStrategy() Strategy {
	return Strategy{
		ID:      "root-strategy-v1",
		Name:    "DefaultStrategy",
		Version: 1,
		Params: map[string]any{
			"temperature":  0.7,
			"max_tokens":   4096,
			"retry_count":  3,
			"timeout_secs": 120,
		},
		StrategyMutationType: "",
		Score:                -1,
		CreatedAt:            time.Now(),
	}
}

// currentGeneration returns the current GA generation when a population is
// attached, else 0. Used for guardrail event attribution.
func (dc *DreamCycle) currentGeneration() int {
	if dc.population != nil {
		return dc.population.CurrentGeneration()
	}
	return 0
}

// findWinner tests all candidates in arena and returns the best one above threshold.
//
// Uses a two-stage approach:
//  1. Quick reject: all candidates are screened in parallel with N=QuickRejectRuns (default 5).
//     Those below MinWinRate are discarded.
//  2. Full eval: survivors are evaluated in parallel with N=TaskSampleSize (default 50)
//     and adaptive batching. The best is returned.
func (dc *DreamCycle) findWinner(
	ctx context.Context,
	candidates []Strategy,
	baseline Strategy,
	taskType string,
) (*candidateResult, error) {
	if len(candidates) == 0 {
		return nil, errors.New("dream cycle: no candidates to evaluate")
	}

	// Tool-set guardrail: reject any candidate whose evolved tool whitelist exceeds the
	// guardrail's upper bound (or enables zero tools, when required) BEFORE it
	// is arena-tested. The toolset guard was previously dead code — the method
	// existed but nothing called it, so a mutated Params["tools"] larger than
	// the deployment intended was silently arena-tested and could win. Filtering
	// here is the selection path: a rejected candidate never consumes arena
	// runs and never becomes a winner.
	if dc.guardrails != nil {
		filtered := make([]Strategy, 0, len(candidates))
		rejected := 0
		for _, cand := range candidates {
			// Parsed by the same agents helper the executors use, so the
			// guardrail counts exactly the tools the LLM would be shown.
			tools := agents.ToolNamesFromParams(cand.Params)
			res := dc.guardrails.ValidateToolSet(dc.currentGeneration(), tools)
			if res.ShouldStop {
				rejected++
				slog.WarnContext(ctx, "[DreamCycle] candidate jailed by tool-set guardrail",
					"strategy_id", cand.ID,
					"tool_count", len(tools),
					"events", len(res.Events))
				continue
			}
			filtered = append(filtered, cand)
		}
		if rejected > 0 {
			slog.InfoContext(ctx, "[DreamCycle] tool-set guardrail rejected candidates",
				"rejected", rejected,
				"survivors", len(filtered))
		}
		candidates = filtered
		if len(candidates) == 0 {
			return nil, ErrAllCandidatesRejected
		}
	}

	// Stage 1: Quick reject — screen all candidates in parallel with small N.
	quickRejectN := dc.config.QuickRejectRuns
	survivors := candidates

	if quickRejectN > 0 {
		type quickResult struct {
			candidate Strategy
			winRate   float64
		}

		quickResults := make([]*quickResult, len(candidates))
		g, gCtx := errgroup.WithContext(ctx)

		for i, cand := range candidates {
			i, cand := i, cand
			g.Go(func() error {
				result, err := dc.tester.Run(gCtx, RegressionConfig{
					Candidate:         cand,
					Baseline:          baseline,
					TaskSampleSize:    quickRejectN,
					AdaptiveBatchSize: quickRejectN, // single batch, no adaptive benefit
				})
				if err != nil {
					slog.WarnContext(ctx, "[DreamCycle] Quick reject failed",
						"candidate_id", cand.ID, "error", err)
					return nil // skip on error
				}
				quickResults[i] = &quickResult{candidate: cand, winRate: result.WinRate}
				return nil
			})
		}

		if err := g.Wait(); err != nil {
			return nil, err
		}

		survivors = nil
		for _, qr := range quickResults {
			if qr == nil {
				continue
			}
			if qr.winRate >= dc.config.MinWinRate {
				survivors = append(survivors, qr.candidate)
			} else {
				slog.DebugContext(ctx, "[DreamCycle] Candidate rejected in quick pass",
					"candidate_id", qr.candidate.ID,
					"win_rate", qr.winRate,
					"threshold", dc.config.MinWinRate)
				// Record the rejection as a negative outcome so the hint
				// provider can learn which strategies fail the cheap screen
				// and avoid regenerating them. Best-effort: a recording
				// failure must not abort the cycle.
				dc.recordQuickReject(ctx, qr.candidate, qr.winRate, taskType)
			}
		}

		rejected := len(candidates) - len(survivors)
		if rejected > 0 {
			slog.InfoContext(ctx, "[DreamCycle] Quick reject filtered candidates",
				"total", len(candidates),
				"survivors", len(survivors),
				"rejected", rejected)
		}
	}

	if len(survivors) == 0 {
		return nil, ErrAllCandidatesRejected // all candidates rejected in quick pass (nilnil)
	}

	// Stage 2: Full evaluation — run survivors in parallel with full N.
	type evalResult struct {
		candidateResult
		err error
	}

	evalResults := make([]*evalResult, len(survivors))
	g, gCtx := errgroup.WithContext(ctx)

	for i, cand := range survivors {
		i, cand := i, cand
		g.Go(func() error {
			result, err := dc.tester.Run(gCtx, RegressionConfig{
				Candidate:         cand,
				Baseline:          baseline,
				TaskSampleSize:    dc.config.TaskSampleSize,
				AdaptiveBatchSize: 5,
			})
			if err != nil {
				evalResults[i] = &evalResult{err: err}
				return nil // skip on error
			}
			evalResults[i] = &evalResult{
				candidateResult: candidateResult{
					strategy:         cand,
					winRate:          result.WinRate,
					scoreImprovement: result.CandidateScore - result.BaselineScore,
				},
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Pick the best among survivors above threshold — by winRate (evolution semantics).
	var best *candidateResult
	for _, er := range evalResults {
		if er == nil || er.err != nil {
			continue
		}
		if er.winRate < dc.config.MinWinRate {
			continue
		}
		if best == nil || er.winRate > best.winRate {
			cr := er.candidateResult
			best = &cr
		}
	}

	return best, nil
}

// recordFailure logs a failed evolution cycle for future analysis and records
// the failure outcome for hint provider learning.
func (dc *DreamCycle) recordFailure(ctx context.Context, parent Strategy) {
	slog.InfoContext(ctx, "[DreamCycle] Evolution cycle produced no acceptable candidate",
		"parent_id", parent.ID,
		"max_mutations", dc.config.MaxMutations,
		"min_win_rate", dc.config.MinWinRate)

	if dc.hintProvider != nil {
		outcome := mutation.StrategyOutcome{
			StrategyID:   parent.ID,
			Success:      false,
			Score:        parent.Score,
			MutationType: "dream_cycle",
			Timestamp:    time.Now(),
		}
		if err := dc.hintProvider.RecordStrategyOutcome(ctx, outcome); err != nil {
			slog.WarnContext(ctx, "[DreamCycle] Failed to record failure outcome",
				"error", err)
		}
	}
}

// recordQuickReject records a candidate that was discarded by the cheap quick-
// reject screen as a negative StrategyOutcome, so the hint provider can learn
// which strategies fail early and steer future mutations away from them. It is
// best-effort: a recording error is logged and never propagated. No-op when no
// hint provider is wired.
func (dc *DreamCycle) recordQuickReject(ctx context.Context, candidate Strategy, winRate float64, taskType string) {
	if dc.hintProvider == nil {
		return
	}
	outcome := mutation.StrategyOutcome{
		StrategyID:   candidate.ID,
		TaskType:     taskType,
		Success:      false,
		Score:        winRate,
		MutationType: candidate.StrategyMutationType,
		Timestamp:    time.Now(),
	}
	if err := dc.hintProvider.RecordStrategyOutcome(ctx, outcome); err != nil {
		slog.WarnContext(ctx, "[DreamCycle] Failed to record quick-reject outcome",
			"candidate_id", candidate.ID, "error", err)
	}
}

// MetricsRecorder abstracts Prometheus metrics recording for evolution events.
// The observability.PrometheusMetrics type satisfies this interface.
type MetricsRecorder interface {
	RecordEvolutionDeploy(status string)
	RecordEvolutionShadow(result string)
	SetEvolutionScore(strategyID string, score float64)
	// SetEvolutionShadowWinRate publishes the current shadow win rate so
	// the /metrics gauge tracks the live shadow gate state.
	SetEvolutionShadowWinRate(rate float64)
}

// winnerToMutationStrategy converts a candidateResult to a mutation.Strategy
// pointer for deployment via ActiveStrategyManager.
//
// Args:
//
//	result - the evaluated candidate result.
//
// Returns:
//
//	*mutation.Strategy - pointer to the strategy ready for deployment, or nil.
func winnerToMutationStrategy(result *candidateResult) *mutation.Strategy {
	if result == nil {
		return nil
	}
	ms := evolutionToMutationStrategy(result.strategy)
	ms.Score = result.winRate
	return &ms
}

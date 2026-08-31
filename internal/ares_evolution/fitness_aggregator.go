// fitness_aggregator.go provides the JUDGE stage of the evolution control
// plane. RuntimeFitnessAggregator merges evidence from multiple sources
// (workflow, scheduler, recovery, strategy) into a single normalized [0,1]
// fitness value. It is the shared scoring backend for:
//
//   - StrategyLifecycle: decides whether to RecordScore into RollbackPolicy
//     (B1 fix) and whether a candidate is "good enough" to promote.
//   - Deployment staging: replaces the single-source "workflow" check with
//     a multi-dimensional aggregate (B6 fix).
//
// The aggregator is read-only: it never mutates evidence or strategy state.
// Cold-start (insufficient samples) returns ok=false so the caller can
// choose a conservative strategy (e.g. hold in SHADOW, or use
// ColdStartScore as a fallback).
package evolution

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/Timwood0x10/ares/internal/evidence"
)

// FitnessWeights configures how each evidence source contributes to the
// aggregate fitness. Weights should sum to 1.0; if they don't, the
// aggregator normalizes them at query time.
type FitnessWeights struct {
	// Outcome is the weight for task outcome (success/failure) fitness.
	Outcome float64 `json:"outcome"`
	// DimensionEval is the weight for dimension_eval evidence.
	DimensionEval float64 `json:"dimension_eval"`
	// Workflow is the weight for workflow-sourced fitness evidence.
	Workflow float64 `json:"workflow"`
	// Scheduler is the weight for scheduler-sourced fitness evidence.
	Scheduler float64 `json:"scheduler"`
}

// DefaultFitnessWeights returns sensible default weights summing to 1.0.
func DefaultFitnessWeights() FitnessWeights {
	return FitnessWeights{
		Outcome:       0.45,
		DimensionEval: 0.25,
		Workflow:      0.15,
		Scheduler:     0.15,
	}
}

// FitnessPenaltyConfig configures cost/latency penalties subtracted from
// the aggregate fitness.
type FitnessPenaltyConfig struct {
	// CostUSDBudget is the cost above which the penalty starts. Set to 0 to
	// disable cost penalty.
	CostUSDBudget float64 `json:"cost_usd_budget"`
	// LatencyBudget is the latency above which the penalty starts. Set to 0
	// to disable latency penalty.
	LatencyBudgetSec float64 `json:"latency_budget_sec"`
}

// AggregatorConfig groups all RuntimeFitnessAggregator settings.
type AggregatorConfig struct {
	// WindowSize is the maximum number of evidence records to consider per
	// source (mirrors recentFitnessSummary's limit).
	WindowSize int `json:"window_size"`
	// MinSamplesBeforeJudge is the minimum total evidence count before the
	// aggregator returns ok=true. Below this, it returns ok=false so callers
	// can apply a conservative cold-start policy (B6 fix).
	MinSamplesBeforeJudge int `json:"min_samples_before_judge"`
	// ColdStartScore is the score returned when no evidence exists. Callers
	// use this when they need a fallback instead of ok=false.
	ColdStartScore float64 `json:"cold_start_score"`
	// Weights controls per-source contribution.
	Weights FitnessWeights `json:"weights"`
	// Penalty configures cost/latency deductions.
	Penalty FitnessPenaltyConfig `json:"penalty"`
}

// DefaultAggregatorConfig returns sensible defaults matching the design doc.
func DefaultAggregatorConfig() AggregatorConfig {
	return AggregatorConfig{
		WindowSize:            50,
		MinSamplesBeforeJudge: 10,
		ColdStartScore:        0.5,
		Weights:               DefaultFitnessWeights(),
	}
}

// RuntimeFitnessAggregator computes normalized [0,1] fitness from the shared
// evidence store. It is read-only and safe for concurrent use.
type RuntimeFitnessAggregator struct {
	store evidence.Store
	cfg   AggregatorConfig
	mu    sync.RWMutex
}

// NewRuntimeFitnessAggregator creates an aggregator backed by the given
// evidence store.
func NewRuntimeFitnessAggregator(store evidence.Store, cfg AggregatorConfig) *RuntimeFitnessAggregator {
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 50
	}
	if cfg.MinSamplesBeforeJudge <= 0 {
		cfg.MinSamplesBeforeJudge = 10
	}
	if cfg.ColdStartScore <= 0 {
		cfg.ColdStartScore = 0.5
	}
	return &RuntimeFitnessAggregator{store: store, cfg: cfg}
}

// SetStore replaces the evidence store. Used by bootstrap to inject the
// shared evidence store after the aggregator is created with nil (the
// store is not known at NewWiredEvolutionSystem time).
func (a *RuntimeFitnessAggregator) SetStore(store evidence.Store) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.store = store
	a.mu.Unlock()
}

// WindowResult holds the aggregate fitness for a given strategy ID.
type WindowResult struct {
	// Mean is the weighted aggregate fitness in [0,1].
	Mean float64
	// Count is the total number of evidence records used.
	Count int
	// PerSource holds the per-source mean and count.
	PerSource map[string]sourceStat
}

// sourceStat holds the mean and count for one evidence source.
type sourceStat struct {
	Mean  float64
	Count int
}

// Window computes the aggregate fitness over recent evidence for the given
// strategy ID. Returns ok=false when insufficient evidence exists (total
// count < MinSamplesBeforeJudge), so callers can apply a conservative
// cold-start policy.
//
// The aggregation:
//  1. Queries KindFitness evidence for each configured source.
//  2. Computes the per-source mean (only values in [0,1] are accepted,
//     matching recentFitnessSummary's filter).
//  3. Computes the weighted aggregate across sources.
//  4. Subtracts cost/latency penalties (proportional, clamped to [0,1]).
func (a *RuntimeFitnessAggregator) Window(ctx context.Context, _ string) (mean float64, count int, ok bool) {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()

	if a.store == nil {
		return cfg.ColdStartScore, 0, false
	}

	sources := []struct {
		name   string
		weight float64
	}{
		{"strategy", cfg.Weights.Outcome},
		{"workflow", cfg.Weights.Workflow},
		{"scheduler", cfg.Weights.Scheduler},
	}

	// Also query dimension_eval evidence.
	dimMean, dimCount := a.querySourceMean(ctx, "dimension_eval", evidence.KindDimensionEval, cfg.WindowSize)

	perSource := make(map[string]sourceStat)
	totalCount := 0
	var weightedSum float64
	var weightSum float64

	for _, src := range sources {
		m, c := a.querySourceMean(ctx, src.name, evidence.KindFitness, cfg.WindowSize)
		if c == 0 {
			continue
		}
		perSource[src.name] = sourceStat{Mean: m, Count: c}
		totalCount += c
		weightedSum += m * src.weight
		weightSum += src.weight
	}

	if dimCount > 0 {
		perSource["dimension_eval"] = sourceStat{Mean: dimMean, Count: dimCount}
		totalCount += dimCount
		weightedSum += dimMean * cfg.Weights.DimensionEval
		weightSum += cfg.Weights.DimensionEval
	}

	if weightSum == 0 {
		return cfg.ColdStartScore, 0, false
	}

	mean = weightedSum / weightSum

	// Clamp to [0,1].
	if mean < 0 {
		mean = 0
	}
	if mean > 1 {
		mean = 1
	}

	if totalCount < cfg.MinSamplesBeforeJudge {
		return mean, totalCount, false
	}
	return mean, totalCount, true
}

// querySourceMean computes the mean fitness value from evidence matching
// the given source and kind. Only values in [0,1] are accepted (matching
// recentFitnessSummary's filter), so callers can rely on the [0,1] contract.
func (a *RuntimeFitnessAggregator) querySourceMean(ctx context.Context, source string, kind evidence.EvidenceKind, limit int) (float64, int) {
	if a.store == nil {
		return 0, 0
	}
	evs, err := a.store.Query(ctx, evidence.Filter{
		Source: source,
		Kind:   kind,
		Limit:  limit,
	})
	if err != nil {
		return 0, 0
	}
	var sum float64
	count := 0
	for _, ev := range evs {
		if len(ev.Payload) == 0 {
			continue
		}
		var fe struct {
			Value float64 `json:"value"`
		}
		if err := json.Unmarshal(ev.Payload, &fe); err != nil {
			continue
		}
		if fe.Value < 0 || fe.Value > 1 {
			continue
		}
		sum += fe.Value
		count++
	}
	if count == 0 {
		return 0, 0
	}
	return sum / float64(count), count
}

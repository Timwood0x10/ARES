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
	"time"

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
	// Recovery is the weight for recovery-sourced fitness evidence.
	Recovery float64 `json:"recovery"`
}

// DefaultFitnessWeights returns sensible default weights summing to 1.0.
func DefaultFitnessWeights() FitnessWeights {
	return FitnessWeights{
		Outcome:       0.40,
		DimensionEval: 0.25,
		Workflow:      0.15,
		Scheduler:     0.15,
		Recovery:      0.05,
	}
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
}

// TODO(tech-debt): the design doc (ga-runtime-evolution-design-zh.md §4 ②)
// specifies a cost/latency penalty term subtracted from the aggregate
// fitness (penalty(cost, latency)). It is not implemented because task
// events carry no cost or latency data today — see the observer.go
// tech-debt note. Wire it once flight-trace cost/latency reaches the
// EventStore payloads; do not reintroduce a config struct before a real
// data source exists (no dead config fields).

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
	// LastAt is the NEWEST evidence timestamp inside the window. Under
	// steady-state churn (window saturated: one record in, one record out)
	// Count stays flat, so "did the window advance" must be judged by this
	// timestamp, not by Count — a count-based check silently stops
	// RecordingScore forever once every source hits WindowSize (the
	// rollback path would die without any error or warning).
	LastAt time.Time
	// Ok reports whether the judging gate passed (see Window's doc).
	Ok bool
}

// sourceStat holds the mean, count, and newest timestamp of one evidence
// source inside the window.
type sourceStat struct {
	Mean   float64
	Count  int
	LastAt time.Time
}

// Window computes the aggregate fitness over recent evidence for the given
// strategy ID. Returns Ok=false when insufficient evidence exists, so callers
// can apply a conservative cold-start policy.
//
// The aggregation:
//  1. Queries KindFitness evidence for each configured source.
//  2. Computes the per-source mean (only values in [0,1] are accepted,
//     matching recentFitnessSummary's filter).
//  3. Computes the weighted aggregate across sources.
//
// strategyID scoping AND the judging gate (review fix #4): only the
// "strategy" source is scoped by the ID (its records carry a strategy_id
// payload key written by RuntimeObserver). The workflow/scheduler/recovery
// sources are runtime-global — they measure the system that runs the active
// strategy, not a specific candidate — so they intentionally ignore the ID.
//
//   - When strategyID is NON-empty (the rollback-decision path), the
//     "strategy" source must ITSELF hold ≥ MinSamplesBeforeJudge records for
//     the given ID before Ok=true. Global sources contribute to the weighted
//     mean but can never substitute for the active strategy's own evidence
//     (design doc §4⑤ principle 4: rollback decisions must rest on the
//     strategy's own evidence). Without
//     this gate, 10 unrelated global records would license a rollback
//     decision while the strategy's own sample count is 0.
//   - When strategyID is empty (deployment staging), the gate is the total
//     count across sources, matching the pre-existing staging contract.
//
// WindowResult.LastAt carries the newest in-window evidence timestamp:
// callers that feed a score into a sliding policy (the lifecycle watch loop)
// MUST gate on LastAt advancing, never on Count — Count saturates at
// WindowSize per source and stops changing under steady-state churn.
//
// LastAt is deliberately the STRATEGY source's newest timestamp only (when
// the caller scopes by a strategy ID): the global sources churn at their own
// rates, and a global-only advance would re-trigger RecordScore every tick
// while the strategy's own fitness sample set is unchanged — partially
// defeating the decorrelation. Callers needing the overall newest timestamp
// can take the max over PerSource.
func (a *RuntimeFitnessAggregator) Window(ctx context.Context, strategyID string) WindowResult {
	// Read cfg and store under the SAME lock: SetStore may run concurrently
	// with Window (bootstrap injects the shared store after construction),
	// and an unlocked store read is a data race.
	a.mu.RLock()
	cfg := a.cfg
	store := a.store
	a.mu.RUnlock()

	if store == nil {
		return WindowResult{Mean: cfg.ColdStartScore, PerSource: map[string]sourceStat{}}
	}

	sources := []struct {
		name       string
		weight     float64
		strategyID string
	}{
		{"strategy", cfg.Weights.Outcome, strategyID},
		{"workflow", cfg.Weights.Workflow, ""},
		{"scheduler", cfg.Weights.Scheduler, ""},
		{"recovery", cfg.Weights.Recovery, ""},
	}

	// Also query dimension_eval evidence.
	dimMean, dimCount, dimLastAt := a.querySourceMean(ctx, store, "dimension_eval", evidence.KindDimensionEval, cfg.WindowSize, "")

	perSource := make(map[string]sourceStat)
	totalCount := 0
	// strategyCount: samples of the STRATEGY source alone — the judging
	// gate for the rollback path (see the doc comment on Window).
	strategyCount := 0
	strategyLastAt := time.Time{}
	globalLastAt := time.Time{}
	var weightedSum float64
	var weightSum float64

	for _, src := range sources {
		m, c, srcLastAt := a.querySourceMean(ctx, store, src.name, evidence.KindFitness, cfg.WindowSize, src.strategyID)
		if c == 0 {
			continue
		}
		perSource[src.name] = sourceStat{Mean: m, Count: c, LastAt: srcLastAt}
		totalCount += c
		weightedSum += m * src.weight
		weightSum += src.weight
		if srcLastAt.After(globalLastAt) {
			globalLastAt = srcLastAt
		}
		if src.name == "strategy" {
			strategyCount = c
			strategyLastAt = srcLastAt
		}
	}

	if dimCount > 0 {
		perSource["dimension_eval"] = sourceStat{Mean: dimMean, Count: dimCount, LastAt: dimLastAt}
		totalCount += dimCount
		weightedSum += dimMean * cfg.Weights.DimensionEval
		weightSum += cfg.Weights.DimensionEval
		if dimLastAt.After(globalLastAt) {
			globalLastAt = dimLastAt
		}
	}

	result := WindowResult{PerSource: perSource}

	if weightSum == 0 {
		result.Mean = cfg.ColdStartScore
		return result
	}

	mean := weightedSum / weightSum

	// TODO(tech-debt): subtract the cost/latency penalty term here once a
	// real cost/latency data source reaches the EventStore (see the
	// tech-debt note on AggregatorConfig above).

	// Clamp to [0,1].
	if mean < 0 {
		mean = 0
	}
	if mean > 1 {
		mean = 1
	}
	result.Mean = mean
	result.Count = totalCount

	if strategyID != "" {
		// Rollback path: the active strategy's OWN evidence must reach the
		// judge threshold. Global sources weight the mean but never satisfy
		// the gate on the strategy's behalf (review fix #4). The advance
		// signal (LastAt) is likewise scoped to the strategy source.
		result.Ok = strategyCount >= cfg.MinSamplesBeforeJudge
		result.LastAt = strategyLastAt
		return result
	}
	// Staging path: advance signal is the newest timestamp across all
	// sources (the staging Evaluate has no decorrelation consumer; LastAt
	// here is informational).
	result.Ok = totalCount >= cfg.MinSamplesBeforeJudge
	result.LastAt = globalLastAt
	return result
}

// querySourceMean computes the mean fitness value from evidence matching
// the given source and kind. Only values in [0,1] are accepted (matching
// recentFitnessSummary's filter), so callers can rely on the [0,1] contract.
// When strategyID is non-empty, records whose payload strategy_id differs
// are skipped (the strategy source scopes by candidate); records without a
// strategy_id payload key are skipped too, because they cannot be attributed.
// The returned time is the newest in-window record's timestamp (zero when no
// records matched) — the saturation-safe "did the window advance" signal.
// The store is passed in (not read from the receiver) so Window can snapshot
// it under its lock and keep this helper lock-free.
func (a *RuntimeFitnessAggregator) querySourceMean(ctx context.Context, store evidence.Store, source string, kind evidence.EvidenceKind, limit int, strategyID string) (float64, int, time.Time) {
	if store == nil {
		return 0, 0, time.Time{}
	}
	evs, err := store.Query(ctx, evidence.Filter{
		Source: source,
		Kind:   kind,
		Limit:  limit,
	})
	if err != nil {
		return 0, 0, time.Time{}
	}
	var sum float64
	count := 0
	var lastAt time.Time
	for _, ev := range evs {
		if len(ev.Payload) == 0 {
			continue
		}
		var fe struct {
			Value      float64 `json:"value"`
			StrategyID string  `json:"strategy_id"`
		}
		if err := json.Unmarshal(ev.Payload, &fe); err != nil {
			continue
		}
		if fe.Value < 0 || fe.Value > 1 {
			continue
		}
		if strategyID != "" && fe.StrategyID != strategyID {
			continue
		}
		sum += fe.Value
		count++
		if ev.Timestamp.After(lastAt) {
			lastAt = ev.Timestamp
		}
	}
	if count == 0 {
		return 0, 0, time.Time{}
	}
	return sum / float64(count), count, lastAt
}

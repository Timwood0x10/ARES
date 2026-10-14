// fitness_aggregator.go provides the JUDGE stage of the evolution control
// plane. RuntimeFitnessAggregator merges evidence from multiple sources
// (workflow, scheduler, recovery, strategy) into a single normalized [0,1]
// fitness value. It is the shared scoring backend for:
//
//   - StrategyLifecycle: decides whether to RecordScore into RollbackPolicy
//     and whether a candidate is "good enough" to promote.
//   - Deployment staging: replaces the single-source "workflow" check with
//     a multi-dimensional aggregate.
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
	// Collaboration weights cross-agent collaboration receipts ("should I
	// have asked THAT agent?"). Default 0 — the channel is opt-in,
	// so an operator who has not enabled collab feedback sees an unchanged
	// aggregate even if stray evidence exists.
	Collaboration float64 `json:"collaboration"`
	// ToolCall weights tool-invocation outcomes ("was calling THAT
	// tool worth it?"). Default 0, same opt-in reasoning as Collaboration.
	ToolCall float64 `json:"tool_call"`
}

// DefaultFitnessWeights returns sensible default weights summing to 1.0.
// Collaboration and ToolCall are deliberately 0: those channels
// are opt-in, and giving them a non-zero default would silently redistribute
// every existing deployment's fitness mix on upgrade.
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
	// can apply a conservative cold-start policy.
	MinSamplesBeforeJudge int `json:"min_samples_before_judge"`
	// ColdStartScore is the score returned when no evidence exists. Callers
	// use this when they need a fallback instead of ok=false.
	ColdStartScore float64 `json:"cold_start_score"`
	// Weights controls per-source contribution.
	Weights FitnessWeights `json:"weights"`
}

// The design doc's cost/latency penalty term is applied at the OBSERVER
// (sample production): completed-task scores already carry the multiplicative
// latency penalty (see observer.go latencyPenalty) and the token-spend cost
// penalty (observer.go costPenalty — the envelope's cumulative usage rides
// terminal task.completed events since schema v4), so the aggregate inherits
// both through the window mean — no subtraction here. (A USD-denominated
// cost term was deliberately removed: the token dimension carries the cost
// signal, and no price table exists to denominate it.)

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
	// OutOfRange counts the records that were DROPPED because their scalar fell
	// outside [0,1], among the records THIS window covers — the scope filters
	// (strategy_id / tool_step_id) are applied first, so a scoped window never blames
	// itself for another producer's bad values. It is reported because the alternative
	// is silence: a producer
	// writing a value in the wrong unit (milliseconds, a token count, a 0-100
	// score) is skipped by the range check, so the only symptom is "this source has
	// no evidence" — the same silent-void failure mode the dimension_eval weight
	// already suffered once. A non-zero value here means "the number below is
	// computed without samples that exist", which is exactly the fact an operator
	// needs and a bare mean cannot carry. Records with NO scalar are not counted:
	// they are diagnosis-only records by design, not a unit error.
	OutOfRange int
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
// can apply a conservative cold-start policy. Without a time range it queries
// the most recent WindowSize records per source (legacy behavior). Callers
// that compare two strategies (deployment staging Evaluate) MUST use WindowAt
// with a shared [since, until] anchor instead — two independent Window calls
// can straddle concurrent evidence writes and distort the delta.
func (a *RuntimeFitnessAggregator) Window(ctx context.Context, strategyID string) WindowResult {
	return a.WindowAt(ctx, strategyID, time.Time{}, time.Time{})
}

// WindowAt is Window scoped to the [since, until] evidence time range.
// Zero since/until disables that bound (matching MemoryStore/PostgresStore
// semantics). Both bounds SHOULD be non-zero for staging comparisons so the
// shadow and baseline sides read the same snapshot.
//
// The aggregation:
//  1. Queries KindFitness evidence for each configured source.
//  2. Computes the per-source mean (only values in [0,1] are accepted,
//     matching recentFitnessSummary's filter).
//  3. Computes the weighted aggregate across sources.
//
// strategyID scoping AND the judging gate (review fix #4): the "strategy"
// source is scoped by the ID (its records carry a strategy_id payload key
// written by RuntimeObserver), and so are the two Step Y channels
// ("collaboration" / "tool_call" — a receipt is earned by the strategy that
// chose to ask/call). The workflow/scheduler/recovery sources are
// runtime-global — they measure the system that runs the active strategy, not
// a specific candidate — so they intentionally ignore the ID.
//
//   - When strategyID is NON-empty (the rollback-decision path), the
//     "strategy" source must ITSELF hold ≥ MinSamplesBeforeJudge records for
//     the given ID before Ok=true. Global sources contribute to the weighted
//     mean but can never substitute for the active strategy's own evidence
//     (rollback decisions must rest on the
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
func (a *RuntimeFitnessAggregator) WindowAt(ctx context.Context, strategyID string, since, until time.Time) WindowResult {
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
		// optIn marks a source that is INERT at weight 0: it is skipped
		// entirely rather than merely contributing 0 to the weighted mean.
		// This matters because a counted source also advances totalCount,
		// which is the staging path's judging gate — a channel nobody opted
		// into must not license a staging verdict.
		optIn bool
	}{
		{name: "strategy", weight: cfg.Weights.Outcome, strategyID: strategyID},
		{name: "workflow", weight: cfg.Weights.Workflow},
		{name: "scheduler", weight: cfg.Weights.Scheduler},
		{name: "recovery", weight: cfg.Weights.Recovery},
		// Step Y.2/Y.3 channels. Both are strategy-SCOPED: unlike the
		// runtime-global sources above, a collaboration receipt or a tool
		// outcome is produced BY a specific strategy's decisions ("ask that
		// agent", "call that tool"), so crediting it to another strategy would
		// be a mis-attribution.
		{name: collaborationEvidenceSource, weight: cfg.Weights.Collaboration, strategyID: strategyID, optIn: true},
		{name: toolCallEvidenceSource, weight: cfg.Weights.ToolCall, strategyID: strategyID, optIn: true},
	}

	// Also query dimension_eval evidence — the accuracy signal (defect G1 / P0-1:
	// "the runtime observe path produces no correctness signal").
	//
	// Filtered by KIND ONLY, and that is the fix, not an oversight: Evidence.Source
	// names the VERIFIER that produced a record (see the Evidence.Source contract;
	// the dimension judge writes "dimension_judge"), while this query used to ask
	// for source == "dimension_eval" — the KIND string, which no producer ever
	// wrote. The result was zero samples forever, so the DimensionEval weight
	// (0.25) silently contributed nothing to fitness and GA optimised without any
	// correctness signal at all. The kind is the contract here: one producer, one
	// meaning, and the payload carries the scalar (`value`, 0..1) read below.
	dimMean, dimCount, dimLastAt, dimOutOfRange := a.querySourceMeanAt(ctx, store, "", evidence.KindDimensionEval, cfg.WindowSize, "", since, until)

	perSource := make(map[string]sourceStat)
	totalCount := 0
	// strategyCount: samples of the STRATEGY source alone — the judging
	// gate for the rollback path (see the doc comment on Window).
	strategyCount := 0
	strategyLastAt := time.Time{}
	globalLastAt := time.Time{}
	var weightedSum float64
	var weightSum float64
	// outOfRangeTotal: records dropped for an out-of-range scalar, across every
	// source AND the dimension channel (see WindowResult.OutOfRange).
	outOfRangeTotal := 0

	for _, src := range sources {
		if src.optIn && src.weight <= 0 {
			// Not opted in: skip entirely so the source contributes neither
			// weight nor sample count (see the optIn field comment).
			continue
		}
		m, c, srcLastAt, srcOutOfRange := a.querySourceMeanAt(ctx, store, src.name, evidence.KindFitness, cfg.WindowSize, src.strategyID, since, until)
		// Counted even when the source has NO usable samples (c == 0): that is
		// exactly the case where the window silently has nothing, and the counter is
		// what tells an operator the samples existed but were unusable.
		outOfRangeTotal += srcOutOfRange
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

	// Counted OUTSIDE the usable-samples branch: when EVERY dimension record is out
	// of range, dimCount is 0 — which is precisely the case the counter exists for
	// (a channel that looks empty but is not), while the warning fires regardless.
	// A counter that only reports when the signal was fine is worse than none.
	outOfRangeTotal += dimOutOfRange
	if dimCount > 0 {
		perSource["dimension_eval"] = sourceStat{Mean: dimMean, Count: dimCount, LastAt: dimLastAt}
		totalCount += dimCount
		weightedSum += dimMean * cfg.Weights.DimensionEval
		weightSum += cfg.Weights.DimensionEval
		if dimLastAt.After(globalLastAt) {
			globalLastAt = dimLastAt
		}
	}

	result := WindowResult{PerSource: perSource, OutOfRange: outOfRangeTotal}

	if weightSum == 0 {
		result.Mean = cfg.ColdStartScore
		return result
	}

	mean := weightedSum / weightSum

	// No explicit penalty subtraction: the latency penalty is already folded
	// into the completed-task samples the observer produces (see observer.go
	// latencyPenalty), so the window mean inherits it.

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

// querySourceMeanAt computes the mean fitness value from evidence matching
// the given source and kind within [since, until]. Only values in [0,1]
// are accepted (matching recentFitnessSummary's filter), so callers can rely
// on the [0,1] contract. When strategyID is non-empty, records whose payload
// strategy_id differs are skipped (the strategy source scopes by candidate);
// records without a strategy_id payload key are skipped too, because they
// cannot be attributed. The returned time is the newest in-window record's
// timestamp (zero when no records matched) — the saturation-safe "did the
// window advance" signal. The store is passed in (not read from the receiver)
// so WindowAt can snapshot it under its lock and keep this helper lock-free.
func (a *RuntimeFitnessAggregator) querySourceMeanAt(ctx context.Context, store evidence.Store, source string, kind evidence.EvidenceKind, limit int, strategyID string, since, until time.Time) (float64, int, time.Time, int) {
	return a.querySourceMeanScopedAt(ctx, store, source, kind, limit, strategyID, "", since, until)
}

// querySourceMeanScopedAt is querySourceMeanAt with an optional tool_step_id
// sub-filter and an explicit [since, until] evidence time range.
// When toolStepID is non-empty, only tool_call evidence whose payload
// tool_step_id matches is counted — enabling process-level attribution
// ("this strategy calling the tool THIS way") distinct from the coarse
// per-strategy bucket.
//
// The strategy_id / tool_step_id equality filters are pushed into the store
// query (Filter.PayloadFilter) rather than applied after the fact: the query
// is LIMIT-bounded, and a client-side filter would only see the most recent
// WindowSize records REGARDLESS of strategy — under multi-strategy traffic the
// window fills with other strategies' records, the scoped strategy's count
// stays 0, and the rollback judge gate (MinSamplesBeforeJudge on the
// strategy's OWN samples) never opens, silently disarming the safety net.
func (a *RuntimeFitnessAggregator) querySourceMeanScopedAt(ctx context.Context, store evidence.Store, source string, kind evidence.EvidenceKind, limit int, strategyID, toolStepID string, since, until time.Time) (float64, int, time.Time, int) {
	if store == nil {
		return 0, 0, time.Time{}, 0
	}
	filter := evidence.Filter{
		Source: source,
		Kind:   kind,
		Since:  since,
		Until:  until,
		Limit:  limit,
	}
	if strategyID != "" {
		if filter.PayloadFilter == nil {
			filter.PayloadFilter = make(map[string]any, 2)
		}
		filter.PayloadFilter["strategy_id"] = strategyID
	}
	if toolStepID != "" {
		if filter.PayloadFilter == nil {
			filter.PayloadFilter = make(map[string]any, 2)
		}
		filter.PayloadFilter["tool_step_id"] = toolStepID
	}
	evs, err := store.Query(ctx, filter)
	if err != nil {
		return 0, 0, time.Time{}, 0
	}
	var sum float64
	count := 0
	outOfRange := 0
	// firstOutOfRange keeps one example for the warning below. The warning is emitted
	// AFTER the loop on purpose: this helper is documented as lock-free (the store is
	// passed in so WindowAt can snapshot it under its own lock), and the limiter takes a
	// process-wide mutex — acquiring it per RECORD would put that lock on the query's
	// hot path for a producer that writes nothing but bad values.
	firstOutOfRange := 0.0
	var lastAt time.Time
	for _, ev := range evs {
		if len(ev.Payload) == 0 {
			continue
		}
		var fe struct {
			// Value is a POINTER so "absent" stays distinguishable from 0.0. A
			// record whose producer wrote no scalar (an older build, or a
			// diagnosis-only record) must be SKIPPED, not counted: folding it in
			// as 0.0 drags this whole source's mean toward zero, which reads as
			// "everything failed" — a silently WRONG signal, worse than an absent
			// one.
			Value *float64 `json:"value"`
			// StrategyID/ToolStepID remain in the unmarshal target so a
			// store implementation WITHOUT PayloadFilter support (third-party
			// Store impls) still gets correct results via the client-side
			// fallback below.
			StrategyID string `json:"strategy_id"`
			ToolStepID string `json:"tool_step_id"`
		}
		if err := json.Unmarshal(ev.Payload, &fe); err != nil {
			continue
		}
		// Scope filters FIRST, then the value check: a scoped window must report only
		// the out-of-range records that belong to it. Counting before the filters made
		// WindowToolStepAt blame this tool step for another step's bad records — the
		// counter would describe a population the mean was never computed over.
		if strategyID != "" && fe.StrategyID != strategyID {
			continue
		}
		if toolStepID != "" && fe.ToolStepID != toolStepID {
			continue
		}
		if fe.Value == nil {
			// Absent is not wrong: a diagnosis-only record (or one written by an
			// older build) carries no scalar, and folding it in as 0.0 would read as
			// "everything failed". Skipped quietly, and NOT counted as out of range.
			continue
		}
		if *fe.Value < 0 || *fe.Value > 1 {
			// A value outside [0,1] is nearly always a UNIT error at the producer
			// (milliseconds, a token count, a 0-100 score rather than a 0-1 one).
			// The record cannot be used, but its absence must not be silent — see
			// WindowResult.OutOfRange.
			if outOfRange == 0 {
				firstOutOfRange = *fe.Value
			}
			outOfRange++
			continue
		}
		sum += *fe.Value
		count++
		if ev.Timestamp.After(lastAt) {
			lastAt = ev.Timestamp
		}
	}
	if outOfRange > 0 {
		// One warning per (source, kind) for the whole process, raised once per query
		// rather than once per record — see firstOutOfRange.
		warnOutOfRangeValue(ctx, source, kind, firstOutOfRange)
	}
	if count == 0 {
		return 0, 0, time.Time{}, outOfRange
	}
	return sum / float64(count), count, lastAt, outOfRange
}

// maxOutOfRangeValueWarnings bounds the "evidence value outside [0,1]" warnings:
// one per (source, kind) pair, then a single notice that further ones are
// suppressed. Same shape as the MCP health join's cap and for the same reason — a
// real finding must not become a per-tick log sink in a deployment whose producer
// is writing the wrong unit for a whole source.
const maxOutOfRangeValueWarnings = 32

var (
	outOfRangeWarnMu         sync.Mutex
	outOfRangeWarnedSources  = map[string]struct{}{}
	outOfRangeWarnSuppressed bool
)

// warnOutOfRangeValue reports, at most once per (source, kind), that a record was
// dropped for carrying a scalar outside [0,1]. It returns whether it logged, which
// is what a test can assert without reaching into the logger (the module logger is
// bound to slog's default at package init, so swapping the default in a test does
// not intercept it).
//
// The value travels with the warning on purpose: "3600" and "1.4" point at
// different producers, and the number is the fastest way to identify the unit.
func warnOutOfRangeValue(ctx context.Context, source string, kind evidence.EvidenceKind, value float64) bool {
	key := source + "\x00" + string(kind)
	if source == "" {
		key = "(unscoped)\x00" + string(kind)
	}

	outOfRangeWarnMu.Lock()
	if _, seen := outOfRangeWarnedSources[key]; seen {
		outOfRangeWarnMu.Unlock()
		return false
	}
	if len(outOfRangeWarnedSources) >= maxOutOfRangeValueWarnings {
		alreadySuppressed := outOfRangeWarnSuppressed
		outOfRangeWarnSuppressed = true
		outOfRangeWarnMu.Unlock()
		if alreadySuppressed {
			return false
		}
		log.WarnContext(ctx, "fitness aggregator: further out-of-range evidence warnings are suppressed",
			"limit", maxOutOfRangeValueWarnings)
		return true
	}
	outOfRangeWarnedSources[key] = struct{}{}
	outOfRangeWarnMu.Unlock()

	log.WarnContext(ctx, "fitness aggregator: evidence value outside [0,1] was dropped",
		"source", source, "kind", string(kind), "value", value,
		"hint", "this is usually a producer unit error (ms, tokens, or a 0-100 score instead of 0-1)")
	return true
}

// WindowToolStep computes the aggregate fitness scoped to a specific tool step
// (strategyID, toolStepID) for the tool_call channel. The tool_step
// dimension surfaces process-level attribution: two strategies — or two
// argument shapes under the same strategy — calling the same tool no longer
// blend into one undifferentiated signal. It reuses the same cold-start gate
// and source weights as Window; the returned Ok reflects the tool_step-scoped
// sample count only when a non-empty strategyID is supplied.
func (a *RuntimeFitnessAggregator) WindowToolStep(ctx context.Context, strategyID, toolStepID string) WindowResult {
	return a.WindowToolStepAt(ctx, strategyID, toolStepID, time.Time{}, time.Time{})
}

// WindowToolStepAt is WindowToolStep scoped to [since, until].
func (a *RuntimeFitnessAggregator) WindowToolStepAt(ctx context.Context, strategyID, toolStepID string, since, until time.Time) WindowResult {
	if toolStepID == "" {
		return a.WindowAt(ctx, strategyID, since, until)
	}
	a.mu.RLock()
	cfg := a.cfg
	store := a.store
	a.mu.RUnlock()
	if store == nil {
		return WindowResult{Mean: cfg.ColdStartScore, PerSource: map[string]sourceStat{}}
	}

	m, c, lastAt, outOfRange := a.querySourceMeanScopedAt(ctx, store, toolCallEvidenceSource, evidence.KindFitness, cfg.WindowSize, strategyID, toolStepID, since, until)
	result := WindowResult{PerSource: map[string]sourceStat{}, OutOfRange: outOfRange}
	if c == 0 {
		result.Mean = cfg.ColdStartScore
		result.Ok = false
		return result
	}
	result.PerSource[toolStepID] = sourceStat{Mean: m, Count: c, LastAt: lastAt}
	result.Mean = m
	result.Count = c
	result.LastAt = lastAt
	// When no strategy is supplied the projection-level query is not scoped by
	// strategy (the tool-step audit is per-agent/session, and the Projector has
	// no strategy to stamp). Judge on sample count — the staging-path gate —
	// just like Window's non-strategy path, so the audit read is usable rather
	// than permanently cold-start.
	result.Ok = c >= cfg.MinSamplesBeforeJudge
	return result
}

package arena

import (
	"sync"
	"time"
)

// maxRecoverySamples bounds the recovery timeseries. Without a cap a
// long-running process accumulates samples forever (one per successful
// revival), making Snapshot's O(n) scan grow without bound. The cap turns
// it into a rolling window: older samples are evicted FIFO, so the
// snapshot approximates recent recovery performance rather than the full
// lifetime average.
const maxRecoverySamples = 1000

// MetricsSnapshot holds a point-in-time view of arena metrics.
type MetricsSnapshot struct {
	// Timing metrics
	AvgRecoveryTime  time.Duration `json:"avg_recovery_time"`
	MinRecoveryTime  time.Duration `json:"min_recovery_time"`
	MaxRecoveryTime  time.Duration `json:"max_recovery_time"`
	LastRecoveryTime time.Duration `json:"last_recovery_time"`

	// Counting metrics
	TotalRecoveries  int `json:"total_recoveries"`
	FailedRecoveries int `json:"failed_recoveries"`

	// Data consistency
	DataConsistencyRate float64 `json:"data_consistency_rate"` // 0-100

	// Unmeasured names the fields above that NO producer filled, so a zero in them means
	// "never measured" rather than "measured as zero".
	//
	// Recovery timing now has a producer — the P3 recovery observer feeds RecordRecovery —
	// so it clears as soon as the first revival lands. Data consistency still has none
	// (RecordConsistency has no production caller), so it stays listed and the score's
	// consistency dimension keeps falling back to its heuristic (calcConsistency). The list
	// exists so an operator (and a reviewer) can tell a fallback from a real measurement —
	// the silent-degradation shape this codebase keeps closing.
	Unmeasured []string `json:"unmeasured,omitempty"`

	// Per-action-type breakdown
	ActionStats map[string]ActionMetric `json:"action_stats"`
}

// ActionMetric holds aggregated metrics for a single action type.
type ActionMetric struct {
	Total       int           `json:"total"`
	Success     int           `json:"success"`
	Failed      int           `json:"failed"`
	AvgDuration time.Duration `json:"avg_duration"`
}

// MetricsCollector collects and aggregates arena metrics.
type MetricsCollector struct {
	mu                 sync.RWMutex
	recoveries         []time.Duration
	actionCounts       map[ActionType]*actionMetricInternal
	consistencySamples []float64
}

type actionMetricInternal struct {
	total, success, failed int
	totalDuration          time.Duration
}

// NewMetricsCollector creates a new MetricsCollector with initialized fields.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		recoveries:   make([]time.Duration, 0),
		actionCounts: make(map[ActionType]*actionMetricInternal),
	}
}

// RecordActionResult records the result of an action for per-type metrics.
func (mc *MetricsCollector) RecordActionResult(actionType ActionType, success bool, duration time.Duration) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	am, ok := mc.actionCounts[actionType]
	if !ok {
		am = &actionMetricInternal{}
		mc.actionCounts[actionType] = am
	}
	am.total++
	if success {
		am.success++
		am.totalDuration += duration
	} else {
		am.failed++
	}
}

// RecordRecovery records a recovery duration sample.
//
// This is the production entry point for recovery timing: the recovery
// observer (arena.NewRecoveryAdapter → aresrecovery.RecoveryObserver) calls
// this from RecoverFromAgentDeath on each successful revival. The snapshot
// fields it feeds (TotalRecoveries, Avg/Min/Max/LastRecoveryTime) are read by
// the chaos panel's RecoveryMetrics (via kernel.arenaMetrics.Snapshot(), wired
// in cmd/ares).
//
// They are NOT read by CalculateScore: the arena score takes its average
// recovery time from scenario-run events (calculateAvgRecoveryTime) and reads
// only DataConsistencyRate off a *MetricsSnapshot* — and from the arena
// Service's own collector, a separate instance from this one. The seam that
// would connect the two (Service.RecoveryObserver) currently has no production
// caller, so it is dead surface, not a live path.
//
// The samples slice is bounded by maxRecoverySamples to prevent unbounded
// growth in long-running processes — older samples are evicted (FIFO) once
// the cap is reached, so the snapshot approximates a rolling window rather
// than accumulating the full lifetime history.
func (mc *MetricsCollector) RecordRecovery(d time.Duration) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if len(mc.recoveries) >= maxRecoverySamples {
		// FIFO eviction: drop the oldest sample to make room. This keeps the
		// slice bounded without a separate eviction pass.
		mc.recoveries = mc.recoveries[1:]
	}
	mc.recoveries = append(mc.recoveries, d)
}

// RecordConsistency records a data consistency rate sample (0-100).
//
// No production caller exists: DataConsistencyRate is read by calcConsistency
// (which falls back to a heuristic when zero), but no code path produces
// honest consistency samples. The field stays in Unmeasured as a result.
// This method remains for test coverage of the legacy consistency-timeseries
// path; removing it is only honest together with making the fallback
// explicit in the score.
func (mc *MetricsCollector) RecordConsistency(rate float64) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.consistencySamples = append(mc.consistencySamples, rate)
}

// Snapshot returns a point-in-time MetricsSnapshot.
func (mc *MetricsCollector) Snapshot() MetricsSnapshot {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	snap := MetricsSnapshot{
		TotalRecoveries: len(mc.recoveries),
	}

	// Recovery timing stats.
	if len(mc.recoveries) > 0 {
		var total time.Duration
		minD := mc.recoveries[0]
		maxD := mc.recoveries[0]
		for _, d := range mc.recoveries {
			total += d
			if d < minD {
				minD = d
			}
			if d > maxD {
				maxD = d
			}
		}
		snap.AvgRecoveryTime = total / time.Duration(len(mc.recoveries))
		snap.MinRecoveryTime = minD
		snap.MaxRecoveryTime = maxD
		snap.LastRecoveryTime = mc.recoveries[len(mc.recoveries)-1]
	}

	// Action type breakdown. Return nil when empty for cleaner JSON output.
	if len(mc.actionCounts) > 0 {
		snap.ActionStats = make(map[string]ActionMetric, len(mc.actionCounts))
		for at, am := range mc.actionCounts {
			var avgDur time.Duration
			if am.success > 0 {
				avgDur = am.totalDuration / time.Duration(am.success)
			}
			snap.ActionStats[string(at)] = ActionMetric{
				Total:       am.total,
				Success:     am.success,
				Failed:      am.failed,
				AvgDuration: avgDur,
			}
		}
	}

	// Consistency rate: average of all samples.
	if len(mc.consistencySamples) > 0 {
		var sum float64
		for _, r := range mc.consistencySamples {
			sum += r
		}
		snap.DataConsistencyRate = sum / float64(len(mc.consistencySamples))
	}

	// Declare what was never measured (see Unmeasured for why this is part of the payload).
	if len(mc.recoveries) == 0 {
		snap.Unmeasured = append(snap.Unmeasured, "recovery_timing")
	}
	if len(mc.consistencySamples) == 0 {
		snap.Unmeasured = append(snap.Unmeasured, "data_consistency")
	}

	return snap
}

// recoveryMetricsAdapter bridges the recovery path to MetricsCollector, so
// recovery timing feeds into the arena metrics without aresrecovery importing
// arena. The adapter structurally implements aresrecovery.RecoveryObserver
// (OnRecovery(time.Duration)); the wiring happens in cmd/ares where both
// packages are visible.
type recoveryMetricsAdapter struct{ mc *MetricsCollector }

// OnRecovery records one recovery duration sample. Called synchronously from
// the recovery path — cheap (append + arithmetic).
func (a *recoveryMetricsAdapter) OnRecovery(d time.Duration) {
	a.mc.RecordRecovery(d)
}

// NewRecoveryAdapter returns an object implementing
// aresrecovery.RecoveryObserver. It is the single bridge between the recovery
// path and the metrics collector: cmd/ares passes the return value to
// recovery.WithRecoveryObserver, and the recovery path calls OnRecovery on
// each successful revival.
func NewRecoveryAdapter(mc *MetricsCollector) *recoveryMetricsAdapter {
	return &recoveryMetricsAdapter{mc: mc}
}

// Reset clears all collected metrics.
func (mc *MetricsCollector) Reset() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.recoveries = make([]time.Duration, 0)
	mc.actionCounts = make(map[ActionType]*actionMetricInternal)
	mc.consistencySamples = make([]float64, 0)
}

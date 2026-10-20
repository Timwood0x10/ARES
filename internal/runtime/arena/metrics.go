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

// minConsistencySamples is the sample count at which DataConsistencyRate is
// published. One successful decode is 100%, which would let a single read
// dominate the score; below this threshold the snapshot reports the sample
// count but leaves the rate unmeasured. The threshold is also the boundary
// calcConsistency uses to decide whether the rate (including a real 0%) is
// trustworthy.
const minConsistencySamples = 20

// maxConsistencySamples bounds the consistency window. Decodes happen far more
// often than recoveries (every task read-back), so the cap keeps Snapshot's
// O(n) scan bounded and makes the rate a rolling recent window.
const maxConsistencySamples = 1000

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
	// ConsistencySamples is the number of checkpoint decodes in the current
	// window. The rate is published only once this reaches
	// minConsistencySamples; below that the rate is 0 and data_consistency is
	// listed in Unmeasured. A zero rate WITH enough samples is a real 0%, not
	// a missing measurement.
	ConsistencySamples int `json:"consistency_samples"`

	// Unmeasured names the fields above that NO producer filled, so a zero in them means
	// "never measured" rather than "measured as zero".
	//
	// Recovery timing has a producer (the P3 recovery observer feeds RecordRecovery),
	// so it clears as soon as the first revival lands. Data consistency has a producer
	// too (the P4 checkpoint read-back observer feeds RecordConsistency), but it stays
	// listed until the window reaches minConsistencySamples — one decode is not a rate.
	// The list exists so an operator (and a reviewer) can tell a fallback from a real
	// measurement — the silent-degradation shape this codebase keeps closing.
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
	consistencySamples []bool
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
// fields it feeds (TotalRecoveries, Avg/Min/Max/LastRecoveryTime), together
// with the consistency window, are read by the chaos panel's RecoveryMetrics
// (via kernel.arenaMetrics.Snapshot(), wired in cmd/ares).
//
// They are NOT read by CalculateScore: the arena score takes its average
// recovery time from scenario-run events (calculateAvgRecoveryTime) and reads
// only DataConsistencyRate off a *MetricsSnapshot* — and from the arena
// Service's own collector, a separate process/instance from this one. That
// service has no task-fabric checkpoint stream, so its consistency rate stays
// honestly unmeasured; the score's corrected 0%-handling is covered by
// TestCalculateScore_ConsistencyZeroIsMeasured and takes effect wherever a
// real decode stream feeds that collector. The seam that once promised to
// bridge the two (Service.RecoveryObserver) was removed as dead surface;
// NewRecoveryAdapter is the only bridge and is wired to kernel.arenaMetrics.
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

// RecordConsistency records one checkpoint read-back outcome.
//
// This is the production entry point for data consistency: the task fabric's
// CheckpointDecodeObserver (arena.NewConsistencyAdapter) calls it once per
// Fabric.DecodeCheckpoint, so the sample is "persisted checkpoint was read
// back and decoded", not a guessed rate. The snapshot publishes the ratio only
// after minConsistencySamples; until then the field stays in Unmeasured.
//
// The samples slice is bounded by maxConsistencySamples with FIFO eviction,
// matching the recovery window's memory discipline.
func (mc *MetricsCollector) RecordConsistency(ok bool) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if len(mc.consistencySamples) >= maxConsistencySamples {
		mc.consistencySamples = mc.consistencySamples[1:]
	}
	mc.consistencySamples = append(mc.consistencySamples, ok)
}

// ConsistencyMeasured reports whether the snapshot's data-consistency rate is
// backed by at least minConsistencySamples decodes. It is the explicit
// "measured zero vs unmeasured zero" bit for consumers that render the rate
// (the chaos panel); calcConsistency applies the same threshold.
func (s MetricsSnapshot) ConsistencyMeasured() bool {
	return s.ConsistencySamples >= minConsistencySamples
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

	// Consistency: successes / decodes in the current window. A rate is only
	// published once the window is large enough to mean something; below the
	// threshold the sample count still rides the snapshot so an operator can
	// see the producer is alive without the score trusting one decode.
	snap.ConsistencySamples = len(mc.consistencySamples)
	if snap.ConsistencySamples >= minConsistencySamples {
		var successes int
		for _, ok := range mc.consistencySamples {
			if ok {
				successes++
			}
		}
		snap.DataConsistencyRate = float64(successes) / float64(snap.ConsistencySamples) * 100
	}

	// Declare what was never measured (see Unmeasured for why this is part of the payload).
	if len(mc.recoveries) == 0 {
		snap.Unmeasured = append(snap.Unmeasured, "recovery_timing")
	}
	if snap.ConsistencySamples < minConsistencySamples {
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
// aresrecovery.RecoveryObserver. It is the SOLE production entry point bridging
// the recovery path to the metrics collector: cmd/ares passes the return value
// to recovery.WithRecoveryObserver, and the recovery path calls OnRecovery on
// each successful revival. arena.Service deliberately exposes no second
// accessor, so this wiring stays single-path (code_rules §5.1).
func NewRecoveryAdapter(mc *MetricsCollector) *recoveryMetricsAdapter {
	return &recoveryMetricsAdapter{mc: mc}
}

// consistencyMetricsAdapter bridges checkpoint read-back outcomes to
// MetricsCollector. It structurally implements taskfabric.CheckpointDecodeObserver
// (OnCheckpointDecode(bool)); the wiring happens in cmd/ares where both
// packages are visible, so arena does not import the fabric.
type consistencyMetricsAdapter struct{ mc *MetricsCollector }

// OnCheckpointDecode records one decode outcome. Called synchronously from the
// fabric read path — cheap (append + optional FIFO eviction).
func (a *consistencyMetricsAdapter) OnCheckpointDecode(ok bool) {
	a.mc.RecordConsistency(ok)
}

// NewConsistencyAdapter returns an object implementing
// taskfabric.CheckpointDecodeObserver. It is the sole production bridge from
// the fabric's checkpoint read-back path to the arena metrics collector:
// cmd/ares passes it to Fabric.WithConsistencyObserver, and the fabric calls
// it once per observed decode. Keep this single-path (code_rules §5.1).
func NewConsistencyAdapter(mc *MetricsCollector) *consistencyMetricsAdapter {
	return &consistencyMetricsAdapter{mc: mc}
}

// Reset clears all collected metrics.
func (mc *MetricsCollector) Reset() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.recoveries = make([]time.Duration, 0)
	mc.actionCounts = make(map[ActionType]*actionMetricInternal)
	mc.consistencySamples = make([]bool, 0)
}

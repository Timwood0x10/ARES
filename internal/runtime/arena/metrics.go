package arena

import (
	"sync"
	"time"
)

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
	// This exists because the score reads these fields: recovery timing and data consistency
	// are consumed by CalculateScore/calcConsistency, yet nothing in production calls
	// RecordRecovery or RecordConsistency. Without this list an operator (and a reviewer)
	// sees `data_consistency_rate: 0` and cannot tell a perfect-ish fallback from a real
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
// Deprecated: Use RecordActionResult instead. No production caller remains
// (the service records per-action results via RecordActionResult; recovery
// timing derives from action events), so this is test-only surface.
// CONSEQUENCE BEFORE ANYONE DELETES THIS: the snapshot fields it feeds
// (TotalRecoveries, AvgRecoveryTime, MinRecoveryTime, MaxRecoveryTime, LastRecoveryTime) are
// read by CalculateScore. Nothing in production calls this method, so in a real deployment
// those fields are ALWAYS ZERO and the score's recovery component is permanently on its
// fallback. Deleting the method without addressing the score would silently remove the
// evidence that the component is unmeasured — fix the score first, then remove this.
//
// TODO(tech-debt): remove RecordRecovery together with its metrics_test.go
// coverage once no test needs the raw recovery-timeseries path.
func (mc *MetricsCollector) RecordRecovery(d time.Duration) {
	log.Warn("MetricsCollector.RecordRecovery is deprecated, use RecordActionResult instead; no production caller remains (test-only)")
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.recoveries = append(mc.recoveries, d)
}

// RecordConsistency records a data consistency rate sample (0-100).
//
// Deprecated: Use RecordActionResult instead. No production caller remains
// (action results are recorded via RecordActionResult), so this is test-only
// surface.
// CONSEQUENCE BEFORE ANYONE DELETES THIS: DataConsistencyRate is read by
// calcConsistency, which falls back to a heuristic whenever it is zero. Nothing in production
// calls this method, so that field is ALWAYS ZERO and every deployment is scored by the
// heuristic while the metrics snapshot still reports a "rate" field. Removing this method (and
// the field) is only honest together with making the fallback explicit in the score.
//
// TODO(tech-debt): remove RecordConsistency and the DataConsistencyRate
// snapshot field once no test needs the legacy consistency-timeseries path.
func (mc *MetricsCollector) RecordConsistency(rate float64) {
	log.Warn("MetricsCollector.RecordConsistency is deprecated, use RecordActionResult instead; no production caller remains (test-only)")
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

// Reset clears all collected metrics.
func (mc *MetricsCollector) Reset() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.recoveries = make([]time.Duration, 0)
	mc.actionCounts = make(map[ActionType]*actionMetricInternal)
	mc.consistencySamples = make([]float64, 0)
}

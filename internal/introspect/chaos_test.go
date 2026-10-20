package introspect

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/kernel"
)

// TestChaosReporterSnapshot verifies the reporter merges config, shadow and
// live state into one coherent frame.
func TestChaosReporterSnapshot(t *testing.T) {
	r := NewChaosReporter()
	// Not yet configured: everything zero.
	cs := r.Snapshot()
	if cs.Enabled || cs.Mode != "" || !cs.Shadow.LastRun.IsZero() || cs.Live.Injections != 0 {
		t.Fatalf("initial snapshot = %+v", cs)
	}

	r.SetConfig(true, "shadow")
	r.RecordShadow(ShadowResult{LastRun: time.Unix(10, 0), Events: 6, Recovered: true})
	cs = r.Snapshot()
	if !cs.Enabled || cs.Mode != "shadow" {
		t.Fatalf("config not recorded: %+v", cs)
	}
	if !cs.Shadow.Recovered || cs.Shadow.Events != 6 || cs.Shadow.LastRun.Unix() != 10 {
		t.Fatalf("shadow not recorded: %+v", cs.Shadow)
	}

	// Live flags + counter merge independently: AddInjection must not clobber
	// the flags written by SetLive, and the counter must accumulate.
	r.SetLive(LiveChaosState{Active: true, FailSafeTripped: true})
	r.AddInjection(time.Unix(20, 0))
	r.AddInjection(time.Unix(21, 0))
	cs = r.Snapshot()
	if !cs.Live.Active || !cs.Live.FailSafeTripped {
		t.Fatalf("live flags clobbered: %+v", cs.Live)
	}
	if cs.Live.Injections != 2 || cs.Live.LastInjection.Unix() != 21 {
		t.Fatalf("live counter/last: %+v", cs.Live)
	}
}

// TestChaosReporterConcurrent hammers the reporter from multiple goroutines to
// lock the atomic-write contract under -race (chaos loops report while the
// collector reads).
func TestChaosReporterConcurrent(t *testing.T) {
	r := NewChaosReporter()
	r.SetConfig(true, "live")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				r.SetLive(LiveChaosState{Active: true})
				r.RecordShadow(ShadowResult{Recovered: true, LastRun: time.Now()})
				r.AddInjection(time.Now())
				_ = r.Snapshot()
			}
		}()
	}
	wg.Wait()

	cs := r.Snapshot()
	if cs.Live.Injections != 8*500 {
		t.Fatalf("injections = %d, want %d", cs.Live.Injections, 8*500)
	}
}

// TestChaosReporterRecoveryMetricsSurvivesFlagFrames locks the anti-clobber
// contract for RecoveryMetrics. Regression: it used to be written through
// SetLive, whose whole-struct Store meant any later flag-only frame (GA-pause,
// fail-safe, loop stop) zeroed it until the next injection. SetRecoveryMetrics
// keeps it in its own atomic, so the panel no longer flickers.
func TestChaosReporterRecoveryMetricsSurvivesFlagFrames(t *testing.T) {
	r := NewChaosReporter()
	r.SetLive(LiveChaosState{Active: true})
	r.SetRecoveryMetrics(&RecoveryMetrics{
		TotalRecoveries:     3,
		AvgRecoveryTime:     2 * time.Second,
		DataConsistencyRate: 95,
		ConsistencySamples:  20,
		ConsistencyMeasured: true,
	})

	// A GA-pause frame writes only flags — it must not wipe the metrics.
	r.SetLive(LiveChaosState{Active: true, PausedForGA: true})

	cs := r.Snapshot()
	if cs.Live.RecoveryMetrics == nil || cs.Live.RecoveryMetrics.TotalRecoveries != 3 {
		t.Fatalf("recovery metrics clobbered by a flag-only SetLive: %+v", cs.Live.RecoveryMetrics)
	}
	if !cs.Live.RecoveryMetrics.ConsistencyMeasured || cs.Live.RecoveryMetrics.DataConsistencyRate != 95 {
		t.Fatalf("consistency metrics clobbered by a flag-only SetLive: %+v", cs.Live.RecoveryMetrics)
	}
	if !cs.Live.PausedForGA {
		t.Fatalf("GA-pause flag lost: %+v", cs.Live)
	}
}

// TestRecoveryMetricsJSONShapeAndBelowThresholdSignal locks the panel wire
// contract: recovery_metrics is omitted until populated, and when populated it
// always carries all three consistency fields. A below-threshold window is
// emitted as samples>0 / measured=false / rate=0 — the "producer is alive but
// the number is not publishable" signal the panel must render as not measured.
func TestRecoveryMetricsJSONShapeAndBelowThresholdSignal(t *testing.T) {
	r := NewChaosReporter()

	raw, err := json.Marshal(r.Snapshot())
	if err != nil {
		t.Fatalf("marshal empty snapshot: %v", err)
	}
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("unmarshal empty snapshot: %v", err)
	}
	live, ok := frame["live"].(map[string]any)
	if !ok {
		t.Fatalf("live frame missing: %s", raw)
	}
	if _, exists := live["recovery_metrics"]; exists {
		t.Fatalf("nil recovery metrics must be omitted: %s", raw)
	}

	r.SetRecoveryMetrics(&RecoveryMetrics{
		TotalRecoveries:     2,
		AvgRecoveryTime:     250 * time.Millisecond,
		MinRecoveryTime:     100 * time.Millisecond,
		MaxRecoveryTime:     500 * time.Millisecond,
		LastRecoveryTime:    250 * time.Millisecond,
		DataConsistencyRate: 0, // below the publication threshold
		ConsistencySamples:  19,
		ConsistencyMeasured: false,
	})

	raw, err = json.Marshal(r.Snapshot())
	if err != nil {
		t.Fatalf("marshal populated snapshot: %v", err)
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("unmarshal populated snapshot: %v", err)
	}
	live, ok = frame["live"].(map[string]any)
	if !ok {
		t.Fatalf("live frame missing: %s", raw)
	}
	metrics, ok := live["recovery_metrics"].(map[string]any)
	if !ok {
		t.Fatalf("recovery_metrics missing when populated: %s", raw)
	}

	for _, key := range []string{
		"total_recoveries",
		"avg_recovery_time",
		"min_recovery_time",
		"max_recovery_time",
		"last_recovery_time",
		"data_consistency_rate",
		"consistency_samples",
		"consistency_measured",
	} {
		if _, exists := metrics[key]; !exists {
			t.Errorf("recovery_metrics missing %q: %s", key, raw)
		}
	}
	if got := metrics["total_recoveries"].(float64); got != 2 {
		t.Errorf("total_recoveries = %v, want 2", got)
	}
	if got := metrics["data_consistency_rate"].(float64); got != 0 {
		t.Errorf("data_consistency_rate = %v, want 0", got)
	}
	if got := metrics["consistency_samples"].(float64); got != 19 {
		t.Errorf("consistency_samples = %v, want 19", got)
	}
	if got := metrics["consistency_measured"].(bool); got {
		t.Error("consistency_measured = true below threshold, want false")
	}
}

// TestCollectorChaosSource verifies the chaos source lands in the snapshot
// when present and is omitted when nil.
func TestCollectorChaosSource(t *testing.T) {
	withChaos := NewCollector(Sources{
		Kernel: func() kernel.SchedulerSnapshot { return kernel.SchedulerSnapshot{} },
		Fabric: func() []taskfabric.LeaseEntry { return nil },
		Chaos:  func() ChaosStatus { return ChaosStatus{Enabled: true, Mode: "shadow"} },
	})
	snap := withChaos.Collect()
	if snap.Chaos == nil || !snap.Chaos.Enabled || snap.Chaos.Mode != "shadow" {
		t.Fatalf("chaos domain not mapped: %+v", snap.Chaos)
	}

	noChaos := NewCollector(Sources{
		Kernel: func() kernel.SchedulerSnapshot { return kernel.SchedulerSnapshot{} },
		Fabric: func() []taskfabric.LeaseEntry { return nil },
	})
	if noChaos.Collect().Chaos != nil {
		t.Fatal("nil chaos source must omit the field")
	}
}

// TestRunShadowSandboxRecovers verifies the shadow sandbox replay records a
// recovery outcome. The scratch-fabric chain kill→lease-expire→recover must
// leave the task recovered, not errored. (Moved from dashboard_test.go
// with runShadowSandbox; the Dashboard runtime is gone.)
func TestRunShadowSandboxRecovers(t *testing.T) {
	res := runShadowSandbox(context.Background())
	if res.Errored {
		t.Fatalf("shadow sandbox errored: %+v", res)
	}
	if !res.Recovered {
		t.Fatalf("shadow sandbox did not recover: %+v", res)
	}
	if res.LastRun.IsZero() {
		t.Fatalf("shadow sandbox did not stamp LastRun: %+v", res)
	}
}

// TestDashboardChaosReporterWired verifies the chaos reporter feeds the
// collector Sources, so shadow-loop RecordShadow output stays observable
// (regression guard, kept from dashboard_test.go).
func TestDashboardChaosReporterWired(t *testing.T) {
	r := NewChaosReporter()
	r.SetConfig(true, "shadow")
	c := NewCollector(Sources{Chaos: r.Snapshot})
	r.RecordShadow(ShadowResult{LastRun: time.Now(), Recovered: true})
	snap := c.Collect()
	if snap.Chaos == nil || !snap.Chaos.Enabled || !snap.Chaos.Shadow.Recovered {
		t.Fatalf("chaos source not wired into collector: %+v", snap.Chaos)
	}
}

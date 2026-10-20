package arena

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewMetricsCollector(t *testing.T) {
	mc := NewMetricsCollector()
	assert.NotNil(t, mc)
	snap := mc.Snapshot()
	assert.Equal(t, 0, snap.TotalRecoveries)
	assert.Nil(t, snap.ActionStats)
	assert.Equal(t, time.Duration(0), snap.AvgRecoveryTime)
}

func TestRecordRecovery_Single(t *testing.T) {
	mc := NewMetricsCollector()
	mc.RecordRecovery(500 * time.Millisecond)

	snap := mc.Snapshot()
	assert.Equal(t, 1, snap.TotalRecoveries)
	assert.Equal(t, 500*time.Millisecond, snap.AvgRecoveryTime)
	assert.Equal(t, 500*time.Millisecond, snap.MinRecoveryTime)
	assert.Equal(t, 500*time.Millisecond, snap.MaxRecoveryTime)
	assert.Equal(t, 500*time.Millisecond, snap.LastRecoveryTime)
}

func TestRecordRecovery_Multiple_AvgMinMax(t *testing.T) {
	mc := NewMetricsCollector()
	durations := []time.Duration{
		200 * time.Millisecond,
		800 * time.Millisecond,
		1 * time.Second,
		400 * time.Millisecond,
	}
	for _, d := range durations {
		mc.RecordRecovery(d)
	}

	snap := mc.Snapshot()
	assert.Equal(t, 4, snap.TotalRecoveries)

	// Avg = (200+800+1000+400)/4 = 600ms
	expectedAvg := (200 + 800 + 1000 + 400) * time.Millisecond / 4
	assert.Equal(t, expectedAvg, snap.AvgRecoveryTime)
	assert.Equal(t, 200*time.Millisecond, snap.MinRecoveryTime)
	assert.Equal(t, 1*time.Second, snap.MaxRecoveryTime)
	assert.Equal(t, 400*time.Millisecond, snap.LastRecoveryTime)
}

func TestRecordActionResult_PerTypeBreakdown(t *testing.T) {
	mc := NewMetricsCollector()

	// Record multiple action types.
	mc.RecordActionResult(ActionKillAgent, true, 100*time.Millisecond)
	mc.RecordActionResult(ActionKillAgent, false, 50*time.Millisecond)
	mc.RecordActionResult(ActionKillLeader, true, 200*time.Millisecond)
	mc.RecordActionResult(ActionKillAgent, true, 80*time.Millisecond)

	snap := mc.Snapshot()

	// Verify kill_agent stats.
	ka, ok := snap.ActionStats["kill_agent"]
	assert.True(t, ok)
	assert.Equal(t, 3, ka.Total)
	assert.Equal(t, 2, ka.Success)
	assert.Equal(t, 1, ka.Failed)
	// Avg duration for successful kill_agent actions: (100+80)/2 = 90ms
	assert.Equal(t, 90*time.Millisecond, ka.AvgDuration)

	// Verify kill_leader stats.
	kl, ok := snap.ActionStats["kill_leader"]
	assert.True(t, ok)
	assert.Equal(t, 1, kl.Total)
	assert.Equal(t, 1, kl.Success)
	assert.Equal(t, 0, kl.Failed)
	assert.Equal(t, 200*time.Millisecond, kl.AvgDuration)
}

func TestSnapshot_AllFieldsPopulated(t *testing.T) {
	mc := NewMetricsCollector()
	mc.RecordRecovery(300 * time.Millisecond)
	mc.RecordRecovery(700 * time.Millisecond)
	mc.RecordActionResult(ActionPauseAgent, true, 50*time.Millisecond)
	for i := 0; i < minConsistencySamples; i++ {
		mc.RecordConsistency(i >= 2) // 18 successes / 20 = 90%
	}

	snap := mc.Snapshot()

	// Recovery timing.
	assert.Equal(t, 2, snap.TotalRecoveries)
	assert.Equal(t, 500*time.Millisecond, snap.AvgRecoveryTime) // (300+700)/2

	// Action stats.
	am, ok := snap.ActionStats["pause_agent"]
	assert.True(t, ok)
	assert.Equal(t, 1, am.Total)
	assert.Equal(t, 1, am.Success)

	// Consistency rate: 18 of 20 decodes succeeded.
	assert.Equal(t, minConsistencySamples, snap.ConsistencySamples)
	assert.InDelta(t, 90.0, snap.DataConsistencyRate, 0.01)
}

func TestReset_Cleared(t *testing.T) {
	mc := NewMetricsCollector()
	mc.RecordRecovery(1 * time.Second)
	mc.RecordActionResult(ActionKillAgent, true, 100*time.Millisecond)
	mc.RecordConsistency(true)

	// Verify data exists before reset.
	snapBefore := mc.Snapshot()
	assert.Greater(t, snapBefore.TotalRecoveries, 0)

	// Reset and verify cleared.
	mc.Reset()
	snapAfter := mc.Snapshot()

	assert.Equal(t, 0, snapAfter.TotalRecoveries)
	assert.Nil(t, snapAfter.ActionStats)
	assert.Equal(t, 0.0, snapAfter.DataConsistencyRate)
	assert.Equal(t, 0, snapAfter.ConsistencySamples)
	assert.Equal(t, time.Duration(0), snapAfter.AvgRecoveryTime)
}

func TestConcurrentAccess(t *testing.T) {
	mc := NewMetricsCollector()
	var wg sync.WaitGroup

	const goroutines = 20
	const opsPerGoroutine = 50

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				switch j % 5 {
				case 0:
					mc.RecordRecovery(time.Duration(j+1) * time.Millisecond)
				case 1:
				case 2:
					mc.RecordActionResult(ActionKillAgent, j%3 != 0, time.Duration(j)*time.Millisecond)
				case 3:
					mc.RecordConsistency(j%2 == 0)
				default:
					_ = mc.Snapshot() // read while others write
				}
			}
		}(i)
	}
	wg.Wait()

	// Verify no corruption - snapshot should be consistent.
	snap := mc.Snapshot()
	recoveryOps := goroutines * (opsPerGoroutine / 5) // approx
	assert.GreaterOrEqual(t, snap.TotalRecoveries, recoveryOps-1)
	assert.LessOrEqual(t, snap.TotalRecoveries, recoveryOps+1)
	assert.NotNil(t, snap.ActionStats)
	assert.Greater(t, snap.ConsistencySamples, 0)
}

// TestRecordConsistency_ThresholdAndZeroRate covers the two cases the old
// rate-only API could not express: one sample is not a rate, and a full window
// of failures is a real 0%, not "unmeasured".
func TestRecordConsistency_ThresholdAndZeroRate(t *testing.T) {
	mc := NewMetricsCollector()
	mc.RecordConsistency(true)

	snap := mc.Snapshot()
	assert.Equal(t, 1, snap.ConsistencySamples)
	assert.Equal(t, 0.0, snap.DataConsistencyRate, "below the threshold no rate is published")
	assert.Contains(t, snap.Unmeasured, "data_consistency")

	for i := 1; i < minConsistencySamples; i++ {
		mc.RecordConsistency(false)
	}
	snap = mc.Snapshot()
	assert.Equal(t, minConsistencySamples, snap.ConsistencySamples)
	assert.Equal(t, 5.0, snap.DataConsistencyRate, "1 success / 20 decodes")
	assert.NotContains(t, snap.Unmeasured, "data_consistency")

	allFail := NewMetricsCollector()
	for i := 0; i < minConsistencySamples; i++ {
		allFail.RecordConsistency(false)
	}
	snap = allFail.Snapshot()
	assert.Equal(t, minConsistencySamples, snap.ConsistencySamples)
	assert.Equal(t, 0.0, snap.DataConsistencyRate)
	assert.NotContains(t, snap.Unmeasured, "data_consistency",
		"a measured 0% must not be reported as unmeasured")
}

func TestRecordRecovery_EmptyCollector(t *testing.T) {
	mc := NewMetricsCollector()
	snap := mc.Snapshot()

	assert.Equal(t, 0, snap.TotalRecoveries)
	assert.Equal(t, time.Duration(0), snap.AvgRecoveryTime)
	assert.Equal(t, time.Duration(0), snap.MinRecoveryTime)
	assert.Equal(t, time.Duration(0), snap.MaxRecoveryTime)
}

// TestSnapshot_DeclaresUnmeasuredInputs is the fix for the silent fallback:
// both score inputs have producers, but each stays "unmeasured" until its
// producer has produced something trustworthy (a first recovery, a full
// consistency window). A zero that means "never measured" must not be
// indistinguishable from a measured zero.
func TestSnapshot_DeclaresUnmeasuredInputs(t *testing.T) {
	mc := NewMetricsCollector()

	snap := mc.Snapshot()
	assert.ElementsMatch(t, []string{"recovery_timing", "data_consistency"}, snap.Unmeasured,
		"a fresh collector has measured neither input")

	// Both producers run to their publication threshold.
	mc.RecordRecovery(250 * time.Millisecond)
	for i := 0; i < minConsistencySamples; i++ {
		mc.RecordConsistency(true)
	}
	snap = mc.Snapshot()
	assert.Empty(t, snap.Unmeasured, "measured inputs must not be listed as unmeasured")
}

// TestRecoveryAdapter_DataReachesSnapshot is the end-to-end proof that
// recovery timing written through the adapter (the same path the recovery
// observer uses in production) is readable from Snapshot — the consumer
// side of the P3 observer. Without this test the observer could write
// into a black hole (as the initial P3 did with a standalone collector
// that nobody read).
func TestRecoveryAdapter_DataReachesSnapshot(t *testing.T) {
	mc := NewMetricsCollector()
	adapter := NewRecoveryAdapter(mc)

	// Simulate two recovery events through the observer interface —
	// the same interface aresrecovery.RecoveryObserver uses.
	adapter.OnRecovery(100 * time.Millisecond)
	adapter.OnRecovery(300 * time.Millisecond)

	// The consumer reads the snapshot.
	snap := mc.Snapshot()
	assert.Equal(t, 2, snap.TotalRecoveries, "both recoveries must be counted")
	assert.Equal(t, 200*time.Millisecond, snap.AvgRecoveryTime, "avg = (100+300)/2")
	assert.Equal(t, 100*time.Millisecond, snap.MinRecoveryTime)
	assert.Equal(t, 300*time.Millisecond, snap.MaxRecoveryTime)
	assert.Equal(t, 300*time.Millisecond, snap.LastRecoveryTime)

	// recovery_timing must NOT be in Unmeasured — it has a producer now.
	assert.NotContains(t, snap.Unmeasured, "recovery_timing",
		"recovery_timing has a producer; it must not be Unmeasured")
}

// TestRecoveryAdapter_FIFOEviction verifies that the recoveries slice is
// bounded: after maxRecoverySamples, the oldest sample is evicted so a
// long-running process does not accumulate unbounded memory.
func TestRecoveryAdapter_FIFOEviction(t *testing.T) {
	mc := NewMetricsCollector()
	adapter := NewRecoveryAdapter(mc)

	// Fill to the cap.
	for i := 0; i < maxRecoverySamples; i++ {
		adapter.OnRecovery(time.Duration(i) * time.Millisecond)
	}
	snap := mc.Snapshot()
	assert.Equal(t, maxRecoverySamples, snap.TotalRecoveries,
		"TotalRecoveries = cap (no eviction yet)")

	// One more — the oldest (0ms) must be evicted.
	adapter.OnRecovery(999 * time.Millisecond)
	snap = mc.Snapshot()
	assert.Equal(t, maxRecoverySamples, snap.TotalRecoveries,
		"TotalRecoveries stays at cap after eviction (not cap+1)")
	assert.Equal(t, 999*time.Millisecond, snap.LastRecoveryTime,
		"the newest sample is the one just added")
	// Min should be 1ms (the second sample), not 0ms (the evicted oldest).
	assert.Equal(t, 1*time.Millisecond, snap.MinRecoveryTime,
		"the oldest sample (0ms) was evicted; min is now 1ms")
}

// TestConsistencyAdapter_DataReachesSnapshot is the end-to-end proof that the
// fabric's decode observer (structurally the same OnCheckpointDecode contract
// taskfabric.CheckpointDecodeObserver uses) reaches Snapshot: below threshold
// with the count visible, at threshold with the rate published.
func TestConsistencyAdapter_DataReachesSnapshot(t *testing.T) {
	mc := NewMetricsCollector()
	adapter := NewConsistencyAdapter(mc)

	adapter.OnCheckpointDecode(true)
	snap := mc.Snapshot()
	assert.Equal(t, 1, snap.ConsistencySamples)
	assert.Equal(t, 0.0, snap.DataConsistencyRate, "one decode is not a rate")
	assert.False(t, snap.ConsistencyMeasured())
	assert.Contains(t, snap.Unmeasured, "data_consistency")

	for i := 1; i < minConsistencySamples; i++ {
		adapter.OnCheckpointDecode(i != minConsistencySamples-1)
	}
	snap = mc.Snapshot()
	assert.Equal(t, minConsistencySamples, snap.ConsistencySamples)
	assert.InDelta(t, 95.0, snap.DataConsistencyRate, 0.01, "19 successes / 20 decodes")
	assert.True(t, snap.ConsistencyMeasured())
	assert.NotContains(t, snap.Unmeasured, "data_consistency")
}

// TestConsistencyAdapter_AllSuccessIs100 covers the acceptance case "at the
// threshold and all successful": the rate is exactly 100 and the field is no
// longer listed as unmeasured.
func TestConsistencyAdapter_AllSuccessIs100(t *testing.T) {
	mc := NewMetricsCollector()
	adapter := NewConsistencyAdapter(mc)
	for i := 0; i < minConsistencySamples; i++ {
		adapter.OnCheckpointDecode(true)
	}
	snap := mc.Snapshot()
	assert.Equal(t, 100.0, snap.DataConsistencyRate)
	assert.True(t, snap.ConsistencyMeasured())
	assert.NotContains(t, snap.Unmeasured, "data_consistency")
}

// TestConsistencyAdapter_FIFOEviction bounds the decode window: a long-running
// process must not accumulate one bool per decode forever.
func TestConsistencyAdapter_FIFOEviction(t *testing.T) {
	mc := NewMetricsCollector()
	adapter := NewConsistencyAdapter(mc)

	for i := 0; i < maxConsistencySamples; i++ {
		adapter.OnCheckpointDecode(false)
	}
	// Evict the oldest failures with successes: the published rate must move.
	for i := 0; i < maxConsistencySamples/2; i++ {
		adapter.OnCheckpointDecode(true)
	}
	snap := mc.Snapshot()
	assert.Equal(t, maxConsistencySamples, snap.ConsistencySamples)
	assert.InDelta(t, 50.0, snap.DataConsistencyRate, 0.01,
		"half the window was evicted by successful decodes")
}

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
	mc.RecordConsistency(95.0)
	mc.RecordConsistency(85.0)

	snap := mc.Snapshot()

	// Recovery timing.
	assert.Equal(t, 2, snap.TotalRecoveries)
	assert.Equal(t, 500*time.Millisecond, snap.AvgRecoveryTime) // (300+700)/2

	// Action stats.
	am, ok := snap.ActionStats["pause_agent"]
	assert.True(t, ok)
	assert.Equal(t, 1, am.Total)
	assert.Equal(t, 1, am.Success)

	// Consistency rate: average of samples.
	assert.InDelta(t, 90.0, snap.DataConsistencyRate, 0.01) // (95+85)/2
}

func TestReset_Cleared(t *testing.T) {
	mc := NewMetricsCollector()
	mc.RecordRecovery(1 * time.Second)
	mc.RecordActionResult(ActionKillAgent, true, 100*time.Millisecond)
	mc.RecordConsistency(90.0)

	// Verify data exists before reset.
	snapBefore := mc.Snapshot()
	assert.Greater(t, snapBefore.TotalRecoveries, 0)

	// Reset and verify cleared.
	mc.Reset()
	snapAfter := mc.Snapshot()

	assert.Equal(t, 0, snapAfter.TotalRecoveries)
	assert.Nil(t, snapAfter.ActionStats)
	assert.Equal(t, 0.0, snapAfter.DataConsistencyRate)
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
					mc.RecordConsistency(float64(50 + j%50))
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
}

func TestRecordConsistency_MultipleSamples(t *testing.T) {
	mc := NewMetricsCollector()
	mc.RecordConsistency(100.0)
	mc.RecordConsistency(90.0)
	mc.RecordConsistency(80.0)

	snap := mc.Snapshot()
	assert.InDelta(t, 90.0, snap.DataConsistencyRate, 0.01) // (100+90+80)/3
}

func TestRecordRecovery_EmptyCollector(t *testing.T) {
	mc := NewMetricsCollector()
	snap := mc.Snapshot()

	assert.Equal(t, 0, snap.TotalRecoveries)
	assert.Equal(t, time.Duration(0), snap.AvgRecoveryTime)
	assert.Equal(t, time.Duration(0), snap.MinRecoveryTime)
	assert.Equal(t, time.Duration(0), snap.MaxRecoveryTime)
}

// TestSnapshot_DeclaresUnmeasuredInputs is the fix for the silent fallback: two snapshot fields
// are read by CalculateScore but have no production producer, so they are always zero. A zero
// that means "never measured" must not be indistinguishable from a measured zero.
func TestSnapshot_DeclaresUnmeasuredInputs(t *testing.T) {
	mc := NewMetricsCollector()

	snap := mc.Snapshot()
	assert.ElementsMatch(t, []string{"recovery_timing", "data_consistency"}, snap.Unmeasured,
		"a fresh collector has measured neither input")

	// Once a producer runs, the field stops being reported as unmeasured.
	mc.RecordRecovery(250 * time.Millisecond)
	mc.RecordConsistency(90)
	snap = mc.Snapshot()
	assert.Empty(t, snap.Unmeasured, "measured inputs must not be listed as unmeasured")
}

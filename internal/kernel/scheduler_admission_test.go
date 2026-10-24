package kernel

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// recordingSchedulerMetrics captures the three scheduling observations.
type recordingSchedulerMetrics struct {
	mu       sync.Mutex
	dispatch []float64
	ages     []float64
	gaps     []float64
}

func (r *recordingSchedulerMetrics) RecordReadyToDispatch(s float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dispatch = append(r.dispatch, s)
}

func (r *recordingSchedulerMetrics) RecordWorkConservingGap(s float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gaps = append(r.gaps, s)
}

func (r *recordingSchedulerMetrics) RecordReadyAge(s float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ages = append(r.ages, s)
}

func (r *recordingSchedulerMetrics) snapshot() (int, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.dispatch), len(r.ages), len(r.gaps)
}

// TestAdmitQuantum_BudgetIsProcessWide pins the M2 admission contract: the limit is a total, not
// a per-pump allowance.
//
// Before the pump stopped waiting for its batch, one drain ran at a time and a per-drain
// semaphore was accidentally a global cap. Once pumps overlap, a per-pump budget lets N pumps
// each admit N quanta, so WithMaxConcurrent stops being a cap — this test is the thing that would
// notice if the semaphore came back.
func TestAdmitQuantum_BudgetIsProcessWide(t *testing.T) {
	s := &Scheduler{}

	require.True(t, s.admitQuantum(2), "first slot")
	require.True(t, s.admitQuantum(2), "second slot")
	assert.False(t, s.admitQuantum(2), "third must be refused: the budget is shared, not per drain")

	s.releaseQuantum()
	assert.True(t, s.admitQuantum(2), "a released slot must be reusable")
}

// TestAdmitQuantum_RefusesInsteadOfBlocking is the difference between the pump and the batch
// barrier it replaced: admission must ANSWER, never park the scheduling loop behind a slow
// quantum. A blocking implementation cannot return at all here, so the assertion is that the call
// returns false rather than that it is fast (timing assertions are flaky by construction).
func TestAdmitQuantum_RefusesInsteadOfBlocking(t *testing.T) {
	s := &Scheduler{}
	require.True(t, s.admitQuantum(1))

	// Called inline on purpose: a blocking implementation would deadlock here and fail the test
	// by timeout, which is exactly the regression this guards. A "fast enough" timing assertion
	// would be flaky instead.
	assert.False(t, s.admitQuantum(1))
}

// TestObserveWorkConservingGap_OnlyWhenWorkWasDeferred: the gap is the "slow quantum blocked new
// work" signal, so it must not be recorded when nothing had to wait.
func TestObserveWorkConservingGap_OnlyWhenWorkWasDeferred(t *testing.T) {
	m := &recordingSchedulerMetrics{}
	s := &Scheduler{metrics: m}
	require.True(t, s.admitQuantum(2), "a running quantum is what the gap is measured against")

	s.observeWorkConservingGap(0)
	_, _, gaps := m.snapshot()
	assert.Equal(t, 0, gaps, "no deferred work means no gap")

	s.observeWorkConservingGap(3)
	_, _, gaps = m.snapshot()
	assert.Equal(t, 1, gaps, "deferred work behind a running quantum is the case the metric exists for")
}

// TestObserveDispatch_RecordsBothLatencyAndAge: work that RAN contributes to both, because
// ready_to_dispatch is its latency while ready_age is the queue-age sample the fairness signal
// needs.
func TestObserveDispatch_RecordsBothLatencyAndAge(t *testing.T) {
	m := &recordingSchedulerMetrics{}
	f := taskfabric.NewFabric()
	require.NoError(t, f.Create(&taskfabric.Task{ID: "t-1"}))
	s := &Scheduler{fabric: f, metrics: m}

	s.observeDispatch("t-1")

	dispatched, ages, _ := m.snapshot()
	assert.Equal(t, 1, dispatched)
	assert.Equal(t, 1, ages)
}

// TestMetricsOf_ZeroValueSchedulerIsSafe: a nil INTERFACE is not a nil receiver, so calling a
// method on an unset sink panics. The constructor installs a no-op, but a zero-value Scheduler
// (tests, future call sites) must not be able to crash the drain loop.
func TestMetricsOf_ZeroValueSchedulerIsSafe(t *testing.T) {
	s := &Scheduler{}
	require.NotPanics(t, func() {
		s.metricsOf().RecordReadyAge(1)
		s.metricsOf().RecordReadyToDispatch(1)
		s.metricsOf().RecordWorkConservingGap(1)
	})
}

package kernel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// gapRecorder captures work-conserving gaps and signals each one, so a test can wait for the
// observation instead of sleeping for a fixed period.
type gapRecorder struct {
	mu   sync.Mutex
	gaps []float64
	seen chan float64
}

func newGapRecorder() *gapRecorder {
	return &gapRecorder{seen: make(chan float64, 4)}
}

func (g *gapRecorder) RecordReadyToDispatch(float64) {}
func (g *gapRecorder) RecordReadyAge(float64)        {}

func (g *gapRecorder) RecordWorkConservingGap(seconds float64) {
	g.mu.Lock()
	g.gaps = append(g.gaps, seconds)
	g.mu.Unlock()
	select {
	case g.seen <- seconds:
	default:
	}
}

func (g *gapRecorder) all() []float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]float64(nil), g.gaps...)
}

// blockingOneShotExecutor blocks its quantum until released, so the scheduler has exactly one busy
// agent and any further READY work must wait behind it.
type blockingOneShotExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (e *blockingOneShotExecutor) ID() string             { return "gap-agent" }
func (e *blockingOneShotExecutor) Type() models.AgentType { return "cap" }

func (e *blockingOneShotExecutor) ExecuteStep(ctx context.Context, _ *models.Task) (*sub.StepOutcome, error) {
	select {
	case e.started <- struct{}{}:
	default:
	}
	select {
	case <-e.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &sub.StepOutcome{Done: true}, nil
}

// TestScheduler_WorkConservingGapReachesTheOrderOfTheBlock: the milestone's acceptance says a slow
// quantum must show up as a gap of the same order as its duration. A test that only asserted "one
// gap was recorded" would pass for a gap of microseconds, which is exactly what a mis-wired clock
// (or a per-pass timer reset on every drain) would produce.
//
// The quantum is held for ~600ms and the second task is submitted afterwards, so the recorded gap
// must be at least half of that. Half rather than exact: the drain that observes it runs on the
// scheduler's own tick, so the sample is the elapsed time at its next look, never less than the
// block but not required to match it to the millisecond.
func TestScheduler_WorkConservingGapReachesTheOrderOfTheBlock(t *testing.T) {
	fabric := taskfabric.NewFabric()
	exec := &blockingOneShotExecutor{started: make(chan struct{}, 1), release: make(chan struct{})}
	rec := newGapRecorder()

	sched := New(fabric, map[string]CapabilityExecutor{"gap-agent": exec}, NewLoadTracker()).
		WithMaxConcurrent(1). // one slot: the second task cannot be admitted
		WithSchedulerMetrics(rec)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		close(exec.release)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("scheduler did not stop after cancellation")
		}
	})

	require.NoError(t, fabric.Create(&taskfabric.Task{ID: "blocking", Capability: "cap"}))
	select {
	case <-exec.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the blocking quantum never started")
	}

	// Let the quantum run long enough that the gap has a measurable order.
	<-time.After(600 * time.Millisecond)

	require.NoError(t, fabric.Create(&taskfabric.Task{ID: "waiting", Capability: "cap"}))

	var gap float64
	select {
	case gap = <-rec.seen:
	case <-time.After(5 * time.Second):
		t.Fatalf("no work-conserving gap was recorded; gaps so far: %v", rec.all())
	}

	assert.GreaterOrEqual(t, gap, 0.5,
		"the gap must reflect how long the in-flight quantum has been running, not a per-drain timer")
}

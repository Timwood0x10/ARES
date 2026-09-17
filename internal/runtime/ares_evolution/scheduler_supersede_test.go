// scheduler_supersede_test.go locks REVIEW 3.4#8: OnAgentEnd's cancel and
// re-arm were two separate evolveMu critical sections, so two concurrent
// OnAgentEnd calls could both cancel the old cycle and then both arm their
// own — leaving two evolution cycles running concurrently (the first never
// cancelled, never waited on). The cancel+arm is now one critical section:
// each new cycle deterministically supersedes the previous one.
package evolution

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// blockingSupersedeAdapter blocks in Run until its gate opens or the ctx is
// cancelled. It detects GENUINE concurrent-active cycles: when a new Run
// starts, any still-live earlier Run whose ctx has not been cancelled yet
// means two uncancelled evolution cycles overlap. (A cancelled-but-draining
// earlier run is fine — the supersede contract is cancel-before-arm.)
type blockingSupersedeAdapter struct {
	gate    chan struct{}
	mu      sync.Mutex
	live    map[int]context.Context
	nextID  int
	started atomic.Int32
	// activeOverlap counts Runs that started while another UNCANCELLED Run
	// was still alive.
	activeOverlap atomic.Int32
}

func newBlockingSupersedeAdapter() *blockingSupersedeAdapter {
	return &blockingSupersedeAdapter{gate: make(chan struct{}), live: map[int]context.Context{}}
}

func (a *blockingSupersedeAdapter) Run(ctx context.Context) error {
	a.started.Add(1)
	a.mu.Lock()
	id := a.nextID
	a.nextID++
	a.live[id] = ctx
	// A genuine concurrent-active overlap needs BOTH runs uncancelled: a
	// superseded cycle whose goroutine enters late (already-cancelled ctx)
	// is just a zombie draining, not a second active cycle.
	if ctx.Err() == nil {
		for _, other := range a.live {
			if other != ctx && other.Err() == nil {
				a.activeOverlap.Add(1)
			}
		}
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.live, id)
		a.mu.Unlock()
	}()

	select {
	case <-a.gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestOnAgentEndSupersedesConcurrentCycles(t *testing.T) {
	adapter := newBlockingSupersedeAdapter()
	scheduler := NewEvolutionScheduler(nil, adapter,
		WithEnabled(true),
		WithTrigger(TriggerOnIdle),
		WithMinInterval(time.Nanosecond),
	)

	// Make shouldEvolve fire: TriggerOnIdle requires recent degradation.
	for i := 0; i < 40; i++ {
		scheduler.RecordScore(1.0)
	}
	for i := 0; i < 10; i++ {
		scheduler.RecordScore(0.0)
	}

	// Fire several OnAgentEnd calls concurrently while the adapter blocks:
	// with the two-critical-section bug, more than one Run could be alive
	// at the same time.
	const callers = 8
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scheduler.OnAgentEnd(context.Background(), CallbackData{AgentID: "supersede"})
		}()
	}

	// Wait until at least two cycles have been started, then open the gate
	// so cancelled runs return and the survivor finishes.
	deadline := time.Now().Add(2 * time.Second)
	for adapter.started.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(adapter.gate)
	wg.Wait()
	scheduler.Shutdown()

	require.Equal(t, int32(0), adapter.activeOverlap.Load(),
		"concurrent OnAgentEnd calls must never leave two uncancelled evolution cycles running at once")
}

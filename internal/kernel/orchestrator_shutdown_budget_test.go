package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShutdown_RespectsCallerDeadlineWhileDraining is RT-04: the errgroup drain used to be bounded
// only by a fixed 30s timer, so a caller that asked for 25ms was answered hundreds of milliseconds
// later by a shutdown that claimed to honour a deadline it never read.
//
// The background loop here deliberately ignores its context — the point is not that cooperative
// loops stop (they do), but that an UNCOOPERATIVE one cannot extend the caller's budget.
func TestShutdown_RespectsCallerDeadlineWhileDraining(t *testing.T) {
	reg := NewRegistry()
	orc := NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	orc.Go(func() error {
		<-release // ignores the orchestrator's context on purpose
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := orc.Shutdown(ctx)
	elapsed := time.Since(start)

	require.Error(t, err, "a shutdown that ran out of budget must not report success")
	assert.ErrorIs(t, err, context.DeadlineExceeded,
		"the caller must be able to tell 'ran out of time' from 'a component failed'")
	assert.Less(t, elapsed, 2*time.Second,
		"shutdown waited %s: the drain must end with the caller's budget, not the 30s backstop", elapsed)
}

// TestShutdown_NoDeadlineStillBounded keeps the other half of the contract: a caller that supplies no
// deadline still gets a bounded wait. The 30s cap cannot be waited out in a test, so this asserts the
// cheap part — a cooperative loop finishes and the call succeeds.
func TestShutdown_NoDeadlineStillBounded(t *testing.T) {
	reg := NewRegistry()
	orc := NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	done := make(chan struct{})
	orc.Go(func() error {
		<-orc.RootContext().Done()
		close(done)
		return nil
	})

	require.NoError(t, orc.Shutdown(context.Background()), "a cooperative drain must report success")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the managed goroutine did not observe cancellation")
	}
}

// TestShutdown_AlreadyCancelledContextFailsFast: a caller that cancels before calling Shutdown must
// not get a success report while an uncooperative loop is still running. "Everything closed" is a
// claim, and a cancelled budget is the one case where it cannot be made.
func TestShutdown_AlreadyCancelledContextFailsFast(t *testing.T) {
	reg := NewRegistry()
	orc := NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	orc.Go(func() error {
		<-release
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := orc.Shutdown(ctx)

	require.Error(t, err, "an already-cancelled budget must not be reported as a clean shutdown")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 2*time.Second)
}

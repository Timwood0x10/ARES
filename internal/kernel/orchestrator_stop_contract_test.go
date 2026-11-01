package kernel

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShutdown_WaitErrorIsNotReportedAsStopped: a component whose Wait() reports failure has not
// stopped, and the status is the only place an operator can see that. The error used to be logged and
// then overwritten by an unconditional StateStopped, and stopComponent returned nil — so Shutdown
// reported success for a teardown that failed.
func TestShutdown_WaitErrorIsNotReportedAsStopped(t *testing.T) {
	sentinel := errors.New("watcher exited with a pending flush")

	reg := NewRegistry()
	comp := &lifecycleComp{name: "a", waitErr: sentinel}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	orc := NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	err := orc.Shutdown(context.Background())

	require.Error(t, err, "a failed Wait must not be reported as a clean shutdown")
	assert.ErrorIs(t, err, sentinel, "the component's own error must stay reachable")

	status, _ := reg.GetStatus("a")
	assert.NotEqual(t, StateStopped, status.State,
		"a component that failed to stop must not be recorded as Stopped")
}

// TestShutdown_WaitHungComponentIsNotReportedAsStopped covers the timeout path: the component ignored
// shutdown, the caller's budget expired, and the old code still wrote StateStopped.
func TestShutdown_WaitHungComponentIsNotReportedAsStopped(t *testing.T) {
	reg := NewRegistry()
	comp := &lifecycleComp{name: "a", blockWait: true}
	requireNoErr(t, reg.Register(comp, ModeRequired))
	t.Cleanup(comp.releaseWait)

	orc := NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := orc.Shutdown(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded,
		"the report must say the budget ran out, not blame the component")

	status, _ := reg.GetStatus("a")
	assert.NotEqual(t, StateStopped, status.State,
		"a component still running past the budget must not be recorded as Stopped")
}

// TestShutdown_NoDeadlineIsBoundedByTheBackstop is the case the previous test could not reach: with
// NO caller deadline the drain is bounded by waitTimeout (30s), which no ordinary test can sit
// through. synctest advances the fake clock, so the bound is asserted instead of assumed.
func TestShutdown_NoDeadlineIsBoundedByTheBackstop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := NewRegistry()
		orc := NewOrchestrator(reg, context.Background())
		require.NoError(t, orc.Start(context.Background()))

		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		orc.Go(func() error {
			<-release // ignores cancellation: the backstop is the only thing that ends the wait
			return nil
		})

		var shutdownErr error
		done := make(chan struct{})
		go func() {
			shutdownErr = orc.Shutdown(context.Background())
			close(done)
		}()

		synctest.Wait() // the shutdown is now parked on the drain
		time.Sleep(waitTimeout + time.Second)
		synctest.Wait()

		select {
		case <-done:
		default:
			t.Fatal("Shutdown was still waiting past waitTimeout with no caller deadline")
		}
		require.Error(t, shutdownErr, "a drain that outlived the backstop is not a clean shutdown")
		assert.Contains(t, shutdownErr.Error(), "timed out",
			"the error must name the backstop so the reader knows which clock expired")
	})
}

package kernel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShutdown_StopThatIgnoresContextIsBounded: Stop used to be called inline, so a component that
// ignored its context held the whole shutdown open past the caller's budget. The orchestrator must
// bound the call itself and report the component as not stopped.
func TestShutdown_StopThatIgnoresContextIsBounded(t *testing.T) {
	reg := NewRegistry()
	comp := &lifecycleComp{name: "writer", blockStop: true}
	requireNoErr(t, reg.Register(comp, ModeRequired))
	t.Cleanup(comp.releaseStop)

	orc := NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := orc.Shutdown(ctx)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not return",
		"the report must say the component ignored the call, not that it stopped")
	assert.Less(t, elapsed, 2*time.Second,
		"Shutdown waited %s: an uncooperative Stop must not outlive the caller's budget", elapsed)

	status, _ := reg.GetStatus("writer")
	assert.NotEqual(t, StateStopped, status.State)
}

// TestShutdown_DuplicateCallSharesTheResult: a second caller used to get nil immediately because the
// first had merely STARTED. "Another goroutine is doing it" is not a result — the second caller has to
// learn how it ended.
func TestShutdown_DuplicateCallSharesTheResult(t *testing.T) {
	reg := NewRegistry()
	comp := &lifecycleComp{name: "a", blockWait: true, waitErr: assert.AnError}
	requireNoErr(t, reg.Register(comp, ModeRequired))
	t.Cleanup(comp.releaseWait)

	orc := NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	firstDone := make(chan error, 1)
	go func() { firstDone <- orc.Shutdown(context.Background()) }()
	time.Sleep(20 * time.Millisecond) // let the first call enter the drain

	// Second caller with its own short budget: it must not report success while the first is running.
	secondCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	secondErr := orc.Shutdown(secondCtx)
	require.Error(t, secondErr, "a concurrent Shutdown returned nil while the first was still draining")
	assert.Contains(t, secondErr.Error(), "still in progress")

	comp.releaseWait()
	firstErr := <-firstDone
	require.Error(t, firstErr, "the first call reports the component that never exited")

	// A third call must replay the first result, not invent a success.
	thirdErr := orc.Shutdown(context.Background())
	require.Error(t, thirdErr)
	assert.Equal(t, firstErr.Error(), thirdErr.Error(),
		"a completed shutdown must return its recorded result to every later caller")
}

// TestShutdown_DoesNotStopDependenciesOfAFailedComponent: writer (depends on store) fails to exit, so
// closing store underneath it would trade a visible failure for silent corruption. The walk must stop
// and say which components were left running.
func TestShutdown_DoesNotStopDependenciesOfAFailedComponent(t *testing.T) {
	reg := NewRegistry()
	store := &lifecycleComp{name: "store"}
	writer := &lifecycleComp{name: "writer", deps: []string{"store"}, waitErr: assert.AnError}
	requireNoErr(t, reg.Register(store, ModeRequired))
	requireNoErr(t, reg.Register(writer, ModeRequired))

	orc := NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	err := orc.Shutdown(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "left running",
		"the report must name what was deliberately not torn down")
	assert.Contains(t, err.Error(), "store")

	storeStatus, _ := reg.GetStatus("store")
	assert.NotEqual(t, StateStopped, storeStatus.State,
		"a store must not be closed while the writer that depends on it is still alive")

	writerStatus, _ := reg.GetStatus("writer")
	assert.NotEqual(t, StateStopped, writerStatus.State)
	assert.NotEmpty(t, strings.TrimSpace(writerStatus.Reason))
}

// TestShutdown_StillStopsIndependentComponents is the correction to a fix that went too far: stopping
// the whole walk when one component failed to exit left unrelated components running. Only the failed
// component's dependency chain is unsafe to close; a cache nobody depends on can still be stopped.
func TestShutdown_StillStopsIndependentComponents(t *testing.T) {
	reg := NewRegistry()
	store := &lifecycleComp{name: "store"}
	writer := &lifecycleComp{name: "writer", deps: []string{"store"}, waitErr: assert.AnError}
	unrelated := &lifecycleComp{name: "cache"}
	requireNoErr(t, reg.Register(store, ModeRequired))
	requireNoErr(t, reg.Register(writer, ModeRequired))
	requireNoErr(t, reg.Register(unrelated, ModeRequired))

	orc := NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	err := orc.Shutdown(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store", "the protected dependency must be named")

	storeStatus, _ := reg.GetStatus("store")
	assert.NotEqual(t, StateStopped, storeStatus.State, "the writer's dependency must stay up")

	cacheStatus, _ := reg.GetStatus("cache")
	assert.Equal(t, StateStopped, cacheStatus.State,
		"an unrelated component must still be stopped: a failed teardown is not a reason to leave it running")
}

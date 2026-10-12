package agentfabric

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBusyBridgeExcludesFromSchedulable verifies the R4 busy bridge: an IDLE
// agent is schedulable (IsIdle true) until SetBusy(true) marks it executing a
// quantum, after which IsIdle reports false even though the lifecycle state is
// still StateIdle. Clearing busy restores schedulability.
func TestBusyBridgeExcludesFromSchedulable(t *testing.T) {
	f := NewFabric()
	a, err := f.Spawn(context.Background(), SpawnSpec{
		Identity: "a", Capabilities: []string{"rust"},
	})
	require.NoError(t, err)
	require.Equal(t, StateIdle, a.State)

	assert.True(t, f.IsIdle("a"), "idle, not busy → schedulable")

	a.SetBusy(true)
	assert.True(t, a.IsBusy())
	assert.False(t, f.IsIdle("a"), "busy agent must not be schedulable")
	assert.Equal(t, StateIdle, a.State, "busy is an execution flag, not a lifecycle state")

	a.SetBusy(false)
	assert.False(t, a.IsBusy())
	assert.True(t, f.IsIdle("a"), "cleared busy → schedulable again")
}

// TestBusyBridgeConcurrentAccessIsRaceFree exercises SetBusy/IsBusy/IsIdle
// from concurrent goroutines so `go test -race` proves the busy flag is
// guarded. No time.Sleep: goroutines are joined with a WaitGroup.
func TestBusyBridgeConcurrentAccessIsRaceFree(t *testing.T) {
	f := NewFabric()
	_, err := f.Spawn(context.Background(), SpawnSpec{
		Identity: "a", Capabilities: []string{"rust"},
	})
	require.NoError(t, err)
	a, err := f.Get("a")
	require.NoError(t, err)

	const workers = 8
	const iters = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				if id%2 == 0 {
					a.SetBusy(j%2 == 0)
				} else {
					_ = a.IsBusy()
					_ = f.IsIdle("a")
				}
			}
		}(i)
	}
	wg.Wait()
}

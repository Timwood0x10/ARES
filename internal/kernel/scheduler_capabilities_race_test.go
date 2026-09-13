package kernel

import (
	"context"
	"fmt"
	"sync"
	"testing"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestSchedulerCapabilitiesReadRacesAddCapabilities locks the read side of
// the hot tool-registration path against the fabric's capability mutation.
//
// AddCapabilities appends to the live agent's Capabilities slice under the
// fabric lock (the mutation half of SDK hot tool registration). Two kernel
// sites read that same slice field AFTER agents.Get returned — Get releases
// the fabric lock on the way out — so a concurrent append and an
// unsynchronized read are a data race on the slice header:
//
//   - Scheduler.Capabilities (executor_registry.go), which the graph
//     submission endpoint uses to reject unroutable requests
//   - freeCapableAgents (scheduler_dispatch.go), which sizes the drain limit
//
// The agent fabric already documents the hazard on CapabilitiesOf ("Get
// hands back the live pointer… would race") and provides the locked copy
// accessor precisely for this; these two call sites simply did not use it.
//
// Run with -race: detecting the race is the whole point of the test. Without
// the fix the race detector reports a concurrent read/write on
// Agent.Capabilities.
func TestSchedulerCapabilitiesReadRacesAddCapabilities(t *testing.T) {
	ctx := context.Background()
	const iterations = 500

	agents := agentfabric.NewFabric()
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:     "peer",
		Capabilities: []string{"ares/plan"},
	}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	sched := New(taskfabric.NewFabric(), map[string]CapabilityExecutor{}, NewLoadTracker())
	sched.WithAgentFabric(agents)

	var wg sync.WaitGroup

	// Writer: the SDK's hot tool registration path.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if err := agents.AddCapabilities("peer", fmt.Sprintf("tool/gen-%d", i)); err != nil {
				t.Errorf("AddCapabilities: %v", err)
				return
			}
		}
	}()

	// Readers: both kernel sites that walk the live agent's capabilities.
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = sched.Capabilities()
				_ = sched.freeCapableAgents([]string{"ares/plan"}, nil)
			}
		}()
	}

	wg.Wait()
}

package kernel

import (
	"context"
	"fmt"
	"testing"
	"time"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// BenchmarkL2GraphGrowthChain64 measures end-to-end growth throughput: 64
// tool nodes added as a burst through the live seam (graph events →
// planprojection compile → scheduler → session agent), waiting for full
// convergence. The metric intentionally includes the scheduler's poll
// cadence (serial chain × 2ms is the product's real steady-state
// throughput); benchstat deltas on this number expose per-node overhead
// changes — exactly what a growth-path regression moves.
func BenchmarkL2GraphGrowthChain64(b *testing.B) {
	const n = 64
	const nodeTimeout = 30 * time.Second

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		ctx, cancel := context.WithCancel(context.Background())
		fabric := taskfabric.NewFabric()
		coord := planprojection.NewCompileCoordinator(fabric, nil)

		plan, err := agentfabric.NewL2Graph("root", "bench-growth", nil)
		if err != nil {
			b.Fatalf("NewL2Graph: %v", err)
		}
		stop := coord.SubscribeGraphEvents(ctx, plan.DAG())

		admitSessionRoot(b, ctx, fabric, plan)

		agents := agentfabric.NewFabric()
		if err := spawnSessionAgent(ctx, agents, &echoBinder{}); err != nil {
			b.Fatalf("spawnSessionAgent: %v", err)
		}
		sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())
		sched.WithAgentFabric(agents)
		sched.PollInterval = 2 * time.Millisecond
		runDone := make(chan struct{})
		go func() {
			sched.Run(ctx)
			close(runDone)
		}()
		b.StartTimer()

		// Grow a serial chain of n tool nodes; each depends on its
		// predecessor, so completion order is deterministic.
		ids := make([]string, 0, n+1)
		ids = append(ids, "root")
		prev := "root"
		for t := 0; t < n; t++ {
			id := fmt.Sprintf("g%d", t)
			if err := plan.AddToolNode(ctx, id, "echo", map[string]any{"query": fmt.Sprintf("q%d", t)}, prev); err != nil {
				b.Fatalf("AddToolNode: %v", err)
			}
			ids = append(ids, id)
			prev = id
		}

		// Converge: poll until every task (root + n nodes) is COMPLETED.
		// The 2ms sleep matches the scheduler's poll cadence — the benchmark
		// measures the real pipeline, not a tight spin.
		deadline := time.Now().Add(nodeTimeout)
		for {
			allDone := true
			for _, id := range ids {
				task, err := fabric.Task(id)
				if err != nil || task.State != taskfabric.StateCompleted {
					allDone = false
					break
				}
			}
			if allDone {
				break
			}
			if time.Now().After(deadline) {
				b.Fatalf("growth chain did not converge within %s", nodeTimeout)
			}
			time.Sleep(2 * time.Millisecond)
		}

		b.StopTimer()
		stop()
		cancel()
		<-runDone
	}
}

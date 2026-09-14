// kernel — scheduler hot-path benchmarks (Phase 5 baseline,
// plan/stability_performance_plan.md). The drain benchmark isolates the
// scheduler's per-tick cost (scoring, lease, dispatch, bookkeeping) with
// synchronous executors, so LLM/executor latency cannot mask regressions.
package kernel

import (
	"context"
	"fmt"
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// benchDrainTasks builds a fabric holding n READY tasks for the bench
// capability.
func benchDrainTasks(b *testing.B, n int) *taskfabric.Fabric {
	b.Helper()
	fabric := taskfabric.NewFabric()
	for i := 0; i < n; i++ {
		if err := fabric.Create(&taskfabric.Task{
			ID:         fmt.Sprintf("bench-task-%d", i),
			Capability: "bench",
		}); err != nil {
			b.Fatalf("Create: %v", err)
		}
	}
	return fabric
}

// benchCountCompleted reports how many of the fabric's tasks are COMPLETED —
// the drain benchmark's sanity check that every iteration did real work.
func benchCountCompleted(b *testing.B, fabric *taskfabric.Fabric) int {
	b.Helper()
	completed := 0
	for _, id := range fabric.IDs() {
		task, err := fabric.Task(id)
		if err != nil {
			b.Fatalf("Task(%s): %v", id, err)
		}
		if task.State == taskfabric.StateCompleted {
			completed++
		}
	}
	return completed
}

// BenchmarkSchedulerDrain100Tasks measures one full drain pass over 100
// READY tasks with synchronous executors: candidate scoring, lease
// acquisition, quantum dispatch, tracker bookkeeping and durable state
// transitions — the scheduler's per-tick cost with executor latency removed.
// A regression here multiplies every poll interval in the system.
func BenchmarkSchedulerDrain100Tasks(b *testing.B) {
	const n = 100
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		fabric := benchDrainTasks(b, n)
		sched := New(fabric, map[string]CapabilityExecutor{
			"bench-exec": &probingExecutor{id: "bench-exec", typ: models.AgentType("bench")},
		}, NewLoadTracker())
		b.StartTimer()

		sched.drain(ctx)

		b.StopTimer()
		if got := benchCountCompleted(b, fabric); got != n {
			b.Fatalf("drain completed %d of %d tasks — benchmark harness broken", got, n)
		}
	}
}

// BenchmarkSchedulerDrainEmpty measures the idle drain pass (no READY
// tasks): the fixed cost every poll tick pays even when the queue is empty.
// This is the floor the PollInterval multiplies.
func BenchmarkSchedulerDrainEmpty(b *testing.B) {
	ctx := context.Background()
	fabric := taskfabric.NewFabric()
	sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sched.drain(ctx)
	}
}

package kernel

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the kernel package on leaked goroutines (Phase 1 leak
// program, plan/stability_performance_plan.md): the kernel spawns scheduler
// preemption sweeps, heartbeats and orchestrator waiters — every test must
// leave none of them behind.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

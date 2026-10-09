package kernel

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the kernel package on leaked goroutines (Phase 1 of the leak
// program): the kernel spawns scheduler
// preemption sweeps, heartbeats and orchestrator waiters — every test must
// leave none of them behind.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

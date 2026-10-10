package engine

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the engine package on leaked goroutines (Phase 1 of the leak
// program): workflow execution and the
// reloader's watch loops must not outlive their tests.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// Package advanced_test verifies that all advanced examples compile and use correct imports.
package advanced_test

import (
	"os/exec"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_events"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	runtime "github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/plugins/resurrection"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// exampleDirs lists all advanced example directories.
var exampleDirs = []string{
	"mutable_dag",
	"dynamic_executor",
	"leader_failover",
	"agent_resurrection",
	"runtime_resurrection",
	"full_lifecycle",
}

// TestExamples_Compile verifies all example packages compile successfully.
func TestExamples_Compile(t *testing.T) {
	t.Parallel()

	for _, dir := range exampleDirs {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command("go", "build", "-o", "/dev/null", "github.com/Timwood0x10/ares/examples/advanced/"+dir)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("example %s failed to compile: %v\n%s", dir, err, output)
			}
		})
	}
}

// TestMutableDAG_Example_Imports verifies that the MutableDAG example
// uses correct types from the workflow engine package.
func TestMutableDAG_Example_Imports(t *testing.T) {
	t.Parallel()

	// Verify MutableDAG type exists and is constructible.
	steps := []*engine.Step{
		{ID: "A", Name: "Step A"},
		{ID: "B", Name: "Step B", DependsOn: []string{"A"}},
	}
	dag, err := engine.NewMutableDAG(steps)
	if err != nil {
		t.Fatalf("NewMutableDAG failed: %v", err)
	}

	// Verify key methods exist.
	if dag.Version() != 0 {
		t.Errorf("expected version 0, got %d", dag.Version())
	}

	order, err := dag.GetExecutionOrder()
	if err != nil {
		t.Fatalf("GetExecutionOrder failed: %v", err)
	}
	if len(order) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(order))
	}

	_ = dag.Snapshot()
}

// TestDynamicExecutor_Example_Imports verifies that the DynamicExecutor example
// uses correct types from the workflow engine package.
func TestDynamicExecutor_Example_Imports(t *testing.T) {
	t.Parallel()

	// Verify DynamicExecutor type exists and accepts options.
	executor := engine.NewDynamicExecutor(nil, engine.ApplyAtCheckpoint)
	if executor == nil {
		t.Fatal("NewDynamicExecutor returned nil")
	}

	executorWithOpts := engine.NewDynamicExecutor(
		nil,
		engine.ApplyImmediate,
		engine.WithMaxParallel(5),
		engine.WithStepTimeout(60*1000*1000*1000),
	)
	if executorWithOpts == nil {
		t.Fatal("NewDynamicExecutor with options returned nil")
	}
}

// TestLeaderFailover_Example_Imports verifies that the leader_failover example
// uses correct types from the runtime and ares_events packages.
func TestLeaderFailover_Example_Imports(t *testing.T) {
	t.Parallel()

	// Verify Runtime constructor accepts EventStore.
	eventStore := ares_events.NewMemoryEventStore()
	rt := runtime.New(&runtime.Config{
		HealthCheckInterval: 100_000_000_000, // 100s (not started)
		MaxRestartsPerAgent: 3,
		MaxReplayEvents:     1000,
	}, eventStore, nil)
	if rt == nil {
		t.Fatal("runtime.New returned nil")
	}

	// Verify EventStore interface.
	var _ ares_events.EventStore = eventStore
}

// TestAgentResurrection_Example_Imports verifies that the agent_resurrection example
// uses correct types from the resurrection plugin and AHP packages.
func TestAgentResurrection_Example_Imports(t *testing.T) {
	t.Parallel()

	// Verify HeartbeatMonitor constructor.
	hbMon := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  2_000_000_000, // 2s
		Timeout:   3_000_000_000, // 3s
		MaxMissed: 2,
	})
	if hbMon == nil {
		t.Fatal("NewHeartbeatMonitor returned nil")
	}

	// Verify HeartbeatAdapter wraps HeartbeatMonitor.
	adapter := resurrection.NewHeartbeatAdapter(hbMon)
	if adapter == nil {
		t.Fatal("NewHeartbeatAdapter returned nil")
	}

	// Verify Supervisor constructor.
	sup, err := resurrection.New(adapter, resurrection.Config{
		CheckInterval:     3_000_000_000, // 3s
		HeartbeatInterval: 2_000_000_000, // 2s
	}, nil)
	if err != nil {
		t.Fatalf("resurrection.New failed: %v", err)
	}
	if sup == nil {
		t.Fatal("resurrection.New returned nil supervisor")
	}
}

// TestRuntimeResurrection_Example_Imports verifies that the runtime_resurrection example
// uses correct types from the runtime and ares_events packages.
func TestRuntimeResurrection_Example_Imports(t *testing.T) {
	t.Parallel()

	// Verify DefaultConfig exists.
	cfg := runtime.DefaultConfig()
	if cfg.HealthCheckInterval == 0 {
		t.Error("DefaultConfig HealthCheckInterval should not be zero")
	}

	// Verify Runtime with all three dependencies.
	eventStore := ares_events.NewMemoryEventStore()
	memMgr, err := memory.NewMemoryManager(memory.DefaultMemoryConfig())
	if err != nil {
		t.Fatalf("NewMemoryManager failed: %v", err)
	}
	rt := runtime.New(cfg, eventStore, memMgr)
	if rt == nil {
		t.Fatal("runtime.New returned nil")
	}

	// Verify StatefulAgent interface exists.
	var _ base.StatefulAgent //nolint:gosimple // interface check
}

// TestFullLifecycle_Example_Imports verifies that the full_lifecycle example
// uses correct types from all major packages.
func TestFullLifecycle_Example_Imports(t *testing.T) {
	t.Parallel()

	// Verify all agent types exist.
	_ = models.AgentTypeLeader
	_ = models.AgentTypeBottom
	_ = models.AgentTypeTop

	// Verify agent status constants.
	_ = models.AgentStatusReady
	_ = models.AgentStatusOffline

	// Verify event types used in examples.
	_ = ares_events.EventAgentStarted
	_ = ares_events.EventSessionCreated
	_ = ares_events.EventTaskCreated
	_ = ares_events.EventTaskCompleted
	_ = ares_events.EventTaskCompleted

	// Verify MemoryManager interface.
	var _ memory.MemoryManager //nolint:gosimple // interface check
}

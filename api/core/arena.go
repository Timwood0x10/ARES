// Package core provides core abstractions for ARES modules.
package core

import (
	"context"
	"time"
)

// ArenaConfig holds chaos engineering arena configuration.
type ArenaConfig struct {
	// Duration is the chaos test duration.
	Duration time.Duration
	// FaultTypes lists the fault injection types to use.
	FaultTypes []string
	// TargetIDs lists the target agent IDs.
	TargetIDs []string
	// ScenarioPath is the path to a YAML scenario file (optional).
	ScenarioPath string
}

// FaultType classifies fault injection methods.
type FaultType string

const (
	FaultKillLeader       FaultType = "kill_leader"
	FaultKillAgent        FaultType = "kill_agent"
	FaultRemoveNode       FaultType = "remove_node"
	FaultRemoveEdge       FaultType = "remove_edge"
	FaultPauseAgent       FaultType = "pause_agent"
	FaultResumeAgent      FaultType = "resume_agent"
	FaultSlowAgent        FaultType = "slow_agent"
	FaultKillOrchestrator FaultType = "kill_orchestrator"
	FaultNetworkPartition FaultType = "network_partition"
	FaultToolTimeout      FaultType = "tool_timeout"
	FaultMemoryCorrupt    FaultType = "memory_corrupt"
	FaultMCPDisconnect    FaultType = "mcp_disconnect"
	FaultLLMFailure       FaultType = "llm_failure"
)

// ResilienceScore holds the resilience evaluation result.
type ResilienceScore struct {
	// Overall is the overall resilience score (0-100).
	Overall float64
	// Recovery is the recovery capability score.
	Recovery float64
	// Stability is the stability score under fault conditions.
	Stability float64
	// Details provides per-fault-type breakdown.
	Details map[string]float64
}

// ArenaReport holds the full chaos test report.
type ArenaReport struct {
	// Score is the resilience evaluation.
	Score ResilienceScore
	// Events records all events during the test.
	Events []ArenaEvent
	// Duration is the actual test duration.
	Duration time.Duration
	// FaultsInjected is the number of faults injected.
	FaultsInjected int
	// AgentsRecovered is the number of agents that recovered.
	AgentsRecovered int
}

// ArenaEvent represents a single event during chaos testing.
type ArenaEvent struct {
	Timestamp time.Time
	Type      string
	Target    string
	Detail    string
}

// Arena defines the interface for chaos engineering operations.
type Arena interface {
	// InjectFault injects a specific fault into the system.
	InjectFault(ctx context.Context, faultType FaultType, targetID string) error

	// RunScenario runs a chaos scenario from a YAML configuration.
	RunScenario(ctx context.Context, scenarioPath string) (*ArenaReport, error)

	// RunRandom runs random fault injection for the configured duration.
	RunRandom(ctx context.Context, duration time.Duration) (*ArenaReport, error)

	// Score returns the current resilience score.
	Score() *ResilienceScore

	// ListAgents returns the list of agents currently under test.
	ListAgents() []string

	// Stop stops the arena and cleans up.
	Stop() error
}

package marketmaking

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// ChaosScenario defines a fault-injection scenario for testing system
// resilience under adverse conditions.
type ChaosScenario struct {
	// Name is the human-readable identifier for this scenario.
	Name string `json:"name"`
	// Type categorizes the failure mode (latency, reject, stale, partition).
	Type string `json:"type"`
	// Probability is the per-event injection probability between 0 and 1.
	Probability float64 `json:"probability"`
	// DurationMillis is how long this scenario should remain active.
	DurationMillis int64 `json:"duration_millis"`
}

// ChaosResult reports the outcome of a chaos test execution.
type ChaosResult struct {
	// Scenario is the scenario that was executed.
	Scenario *ChaosScenario `json:"scenario"`
	// TotalEvents is the number of events processed during the test.
	TotalEvents int64 `json:"total_events"`
	// InjectedFaults is the number of faults actually injected.
	InjectedFaults int64 `json:"injected_faults"`
	// SystemDegraded records whether the system entered a degraded state.
	SystemDegraded bool `json:"system_degraded"`
	// RecoveryTimeMillis measures how long the system took to recover.
	RecoveryTimeMillis int64 `json:"recovery_time_millis"`
}

// ChaosExecutor defines the interface for running chaos engineering tests.
type ChaosExecutor interface {
	// Execute runs a single chaos scenario and returns results.
	Execute(ctx context.Context, scenario *ChaosScenario) (*ChaosResult, error)
	// AvailableScenarios returns the list of supported scenario types.
	AvailableScenarios() []string
}

// DefaultChaosExecutor is a skeleton implementation of ChaosExecutor.
// It simulates fault injection based on probability without affecting real systems.
type DefaultChaosExecutor struct {
	rng *rand.Rand
}

// NewDefaultChaosExecutor creates a new chaos executor with a seeded RNG.
//
// Returns:
//
//	executor - a chaos executor instance (skeleton implementation).
func NewDefaultChaosExecutor() *DefaultChaosExecutor {
	return &DefaultChaosExecutor{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Execute runs a chaos scenario by injecting faults at the configured probability.
//
// This is a skeleton implementation that simulates fault injection events and
// returns a result. A full implementation would:
//
//  1. Intercept quote generation / order submission paths.
//  2. Based on scenario.Type, inject latency, rejections, or stale data.
//  3. Monitor system health metrics during the injection window.
//  4. Measure recovery time after the scenario ends.
//  5. Report whether the system degraded and how quickly it recovered.
//
// Args:
//
//	ctx - operation context supporting cancellation.
//	scenario - the fault-injection scenario to execute.
//
// Returns:
//
//	result - counts of injected faults and system health observations.
//	err - validation error or execution error.
func (e *DefaultChaosExecutor) Execute(ctx context.Context, scenario *ChaosScenario) (*ChaosResult, error) {
	if scenario == nil {
		return nil, errors.New("chaos scenario must not be nil")
	}
	if scenario.Probability < 0 || scenario.Probability > 1 {
		return nil, fmt.Errorf("probability must be between 0 and 1, got %f", scenario.Probability)
	}
	if scenario.DurationMillis <= 0 {
		return nil, errors.New("duration_millis must be > 0")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Simulate event processing over the scenario duration window.
	totalEvents := scenario.DurationMillis / 10 // assume ~100 events/second
	injectedFaults := int64(0)
	for i := int64(0); i < totalEvents; i++ {
		if e.rng.Float64() < scenario.Probability {
			injectedFaults++
		}
		select {
		case <-ctx.Done():
			return &ChaosResult{
				Scenario:       scenario,
				TotalEvents:    i,
				InjectedFaults: injectedFaults,
				SystemDegraded: injectedFaults > 0,
			}, ctx.Err()
		default:
		}
	}

	return &ChaosResult{
		Scenario:           scenario,
		TotalEvents:        totalEvents,
		InjectedFaults:     injectedFaults,
		SystemDegraded:     injectedFaults > totalEvents/2,
		RecoveryTimeMillis: 0,
	}, nil
}

// AvailableScenarios returns the list of scenario types this executor supports.
//
// Returns:
//
//	names - list of supported scenario type names.
func (e *DefaultChaosExecutor) AvailableScenarios() []string {
	return []string{"latency", "reject", "stale_data", "network_partition"}
}

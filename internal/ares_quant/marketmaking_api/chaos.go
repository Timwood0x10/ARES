package marketmakingapi

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
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
// It simulates fault injection based on probability without affecting real systems,
// and supports chaos feature flags to modulate injection behavior.
type DefaultChaosExecutor struct {
	mu    sync.Mutex
	rng   *rand.Rand
	flags ChaosFlagConfig
}

// NewDefaultChaosExecutor creates a new chaos executor with a seeded RNG.
// When no flags are provided, all chaos features are disabled by default
// for backward compatibility.
//
// Returns:
//
//	executor - a chaos executor instance (skeleton implementation).
func NewDefaultChaosExecutor() *DefaultChaosExecutor {
	return &DefaultChaosExecutor{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec G404
		flags: ChaosFlagConfig{
			EnableLatency:   false,
			EnableReject:    false,
			EnableStaleData: false,
		},
	}
}

// WithFlags sets the chaos feature flags on the executor and returns the
// same executor for chaining. This allows callers to enable specific fault
// injection modes after construction.
//
// Args:
//
//	flags - configuration controlling which chaos features are active.
//
// Returns:
//
//	e - the executor with updated flags for method chaining.
func (e *DefaultChaosExecutor) WithFlags(flags ChaosFlagConfig) *DefaultChaosExecutor {
	e.mu.Lock()
	e.flags = flags
	e.mu.Unlock()
	return e
}

// Execute runs a chaos scenario by injecting faults at the configured probability.
//
// This implementation simulates fault injection events and returns a result
// with calculated recovery times based on scenario type and active chaos flags.
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
	if err := e.validateScenario(scenario); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	e.mu.Lock()
	localFlags := e.flags
	e.mu.Unlock()

	totalEvents := scenario.DurationMillis / 10
	effectiveProb := e.effectiveProbability(scenario, localFlags)
	injectedFaults := e.injectFaults(ctx, totalEvents, effectiveProb)

	recoveryTime := e.calculateRecoveryTime(scenario, injectedFaults, localFlags)
	systemDegraded := e.isSystemDegraded(scenario, injectedFaults, totalEvents, localFlags)

	return &ChaosResult{
		Scenario:           scenario,
		TotalEvents:        totalEvents,
		InjectedFaults:     injectedFaults,
		SystemDegraded:     systemDegraded,
		RecoveryTimeMillis: recoveryTime,
	}, nil
}

// validateScenario checks that the scenario has valid fields.
//
// Args:
//
//	scenario - the scenario to validate.
//
// Returns:
//
//	err - validation error if any field is invalid, nil otherwise.
func (e *DefaultChaosExecutor) validateScenario(scenario *ChaosScenario) error {
	if scenario == nil {
		return errors.New("chaos scenario must not be nil")
	}
	if scenario.Probability < 0 || scenario.Probability > 1 {
		return fmt.Errorf("probability must be between 0 and 1, got %f", scenario.Probability)
	}
	if scenario.DurationMillis <= 0 {
		return errors.New("duration_millis must be > 0")
	}
	return nil
}

// effectiveProbability returns the adjusted injection probability based on
// scenario type and active chaos flags. When reject mode is enabled for
// reject scenarios, faults are more likely to occur.
//
// Args:
//
//	scenario - the scenario whose probability may be adjusted.
//
// Returns:
//
//	probability - the effective fault injection probability.
func (e *DefaultChaosExecutor) effectiveProbability(scenario *ChaosScenario, flags ChaosFlagConfig) float64 {
	baseProb := scenario.Probability
	if flags.EnableReject && scenario.Type == "reject" {
		// Increase probability by 20% when reject mode is active (capped at 1.0).
		adjusted := baseProb * 1.2
		if adjusted > 1.0 {
			return 1.0
		}
		return adjusted
	}
	return baseProb
}

// injectFaults simulates fault injection over the event window and returns
// the count of injected faults. Respects context cancellation.
//
// Args:
//
//	ctx - operation context supporting cancellation.
//	totalEvents - number of events to process.
//	probability - per-event fault injection probability.
//
// Returns:
//
//	faultCount - number of faults actually injected.
func (e *DefaultChaosExecutor) injectFaults(ctx context.Context, totalEvents int64, probability float64) int64 {
	faultCount := int64(0)
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := int64(0); i < totalEvents; i++ {
		if e.rng.Float64() < probability {
			faultCount++
		}
		select {
		case <-ctx.Done():
			return faultCount
		default:
		}
	}
	return faultCount
}

// calculateRecoveryTime computes estimated recovery time in milliseconds
// based on scenario type, duration, active flags, and fault count.
//
// Recovery time ratios by scenario type:
//   - latency: ~10% of duration (+5% overhead when flag enabled)
//   - reject: ~20% of duration (+10% overhead when flag enabled)
//   - stale_data: ~15% of duration (+5% overhead when flag enabled)
//   - network_partition: ~33% of duration (always significant)
//
// Args:
//
//	scenario - the executed scenario.
//	injectedFaults - number of faults injected during execution.
//
// Returns:
//
//	recoveryMillis - estimated recovery time in milliseconds.
func (e *DefaultChaosExecutor) calculateRecoveryTime(scenario *ChaosScenario, injectedFaults int64, flags ChaosFlagConfig) int64 {
	if injectedFaults == 0 {
		return 0
	}

	duration := scenario.DurationMillis
	switch scenario.Type {
	case "latency":
		base := duration / 10
		if flags.EnableLatency {
			return base + duration/20 // +5% overhead
		}
		return base

	case "reject":
		base := duration / 5
		if flags.EnableReject {
			return base + duration/10 // +10% overhead
		}
		return base

	case "stale_data":
		base := (duration * 15) / 100 // 15% using integer math
		if flags.EnableStaleData {
			return base + duration/20 // +5% overhead
		}
		return base

	case "network_partition":
		return duration / 3 // always significant recovery time

	default:
		// Unknown scenario type: use conservative 10% estimate.
		return duration / 10
	}
}

// isSystemDegraded determines whether the system entered a degraded state
// based on scenario type, fault count, event count, and active flags.
//
// Degradation thresholds by scenario type:
//   - stale_data with flag: degraded when >25% faults (more aggressive)
//   - network_partition: degraded if any faults were injected
//   - other types: degraded when >50% faults
//
// Args:
//
//	scenario - the executed scenario.
//	injectedFaults - number of faults injected.
//	totalEvents - total events processed.
//
// Returns:
//
//	degraded - true if system is considered degraded.
func (e *DefaultChaosExecutor) isSystemDegraded(scenario *ChaosScenario, injectedFaults int64, totalEvents int64, flags ChaosFlagConfig) bool {
	if totalEvents == 0 {
		return false
	}

	switch scenario.Type {
	case "stale_data":
		if flags.EnableStaleData {
			// More aggressive degradation threshold: >25% instead of 50%.
			return injectedFaults*4 > totalEvents
		}
		return injectedFaults > totalEvents/2

	case "network_partition":
		// Any fault injection causes degradation in partition scenarios.
		return injectedFaults > 0

	default:
		return injectedFaults > totalEvents/2
	}
}

// AvailableScenarios returns the list of scenario types this executor supports.
//
// Returns:
//
//	names - list of supported scenario type names.
func (e *DefaultChaosExecutor) AvailableScenarios() []string {
	return []string{"latency", "reject", "stale_data", "network_partition"}
}

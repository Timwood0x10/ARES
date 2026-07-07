package marketmakingapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewDefaultChaosExecutor tests constructor.
func TestNewDefaultChaosExecutor(t *testing.T) {
	executor := NewDefaultChaosExecutor()
	require.NotNil(t, executor)
}

// TestChaosExecutor_AvailableScenarios returns supported scenario types.
func TestChaosExecutor_AvailableScenarios(t *testing.T) {
	executor := NewDefaultChaosExecutor()
	scenarios := executor.AvailableScenarios()
	require.NotEmpty(t, scenarios)
	require.Contains(t, scenarios, FaultLatency)
	require.Contains(t, scenarios, FaultReject)
}

// TestChaosExecute_NilScenario tests nil scenario handling.
func TestChaosExecute_NilScenario(t *testing.T) {
	executor := NewDefaultChaosExecutor()
	result, err := executor.Execute(context.Background(), nil)
	require.Error(t, err)
	require.Nil(t, result)
}

// TestChaosExecute_InvalidProbability tests out-of-range probability.
func TestChaosExecute_InvalidProbability(t *testing.T) {
	executor := NewDefaultChaosExecutor()
	result, err := executor.Execute(context.Background(), &ChaosScenario{
		Name:           "bad-prob",
		Type:           FaultReject,
		Probability:    1.5,
		DurationMillis: 1000,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "probability")
	require.Nil(t, result)
}

// TestChaosExecute_ZeroDuration tests zero duration.
func TestChaosExecute_ZeroDuration(t *testing.T) {
	executor := NewDefaultChaosExecutor()
	result, err := executor.Execute(context.Background(), &ChaosScenario{
		Name:        "zero-dur",
		Type:        FaultLatency,
		Probability: 0.5,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "duration")
	require.Nil(t, result)
}

// TestChaosExecute_ValidScenario tests successful execution.
func TestChaosExecute_ValidScenario(t *testing.T) {
	executor := NewDefaultChaosExecutor()
	scenario := &ChaosScenario{
		Name:           "test-latency",
		Type:           FaultLatency,
		Probability:    0.0, // no faults expected
		DurationMillis: 100, // short duration for fast test
	}
	result, err := executor.Execute(context.Background(), scenario)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, scenario, result.Scenario)
	require.Equal(t, int64(0), result.InjectedFaults)
	require.False(t, result.SystemDegraded)
}

// TestChaosExecute_HighProbability tests with high fault injection rate.
func TestChaosExecute_HighProbability(t *testing.T) {
	executor := NewDefaultChaosExecutor()
	scenario := &ChaosScenario{
		Name:           "high-reject",
		Type:           FaultReject,
		Probability:    1.0, // all events should be faults
		DurationMillis: 50,
	}
	result, err := executor.Execute(context.Background(), scenario)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.InjectedFaults > 0)
	require.True(t, result.SystemDegraded)
}

// TestChaosExecute_CancelledContext tests context cancellation during execution.
func TestChaosExecute_CancelledContext(t *testing.T) {
	executor := NewDefaultChaosExecutor()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediate cancellation

	scenario := &ChaosScenario{
		Name:           "cancelled",
		Type:           FaultStaleData,
		Probability:    0.5,
		DurationMillis: 10000,
	}
	result, err := executor.Execute(ctx, scenario)
	require.ErrorIs(t, err, context.Canceled)
	// Result may be nil or partial depending on when cancellation occurred
	_ = result
}

// TestChaosExecutor_WithFlags tests the WithFlags method and flag-based behavior.
func TestChaosExecutor_WithFlags(t *testing.T) {
	executor := NewDefaultChaosExecutor()

	// Test WithFlags returns same executor for chaining.
	flags := ChaosFlagConfig{
		EnableLatency:   true,
		EnableReject:    true,
		EnableStaleData: true,
	}
	result := executor.WithFlags(flags)
	require.Same(t, executor, result)
}

// TestChaosExecute_LatencyWithFlagEnabled tests latency scenario with latency flag enabled.
func TestChaosExecute_LatencyWithFlagEnabled(t *testing.T) {
	executor := NewDefaultChaosExecutor().WithFlags(ChaosFlagConfig{
		EnableLatency: true,
	})
	scenario := &ChaosScenario{
		Name:           "latency-enabled",
		Type:           FaultLatency,
		Probability:    0.5,
		DurationMillis: 1000,
	}
	result, err := executor.Execute(context.Background(), scenario)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Recovery time should be non-zero when faults are injected and flag enabled.
	if result.InjectedFaults > 0 {
		expectedBase := scenario.DurationMillis / 10
		expectedOverhead := scenario.DurationMillis / 20
		expectedRecovery := expectedBase + expectedOverhead
		require.Equal(t, expectedRecovery, result.RecoveryTimeMillis)
	}
}

// TestChaosExecute_RejectWithFlagEnabled tests reject scenario with reject flag enabled.
func TestChaosExecute_RejectWithFlagEnabled(t *testing.T) {
	executor := NewDefaultChaosExecutor().WithFlags(ChaosFlagConfig{
		EnableReject: true,
	})
	scenario := &ChaosScenario{
		Name:           "reject-enabled",
		Type:           FaultReject,
		Probability:    0.5,
		DurationMillis: 1000,
	}
	result, err := executor.Execute(context.Background(), scenario)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Recovery time should be ~20% + 10% overhead when flag enabled.
	if result.InjectedFaults > 0 {
		expectedBase := scenario.DurationMillis / 5
		expectedOverhead := scenario.DurationMillis / 10
		expectedRecovery := expectedBase + expectedOverhead
		require.Equal(t, expectedRecovery, result.RecoveryTimeMillis)
	}
}

// TestChaosExecute_StaleDataWithFlagEnabled tests stale_data scenario with stale data flag enabled.
func TestChaosExecute_StaleDataWithFlagEnabled(t *testing.T) {
	executor := NewDefaultChaosExecutor().WithFlags(ChaosFlagConfig{
		EnableStaleData: true,
	})
	scenario := &ChaosScenario{
		Name:           "stale-data-enabled",
		Type:           FaultStaleData,
		Probability:    0.3,
		DurationMillis: 1000,
	}
	result, err := executor.Execute(context.Background(), scenario)
	require.NoError(t, err)
	require.NotNil(t, result)

	// More aggressive degradation threshold (>25% instead of >50%).
	if result.InjectedFaults > 0 {
		// Should have recovery time calculated.
		require.True(t, result.RecoveryTimeMillis > 0)
	}
}

// TestChaosExecute_NetworkPartitionAlwaysDegraded tests network partition scenario.
func TestChaosExecute_NetworkPartitionAlwaysDegraded(t *testing.T) {
	executor := NewDefaultChaosExecutor()
	scenario := &ChaosScenario{
		Name:           "partition-test",
		Type:           FaultNetworkPartition,
		Probability:    0.1,
		DurationMillis: 500,
	}
	result, err := executor.Execute(context.Background(), scenario)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Network partition: any fault causes degradation.
	if result.InjectedFaults > 0 {
		require.True(t, result.SystemDegraded)
		// Recovery time should be ~33% of duration.
		expectedRecovery := scenario.DurationMillis / 3
		require.Equal(t, expectedRecovery, result.RecoveryTimeMillis)
	}
}

// TestChaosExecute_RecoveryTimeZeroWhenNoFaults tests that recovery time is zero when no faults injected.
func TestChaosExecute_RecoveryTimeZeroWhenNoFaults(t *testing.T) {
	executor := NewDefaultChaosExecutor().WithFlags(ChaosFlagConfig{
		EnableLatency:   true,
		EnableReject:    true,
		EnableStaleData: true,
	})
	scenario := &ChaosScenario{
		Name:           "no-faults",
		Type:           FaultLatency,
		Probability:    0.0,
		DurationMillis: 1000,
	}
	result, err := executor.Execute(context.Background(), scenario)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(0), result.InjectedFaults)
	require.Equal(t, int64(0), result.RecoveryTimeMillis)
	require.False(t, result.SystemDegraded)
}

package marketmaking

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
	require.Contains(t, scenarios, "latency")
	require.Contains(t, scenarios, "reject")
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
		Type:           "reject",
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
		Type:        "latency",
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
		Type:           "latency",
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
		Type:           "reject",
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
		Type:           "stale_data",
		Probability:    0.5,
		DurationMillis: 10000,
	}
	result, err := executor.Execute(ctx, scenario)
	require.ErrorIs(t, err, context.Canceled)
	// Result may be nil or partial depending on when cancellation occurred
	_ = result
}

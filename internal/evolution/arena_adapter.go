package evolution

import (
	"context"
	"fmt"

	"goagentx/internal/arena"
)

// TesterAdapter wraps an arena.RegressionTester to implement evolution.TesterInterface.
// It handles type conversion between evolution.RegressionConfig/RegressionResult
// and arena.RegressionConfig/RegressionResult.
type TesterAdapter struct {
	tester *arena.RegressionTester
}

// NewTesterAdapter creates an adapter from an arena.RegressionTester.
//
// Args:
//
//	t - the arena regression tester to wrap (must not be nil).
//
// Returns:
//
//	*TesterAdapter - the adapter instance.
//	error - non-nil if tester is nil.
func NewTesterAdapter(t *arena.RegressionTester) (*TesterAdapter, error) {
	if t == nil {
		return nil, fmt.Errorf("tester must not be nil")
	}
	return &TesterAdapter{tester: t}, nil
}

// Run converts evolution.RegressionConfig to arena.RegressionConfig, calls the
// wrapped tester, and converts the result back to evolution.RegressionResult.
//
// Args:
//
//	ctx - operation context for cancellation.
//	cfg - the regression test configuration in evolution package format.
//
// Returns:
//
//	*RegressionResult - the test results in evolution package format.
//	error - delegation error from the wrapped tester or conversion error.
func (a *TesterAdapter) Run(ctx context.Context, cfg RegressionConfig) (*RegressionResult, error) {
	// Convert evolution.RegressionConfig to arena.RegressionConfig.
	arenaCfg := evolutionToArenaConfig(cfg)

	// Delegate to the wrapped tester.
	result, err := a.tester.Run(ctx, arenaCfg)
	if err != nil {
		return nil, fmt.Errorf("tester adapter: %w", err)
	}

	// Convert arena.RegressionResult back to evolution.RegressionResult.
	evolutionResult := arenaToEvolutionResult(result)
	return evolutionResult, nil
}

// evolutionToArenaConfig converts an evolution.RegressionConfig to arena.RegressionConfig.
func evolutionToArenaConfig(cfg RegressionConfig) arena.RegressionConfig {
	return arena.RegressionConfig{
		OldStrategy:  cfg.Baseline,
		NewStrategy:  cfg.Candidate,
		BaselineRuns: cfg.TaskSampleSize,
		CompareRuns:  cfg.TaskSampleSize,
	}
}

// arenaToEvolutionResult converts an arena.RegressionResult to evolution.RegressionResult.
func arenaToEvolutionResult(result *arena.RegressionResult) *RegressionResult {
	if result == nil {
		return nil
	}
	return &RegressionResult{
		CandidateScore: result.NewAvg,
		BaselineScore:  result.OldAvg,
		WinRate:        result.WinRate,
		TotalTasks:     result.Samples,
	}
}

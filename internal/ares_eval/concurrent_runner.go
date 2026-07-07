package ares_eval

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

// ConcurrentRunnerConfig holds configuration for concurrent test execution.
type ConcurrentRunnerConfig struct {
	// MaxParallel is the maximum number of tests to run concurrently.
	// A value of 1 means sequential execution (default).
	MaxParallel int `yaml:"max_parallel" json:"max_parallel"`

	// Timeout is the per-test timeout applied on top of any timeout in the test case itself.
	// A value of 0 means no additional timeout is imposed.
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
}

// DefaultConcurrentRunnerConfig returns a config with safe defaults:
// sequential execution and no extra timeout.
func DefaultConcurrentRunnerConfig() ConcurrentRunnerConfig {
	return ConcurrentRunnerConfig{
		MaxParallel: 1,
		Timeout:     0,
	}
}

// ErrInvalidMaxParallel is returned when MaxParallel is less than 1.
// Errors are defined in errors.go.

// ConcurrentRunner wraps a TestRunner with parallel test execution support.
// It distributes test cases across goroutines using errgroup with a configurable
// concurrency limit, applying per-test timeouts while preserving result order.
type ConcurrentRunner struct {
	inner       TestRunner
	maxParallel int
	timeout     time.Duration
}

// Compile-time interface check: ConcurrentRunner implements TestRunner.
var _ TestRunner = (*ConcurrentRunner)(nil)

// NewConcurrentRunner creates a new ConcurrentRunner that wraps the given TestRunner
// with concurrent execution capabilities.
//
// Args:
//   - inner: the underlying TestRunner to delegate actual execution to (must not be nil).
//   - config: concurrent execution configuration.
//
// Returns:
//   - *ConcurrentRunner: configured concurrent runner instance.
//   - error: ErrInvalidMaxParallel if config.MaxParallel < 1.
func NewConcurrentRunner(inner TestRunner, config ConcurrentRunnerConfig) (*ConcurrentRunner, error) {
	if inner == nil {
		return nil, fmt.Errorf("inner runner must not be nil")
	}
	if config.MaxParallel < 1 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidMaxParallel, config.MaxParallel)
	}
	return &ConcurrentRunner{
		inner:       inner,
		maxParallel: config.MaxParallel,
		timeout:     config.Timeout,
	}, nil
}

// RunSuite runs all test cases in the suite concurrently, respecting the maxParallel limit.
// Each test case gets its own context with the configured per-test timeout.
// Results are returned in the same order as the input test cases.
//
// On context cancellation or partial failure, all completed results are returned
// along with the error from errgroup.
//
// Args:
//   - ctx: parent context for cancellation propagation.
//   - suite: the test suite containing test cases to execute.
//
// Returns:
//   - []TestResult: results for each test case (preserving input order).
//   - error: non-nil if context cancelled or any test fails critically.
func (r *ConcurrentRunner) RunSuite(ctx context.Context, suite TestSuite) ([]TestResult, error) {
	if len(suite.TestCases) == 0 {
		return []TestResult{}, nil
	}

	results := make([]TestResult, len(suite.TestCases))

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(r.maxParallel)

	for i, tc := range suite.TestCases {
		tc := tc // capture loop variable
		idx := i // capture original index for ordered result placement
		eg.Go(func() error {
			select {
			case <-egCtx.Done():
				return egCtx.Err()
			default:
			}

			testCtx := egCtx
			if r.timeout > 0 {
				var cancel context.CancelFunc
				testCtx, cancel = context.WithTimeout(egCtx, r.timeout)
				defer cancel()
			}

			result, err := r.inner.RunSingle(testCtx, tc)
			if err != nil {
				log.Error("test case failed",
					"test_case_id", tc.ID,
					"error", err,
				)
				// Record the error in the result but don't abort other tests.
				result.Error = err.Error()
			}

			// Store result at the original index to preserve input order.
			results[idx] = result

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		// Return partial results along with the error.
		return results, fmt.Errorf("concurrent suite execution: %w", err)
	}

	return results, nil
}

// RunSingle runs a single test case with the configured per-test timeout.
// This delegates directly to the inner runner after applying timeout.
//
// Args:
//   - ctx: parent context for cancellation propagation.
//   - testCase: the test case to execute.
//
// Returns:
//   - TestResult: the test result.
//   - error: execution error or timeout.
func (r *ConcurrentRunner) RunSingle(ctx context.Context, testCase TestCase) (TestResult, error) {
	testCtx := ctx
	if r.timeout > 0 {
		var cancel context.CancelFunc
		testCtx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}

	return r.inner.RunSingle(testCtx, testCase)
}

package ares_eval

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowMockRunner simulates a test runner that takes a configurable amount of time per test.
type slowMockRunner struct {
	delay       time.Duration
	shouldFail  bool
	callCount   int
	callCountMu sync.Mutex
}

func (s *slowMockRunner) RunSuite(_ context.Context, suite TestSuite) ([]TestResult, error) {
	results := make([]TestResult, len(suite.TestCases))
	for i, tc := range suite.TestCases {
		select {
		case <-time.After(s.delay):
			// Simulate work.
		case <-context.Background().Done():
			return nil, context.Background().Err()
		}
		s.incrementCall()
		results[i] = TestResult{
			TestCaseID: tc.ID,
			Duration:   s.delay,
			Metrics:    map[string]float64{"score": 0.9},
		}
		if s.shouldFail && i == 1 {
			results[i].Error = "simulated failure"
		}
	}
	return results, nil
}

func (s *slowMockRunner) RunSingle(ctx context.Context, tc TestCase) (TestResult, error) {
	select {
	case <-time.After(s.delay):
		// Simulate work.
	case <-ctx.Done():
		return TestResult{TestCaseID: tc.ID, Error: ctx.Err().Error()}, ctx.Err()
	}

	s.incrementCall()

	result := TestResult{
		TestCaseID: tc.ID,
		Duration:   s.delay,
		Timestamp:  time.Now(),
		Metrics:    map[string]float64{"score": 0.9},
	}
	if s.shouldFail {
		result.Error = "simulated failure"
		return result, fmt.Errorf("simulated failure")
	}
	return result, nil
}

func (s *slowMockRunner) incrementCall() {
	s.callCountMu.Lock()
	defer s.callCountMu.Unlock()
	s.callCount++
}

// trackingMockRunner records which test cases were executed and in what order.
type trackingMockRunner struct {
	mu       sync.Mutex
	executed []string
	delay    time.Duration
	errOnIDs map[string]error // test case IDs that should return errors
}

func (t *trackingMockRunner) RunSuite(_ context.Context, suite TestSuite) ([]TestResult, error) {
	results := make([]TestResult, len(suite.TestCases))
	for i, tc := range suite.TestCases {
		t.recordExecuted(tc.ID)

		if t.delay > 0 {
			time.Sleep(t.delay)
		}

		result := TestResult{
			TestCaseID: tc.ID,
			Duration:   t.delay,
			Timestamp:  time.Now(),
			Metrics:    map[string]float64{"score": 0.85},
		}

		if t.errOnIDs != nil {
			if err, ok := t.errOnIDs[tc.ID]; ok {
				result.Error = err.Error()
				results[i] = result
				continue
			}
		}

		results[i] = result
	}
	return results, nil
}

func (t *trackingMockRunner) RunSingle(ctx context.Context, tc TestCase) (TestResult, error) {
	t.recordExecuted(tc.ID)

	if t.delay > 0 {
		select {
		case <-time.After(t.delay):
		case <-ctx.Done():
			return TestResult{TestCaseID: tc.ID, Error: ctx.Err().Error()}, ctx.Err()
		}
	}

	result := TestResult{
		TestCaseID: tc.ID,
		Duration:   t.delay,
		Timestamp:  time.Now(),
		Metrics:    map[string]float64{"score": 0.85},
	}

	if t.errOnIDs != nil {
		if err, ok := t.errOnIDs[tc.ID]; ok {
			result.Error = err.Error()
			return result, err
		}
	}

	return result, nil
}

func (t *trackingMockRunner) recordExecuted(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.executed = append(t.executed, id)
}

func (t *trackingMockRunner) getExecuted() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.executed
}

func TestNewConcurrentRunner(t *testing.T) {
	inner := &mockTestRunner{}
	config := ConcurrentRunnerConfig{MaxParallel: 4, Timeout: 10 * time.Second}

	runner, err := NewConcurrentRunner(inner, config)
	require.NoError(t, err)
	assert.NotNil(t, runner)
	assert.Equal(t, 4, runner.maxParallel)
	assert.Equal(t, 10*time.Second, runner.timeout)
}

func TestNewConcurrentRunner_InvalidConfig(t *testing.T) {
	inner := &mockTestRunner{}

	// MaxParallel < 1 should fail.
	_, err := NewConcurrentRunner(inner, ConcurrentRunnerConfig{MaxParallel: 0})
	assert.ErrorIs(t, err, ErrInvalidMaxParallel)

	_, err = NewConcurrentRunner(inner, ConcurrentRunnerConfig{MaxParallel: -1})
	assert.ErrorIs(t, err, ErrInvalidMaxParallel)

	// Nil inner should fail.
	_, err = NewConcurrentRunner(nil, DefaultConcurrentRunnerConfig())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inner runner must not be nil")
}

func TestDefaultConcurrentRunnerConfig(t *testing.T) {
	config := DefaultConcurrentRunnerConfig()
	assert.Equal(t, 1, config.MaxParallel)
	assert.Equal(t, time.Duration(0), config.Timeout)
}

func TestConcurrentRunner_RunSuite_Sequential(t *testing.T) {
	tracker := &trackingMockRunner{delay: 10 * time.Millisecond}
	runner, err := NewConcurrentRunner(tracker, ConcurrentRunnerConfig{MaxParallel: 1})
	require.NoError(t, err)

	suite := TestSuite{
		Name: "seq-test",
		TestCases: []TestCase{
			{ID: "a", Name: "A", Input: "in-a"},
			{ID: "b", Name: "B", Input: "in-b"},
			{ID: "c", Name: "C", Input: "in-c"},
		},
	}

	results, err := runner.RunSuite(context.Background(), suite)
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Verify order is preserved.
	assert.Equal(t, "a", results[0].TestCaseID)
	assert.Equal(t, "b", results[1].TestCaseID)
	assert.Equal(t, "c", results[2].TestCaseID)

	// Sequential mode should execute in order.
	executed := tracker.getExecuted()
	assert.Equal(t, []string{"a", "b", "c"}, executed)
}

func TestConcurrentRunner_RunSuite_Parallel(t *testing.T) {
	// Use a small delay so tests run concurrently.
	tracker := &trackingMockRunner{delay: 50 * time.Millisecond}
	runner, err := NewConcurrentRunner(tracker, ConcurrentRunnerConfig{MaxParallel: 3})
	require.NoError(t, err)

	suite := TestSuite{
		Name: "par-test",
		TestCases: []TestCase{
			{ID: "p1", Name: "P1", Input: "in-p1"},
			{ID: "p2", Name: "P2", Input: "in-p2"},
			{ID: "p3", Name: "P3", Input: "in-p3"},
		},
	}

	start := time.Now()
	results, err := runner.RunSuite(context.Background(), suite)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Len(t, results, 3)

	// With MaxParallel=3 and 3 tests each taking ~50ms, concurrent execution
	// should complete in roughly 50ms (not 150ms sequential).
	// Allow generous margin for CI environments.
	assert.Less(t, elapsed, 120*time.Millisecond,
		"concurrent execution should be faster than sequential: took %v", elapsed)

	// All three tests should have been executed.
	executed := tracker.getExecuted()
	assert.Len(t, executed, 3)

	// Results should still be in order regardless of completion order.
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.TestCaseID
	}
	assert.Equal(t, []string{"p1", "p2", "p3"}, ids)
}

func TestConcurrentRunner_RunSuite_RespectsMaxParallel(t *testing.T) {
	// Create a runner with maxParallel=2 but 4 tests.
	// Each test takes 30ms. With maxParallel=2, expected time is ~60ms (2 batches).
	tracker := &trackingMockRunner{delay: 30 * time.Millisecond}
	runner, err := NewConcurrentRunner(tracker, ConcurrentRunnerConfig{MaxParallel: 2})
	require.NoError(t, err)

	suite := TestSuite{
		Name: "limit-test",
		TestCases: []TestCase{
			{ID: "t1", Name: "T1", Input: "in-1"},
			{ID: "t2", Name: "T2", Input: "in-2"},
			{ID: "t3", Name: "T3", Input: "in-3"},
			{ID: "t4", Name: "T4", Input: "in-4"},
		},
	}

	start := time.Now()
	results, err := runner.RunSuite(context.Background(), suite)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Len(t, results, 4)

	// Should take roughly 2 batches * 30ms = 60ms (not 120ms for all at once).
	// Allow margin for scheduling overhead.
	assert.Greater(t, elapsed, 40*time.Millisecond,
		"should show concurrency limiting effect: took %v", elapsed)
	assert.Less(t, elapsed, 150*time.Millisecond,
		"should not take as long as fully sequential: took %v", elapsed)
}

func TestConcurrentRunner_RunSuite_Timeout(t *testing.T) {
	// Inner runner takes 200ms per test, but timeout is set to 50ms.
	slow := &slowMockRunner{delay: 200 * time.Millisecond}
	runner, err := NewConcurrentRunner(slow, ConcurrentRunnerConfig{
		MaxParallel: 2,
		Timeout:     50 * time.Millisecond,
	})
	require.NoError(t, err)

	suite := TestSuite{
		Name:      "timeout-test",
		TestCases: []TestCase{{ID: "slow-tc", Name: "Slow", Input: "input"}},
	}

	results, err := runner.RunSuite(context.Background(), suite)
	// Errors are recorded in individual results; RunSuite returns nil unless
	// context is externally cancelled.
	require.NoError(t, err)
	require.Len(t, results, 1)
	// The result should contain a timeout-related error.
	assert.NotEmpty(t, results[0].Error,
		"timeout should produce an error in the result")
}

func TestConcurrentRunner_RunSingle_Timeout(t *testing.T) {
	slow := &slowMockRunner{delay: 5 * time.Second}
	runner, err := NewConcurrentRunner(slow, ConcurrentRunnerConfig{
		MaxParallel: 1,
		Timeout:     50 * time.Millisecond,
	})
	require.NoError(t, err)

	tc := TestCase{ID: "timeout-tc", Name: "Timeout", Input: "input"}
	start := time.Now()
	_, err = runner.RunSingle(context.Background(), tc)
	elapsed := time.Since(start)

	// Should fail due to timeout.
	require.Error(t, err)
	assert.Less(t, elapsed, 500*time.Millisecond,
		"should have been cancelled by timeout: took %v", elapsed)
}

func TestConcurrentRunner_RunSuite_ErrorPropagation(t *testing.T) {
	tracker := &trackingMockRunner{
		delay: 10 * time.Millisecond,
		errOnIDs: map[string]error{
			"fail-tc": errors.New("test failure"),
		},
	}
	runner, err := NewConcurrentRunner(tracker, ConcurrentRunnerConfig{MaxParallel: 2})
	require.NoError(t, err)

	suite := TestSuite{
		Name: "error-test",
		TestCases: []TestCase{
			{ID: "ok-tc", Name: "OK", Input: "ok-input"},
			{ID: "fail-tc", Name: "Fail", Input: "fail-input"},
			{ID: "ok2-tc", Name: "OK2", Input: "ok2-input"},
		},
	}

	results, err := runner.RunSuite(context.Background(), suite)
	// Errors are recorded in individual results; RunSuite itself should not return
	// an error unless context cancellation occurs.
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Find the failed result and verify error was recorded.
	var foundFailed bool
	for _, r := range results {
		if r.TestCaseID == "fail-tc" {
			assert.Contains(t, r.Error, "test failure")
			foundFailed = true
		}
	}
	assert.True(t, foundFailed, "should find failed test case result")

	// Other results should not have errors.
	for _, r := range results {
		if r.TestCaseID != "fail-tc" {
			assert.Empty(t, r.Error, "result for %s should not have error", r.TestCaseID)
		}
	}
}

func TestConcurrentRunner_RunSuite_EmptySuite(t *testing.T) {
	inner := &mockTestRunner{}
	runner, err := NewConcurrentRunner(inner, DefaultConcurrentRunnerConfig())
	require.NoError(t, err)

	results, err := runner.RunSuite(context.Background(), TestSuite{Name: "empty"})
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestConcurrentRunner_RunSuite_ContextCancellation(t *testing.T) {
	slow := &slowMockRunner{delay: 10 * time.Second}
	runner, err := NewConcurrentRunner(slow, ConcurrentRunnerConfig{MaxParallel: 2})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay to simulate mid-execution cancellation.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	suite := TestSuite{
		Name: "cancel-test",
		TestCases: []TestCase{
			{ID: "c1", Name: "C1", Input: "in-c1"},
			{ID: "c2", Name: "C2", Input: "in-c2"},
			{ID: "c3", Name: "C3", Input: "in-c3"},
		},
	}

	results, err := runner.RunSuite(ctx, suite)
	assert.Error(t, err)
	_ = results // Partial results may be returned.
}

func TestConcurrentRunner_RunSingle_NoTimeout(t *testing.T) {
	inner := &mockTestRunner{
		results: []TestResult{
			{TestCaseID: "single", ActualOutput: "hello", Metrics: map[string]float64{"s": 1.0}},
		},
	}
	runner, err := NewConcurrentRunner(inner, ConcurrentRunnerConfig{MaxParallel: 1})
	require.NoError(t, err)

	tc := TestCase{ID: "single", Name: "Single", Input: "hi"}
	result, err := runner.RunSingle(context.Background(), tc)
	require.NoError(t, err)
	assert.Equal(t, "single", result.TestCaseID)
	assert.Equal(t, "hello", result.ActualOutput)
}

func TestConcurrentRunner_RaceConditionSafety(t *testing.T) {
	// This test is designed to be run with -race flag to detect data races.
	tracker := &trackingMockRunner{delay: 5 * time.Millisecond}
	runner, err := NewConcurrentRunner(tracker, ConcurrentRunnerConfig{MaxParallel: 4})
	require.NoError(t, err)

	// Generate many test cases to increase chance of exposing races.
	testCases := make([]TestCase, 20)
	for i := 0; i < 20; i++ {
		testCases[i] = TestCase{
			ID:    fmt.Sprintf("race-tc-%d", i),
			Name:  fmt.Sprintf("Race Test %d", i),
			Input: fmt.Sprintf("input-%d", i),
		}
	}

	suite := TestSuite{Name: "race-suite", TestCases: testCases}

	// Run multiple times to increase race detection probability.
	for run := 0; run < 5; run++ {
		results, err := runner.RunSuite(context.Background(), suite)
		require.NoError(t, err, "run %d should succeed", run)
		require.Len(t, results, 20, "run %d should return all results", run)

		// Verify each result has a valid TestCaseID.
		for i, r := range results {
			assert.NotEmpty(t, r.TestCaseID, "run %d, result %d should have ID", run, i)
		}
	}
}

func TestConcurrentRunner_ResultsOrderedCorrectly(t *testing.T) {
	// Use varying delays to ensure out-of-order completion.
	inner := &variableDelayRunner{}
	runner, err := NewConcurrentRunner(inner, ConcurrentRunnerConfig{MaxParallel: 4})
	require.NoError(t, err)

	suite := TestSuite{
		Name: "order-test",
		TestCases: []TestCase{
			{ID: "first", Name: "First", Input: "a"},
			{ID: "second", Name: "Second", Input: "b"},
			{ID: "third", Name: "Third", Input: "c"},
			{ID: "fourth", Name: "Fourth", Input: "d"},
			{ID: "fifth", Name: "Fifth", Input: "e"},
		},
	}

	results, err := runner.RunSuite(context.Background(), suite)
	require.NoError(t, err)
	require.Len(t, results, 5)

	// Results must be in original order regardless of completion order.
	assert.Equal(t, "first", results[0].TestCaseID)
	assert.Equal(t, "second", results[1].TestCaseID)
	assert.Equal(t, "third", results[2].TestCaseID)
	assert.Equal(t, "fourth", results[3].TestCaseID)
	assert.Equal(t, "fifth", results[4].TestCaseID)
}

// variableDelayRunner returns results with different delays per test case
// to intentionally cause out-of-order completion.
type variableDelayRunner struct{}

func (v *variableDelayRunner) RunSuite(_ context.Context, suite TestSuite) ([]TestResult, error) {
	results := make([]TestResult, len(suite.TestCases))
	for i, tc := range suite.TestCases {
		// Reverse delay order: later test cases finish first.
		delay := time.Duration(len(suite.TestCases)-i) * 10 * time.Millisecond
		time.Sleep(delay)
		results[i] = TestResult{
			TestCaseID: tc.ID,
			Duration:   delay,
			Timestamp:  time.Now(),
			Metrics:    map[string]float64{"score": 1.0},
		}
	}
	return results, nil
}

func (v *variableDelayRunner) RunSingle(ctx context.Context, tc TestCase) (TestResult, error) {
	return TestResult{
		TestCaseID: tc.ID,
		Timestamp:  time.Now(),
		Metrics:    map[string]float64{"score": 1.0},
	}, nil
}

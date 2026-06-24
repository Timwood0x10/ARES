package eval

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTestRunner is a simple mock implementation of TestRunner for testing.
type mockTestRunner struct {
	results []TestResult
	err     error
}

func (m *mockTestRunner) RunSuite(_ context.Context, suite TestSuite) ([]TestResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

func (m *mockTestRunner) RunSingle(_ context.Context, tc TestCase) (TestResult, error) {
	if m.err != nil {
		return TestResult{}, m.err
	}
	for _, r := range m.results {
		if r.TestCaseID == tc.ID {
			return r, nil
		}
	}
	return TestResult{TestCaseID: tc.ID}, nil
}

// newMockResults creates a slice of test results with scores for testing.
func newMockResults(configName string, count int) []TestResult {
	results := make([]TestResult, count)
	for i := range results {
		results[i] = TestResult{
			TestCaseID:   fmt.Sprintf("tc%d", i+1),
			ActualOutput: fmt.Sprintf("output%d from %s", i+1, configName),
			Duration:     time.Duration(100+i*50) * time.Millisecond,
			TokensUsed:   10 * (i + 1),
			Metrics:      map[string]float64{"correctness": 0.9 - float64(i)*0.1, "completeness": 0.8 - float64(i)*0.1},
		}
	}
	return results
}

func TestNewComparisonRunner(t *testing.T) {
	suite := &TestSuite{Name: "test-suite"}
	factory := func(AgentConfig) (TestRunner, error) {
		return &mockTestRunner{}, nil
	}
	runner := NewComparisonRunner(suite, factory)
	assert.NotNil(t, runner)
	assert.Same(t, suite, runner.suite)
	assert.NotNil(t, runner.runnerFactory)
}

func TestComparisonRunner_Run_EmptyConfigs(t *testing.T) {
	suite := &TestSuite{
		Name:      "empty-test",
		TestCases: []TestCase{{ID: "tc1", Name: "Test 1", Input: "hello"}},
	}
	factory := func(AgentConfig) (TestRunner, error) {
		return &mockTestRunner{}, nil
	}
	runner := NewComparisonRunner(suite, factory)

	report, err := runner.Run(context.Background(), []AgentConfig{})
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, "empty-test", report.SuiteName)
	assert.Empty(t, report.Results)
	assert.Empty(t, report.Leaderboard)
	assert.Equal(t, 0, report.Summary.TotalConfigs)
	assert.Equal(t, 1, report.Summary.TotalTests)
}

func TestComparisonRunner_Run_SingleConfig(t *testing.T) {
	suite := &TestSuite{
		Name: "single-config-suite",
		TestCases: []TestCase{
			{ID: "tc1", Name: "Test 1", Input: "input1"},
			{ID: "tc2", Name: "Test 2", Input: "input2"},
		},
	}

	configs := []AgentConfig{
		{Name: "config-a", Model: "gpt-4"},
	}

	callCount := 0
	factory := func(cfg AgentConfig) (TestRunner, error) {
		callCount++
		assert.Equal(t, "config-a", cfg.Name)
		return &mockTestRunner{results: newMockResults(cfg.Name, 2)}, nil
	}

	runner := NewComparisonRunner(suite, factory)
	report, err := runner.Run(context.Background(), configs)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, 1, callCount)
	assert.Len(t, report.Results, 1)

	cr := report.Results[0]
	assert.Equal(t, "config-a", cr.ConfigName)
	assert.Len(t, cr.Results, 2)
	assert.Greater(t, cr.OverallScore, 0.0)
	assert.Greater(t, cr.PassRate, 0.0)
	assert.NotEmpty(t, cr.AggregatedScores)
}

func TestComparisonRunner_Run_MultipleConfigs(t *testing.T) {
	suite := &TestSuite{
		Name: "multi-config-suite",
		TestCases: []TestCase{
			{ID: "tc1", Name: "Test 1", Input: "input1"},
			{ID: "tc2", Name: "Test 2", Input: "input2"},
		},
	}

	configs := []AgentConfig{
		{Name: "config-alpha", Model: "gpt-4"},
		{Name: "config-beta", Model: "claude-3"},
		{Name: "config-gamma", Model: "gemini-pro"},
	}

	factory := func(cfg AgentConfig) (TestRunner, error) {
		results := newMockResults(cfg.Name, 2)
		// Vary scores per config so leaderboard ordering is meaningful.
		switch cfg.Name {
		case "config-alpha":
			results[0].Metrics["correctness"] = 0.95
			results[1].Metrics["correctness"] = 0.90
		case "config-beta":
			results[0].Metrics["correctness"] = 0.80
			results[1].Metrics["correctness"] = 0.75
		default:
			results[0].Metrics["correctness"] = 0.60
			results[1].Metrics["correctness"] = 0.55
		}
		return &mockTestRunner{results: results}, nil
	}

	runner := NewComparisonRunner(suite, factory)
	report, err := runner.Run(context.Background(), configs)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Len(t, report.Results, 3)
	assert.Len(t, report.Leaderboard, 3)

	// Leaderboard should be sorted by overall score descending.
	assert.Equal(t, "config-alpha", report.Leaderboard[0].ConfigName)
	assert.Equal(t, 1, report.Leaderboard[0].Rank)
	assert.Equal(t, "config-beta", report.Leaderboard[1].ConfigName)
	assert.Equal(t, 2, report.Leaderboard[1].Rank)
	assert.Equal(t, "config-gamma", report.Leaderboard[2].ConfigName)
	assert.Equal(t, 3, report.Leaderboard[2].Rank)

	// Summary should identify best config.
	assert.Equal(t, "config-alpha", report.Summary.BestConfig)
	assert.Equal(t, 3, report.Summary.TotalConfigs)
	assert.Greater(t, report.Summary.AvgOverall, 0.0)
}

func TestComparisonRunner_Run_ConfigError(t *testing.T) {
	suite := &TestSuite{
		Name:      "error-suite",
		TestCases: []TestCase{{ID: "tc1", Name: "Test 1"}},
	}

	configs := []AgentConfig{
		{Name: "good-config", Model: "gpt-4"},
		{Name: "bad-config", Model: "invalid"},
	}

	factory := func(cfg AgentConfig) (TestRunner, error) {
		if cfg.Name == "bad-config" {
			return nil, assert.AnError
		}
		return &mockTestRunner{results: newMockResults(cfg.Name, 1)}, nil
	}

	runner := NewComparisonRunner(suite, factory)
	report, err := runner.Run(context.Background(), configs)
	require.NoError(t, err) // Should not return error; bad config recorded in results.
	require.NotNil(t, report)
	assert.Len(t, report.Results, 2)

	// Bad config should have error set.
	badResult := report.Results[1]
	assert.Equal(t, "bad-config", badResult.ConfigName)
	assert.NotEmpty(t, badResult.Error)
}

func TestGenerateLeaderboard_Ordering(t *testing.T) {
	results := []ComparisonResult{
		{
			ConfigName:       "low-score",
			OverallScore:     0.3,
			AggregatedScores: map[string]float64{"correctness": 0.3},
		},
		{
			ConfigName:       "high-score",
			OverallScore:     0.9,
			AggregatedScores: map[string]float64{"correctness": 0.9},
		},
		{
			ConfigName:       "mid-score",
			OverallScore:     0.6,
			AggregatedScores: map[string]float64{"correctness": 0.6},
		},
	}

	board := GenerateLeaderboard(results)
	require.Len(t, board, 3)

	// Highest score should be rank 1.
	assert.Equal(t, "high-score", board[0].ConfigName)
	assert.Equal(t, 1, board[0].Rank)
	assert.InDelta(t, 0.9, board[0].Overall, 0.001)

	assert.Equal(t, "mid-score", board[1].ConfigName)
	assert.Equal(t, 2, board[1].Rank)

	assert.Equal(t, "low-score", board[2].ConfigName)
	assert.Equal(t, 3, board[2].Rank)
}

func TestGenerateLeaderboard_TieHandling(t *testing.T) {
	results := []ComparisonResult{
		{
			ConfigName:       "same-a",
			OverallScore:     0.8,
			AggregatedScores: map[string]float64{"c": 0.8},
		},
		{
			ConfigName:       "same-b",
			OverallScore:     0.8,
			AggregatedScores: map[string]float64{"c": 0.8},
		},
		{
			ConfigName:       "lower",
			OverallScore:     0.5,
			AggregatedScores: map[string]float64{"c": 0.5},
		},
	}

	board := GenerateLeaderboard(results)
	require.Len(t, board, 3)

	// Tied entries should share rank 1.
	assert.Equal(t, 1, board[0].Rank)
	assert.Equal(t, 1, board[1].Rank)
	assert.Equal(t, 3, board[2].Rank)
}

func TestGenerateLeaderboard_Empty(t *testing.T) {
	board := GenerateLeaderboard([]ComparisonResult{})
	assert.Empty(t, board)
}

func TestGenerateLeaderboard_SingleEntry(t *testing.T) {
	results := []ComparisonResult{
		{
			ConfigName:       "only",
			OverallScore:     1.0,
			AggregatedScores: map[string]float64{"c": 1.0},
		},
	}

	board := GenerateLeaderboard(results)
	require.Len(t, board, 1)
	assert.Equal(t, 1, board[0].Rank)
	assert.Equal(t, "only", board[0].ConfigName)
}

func TestComparisonRunner_GenerateMarkdown(t *testing.T) {
	suite := &TestSuite{
		Name:        "markdown-test-suite",
		Description: "A test suite for markdown generation",
		TestCases: []TestCase{
			{ID: "tc1", Name: "Hello World", Input: "say hello"},
			{ID: "tc2", Name: "Math Test", Input: "1+1"},
		},
	}

	configs := []AgentConfig{
		{Name: "model-a", Model: "gpt-4"},
		{Name: "model-b", Model: "claude-3"},
	}

	factory := func(cfg AgentConfig) (TestRunner, error) {
		results := newMockResults(cfg.Name, 2)
		if cfg.Name == "model-a" {
			results[0].Metrics["correctness"] = 0.95
			results[1].Metrics["correctness"] = 0.85
		} else {
			results[0].Metrics["correctness"] = 0.70
			results[1].Metrics["correctness"] = 0.60
		}
		return &mockTestRunner{results: results}, nil
	}

	runner := NewComparisonRunner(suite, factory)
	report, err := runner.Run(context.Background(), configs)
	require.NoError(t, err)

	md, err := runner.GenerateMarkdown(report)
	require.NoError(t, err)
	require.NotEmpty(t, md)

	// Verify key sections exist.
	assert.Contains(t, md, "# Agent Comparison Report")
	assert.Contains(t, md, "markdown-test-suite")
	assert.Contains(t, md, "## Summary")
	assert.Contains(t, md, "## Leaderboard")
	assert.Contains(t, md, "## Per-Test Results")
	assert.Contains(t, md, "## Dimension Analysis")
	assert.Contains(t, md, "model-a")
	assert.Contains(t, md, "model-b")
	assert.Contains(t, md, "| Rank | Config | Overall Score")

	// Verify table structure.
	assert.Contains(t, md, "|---") // Table separator lines.
	assert.Contains(t, md, "Total Configurations")
	assert.Contains(t, md, "Best Configuration")
}

func TestComparisonRunner_GenerateMarkdown_EmptyReport(t *testing.T) {
	suite := &TestSuite{Name: "empty"}
	runner := NewComparisonRunner(suite, func(AgentConfig) (TestRunner, error) {
		return &mockTestRunner{}, nil
	})

	report := &ComparisonReport{
		SuiteName:   "empty",
		Timestamp:   time.Now(),
		Results:     []ComparisonResult{},
		Leaderboard: []LeaderboardEntry{},
		Summary:     ComparisonSummary{},
	}

	md, err := runner.GenerateMarkdown(report)
	require.NoError(t, err)
	assert.Contains(t, md, "# Agent Comparison Report")
}

func TestComputePassRate(t *testing.T) {
	allPass := []TestResult{
		{TestCaseID: "t1", Error: ""},
		{TestCaseID: "t2", Error: ""},
	}
	assert.InDelta(t, 1.0, computePassRate(allPass), 0.0001)

	someFail := []TestResult{
		{TestCaseID: "t1", Error: ""},
		{TestCaseID: "t2", Error: "something failed"},
		{TestCaseID: "t3", Error: ""},
	}
	assert.InDelta(t, 2.0/3.0, computePassRate(someFail), 0.0001)

	assert.InDelta(t, 0.0, computePassRate([]TestResult{}), 0.0001)
}

func TestComputeOverallScore(t *testing.T) {
	scores := map[string]float64{"a": 0.5, "b": 1.0, "c": 0.0}
	assert.InDelta(t, 0.5, computeOverallScore(scores), 0.0001)

	assert.InDelta(t, 0.0, computeOverallScore(map[string]float64{}), 0.0001)
}

func TestAggregateScores(t *testing.T) {
	results := []TestResult{
		{TestCaseID: "t1", Metrics: map[string]float64{"dim1": 0.8, "dim2": 0.6}},
		{TestCaseID: "t2", Metrics: map[string]float64{"dim1": 1.0, "dim2": 0.4}},
	}

	agg := aggregateScores(results)
	assert.InDelta(t, 0.9, agg["dim1"], 0.0001)
	assert.InDelta(t, 0.5, agg["dim2"], 0.0001)
}

func TestBuildComparisonSummary(t *testing.T) {
	results := []ComparisonResult{
		{
			ConfigName:   "best",
			OverallScore: 0.9,
			Error:        "",
		},
		{
			ConfigName:   "worst",
			OverallScore: 0.3,
			Error:        "",
		},
	}

	sum := buildComparisonSummary(results)
	assert.Equal(t, 2, sum.TotalConfigs)
	assert.Equal(t, "best", sum.BestConfig)
	assert.InDelta(t, 0.6, sum.AvgOverall, 0.001)
	assert.Greater(t, sum.ScoreVariance, 0.0)
}

func TestCopyMap(t *testing.T) {
	original := map[string]float64{"a": 1.0, "b": 2.0}
	copied := copyMap(original)

	assert.Equal(t, original, copied)
	// Mutating copy should not affect original.
	copied["a"] = 99.0
	assert.Equal(t, 1.0, original["a"])

	assert.Nil(t, copyMap(nil))
}

func TestComparisonRunner_Run_ContextCancellation(t *testing.T) {
	suite := &TestSuite{
		Name:      "cancel-suite",
		TestCases: []TestCase{{ID: "tc1", Name: "T1"}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	runner := NewComparisonRunner(suite, func(AgentConfig) (TestRunner, error) {
		return &mockTestRunner{}, nil
	})

	configs := []AgentConfig{{Name: "c1", Model: "m1"}}
	_, err := runner.Run(ctx, configs)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestGenerateMarkdown_ContainsPerDimensionAnalysis(t *testing.T) {
	suite := &TestSuite{
		Name: "dim-suite",
		TestCases: []TestCase{
			{ID: "tc1", Name: "T1", Input: "in"},
		},
	}

	factory := func(cfg AgentConfig) (TestRunner, error) {
		return &mockTestRunner{
			results: []TestResult{
				{
					TestCaseID: "tc1",
					Metrics:    map[string]float64{"correctness": 0.95, "efficiency": 0.80},
				},
			},
		}, nil
	}

	runner := NewComparisonRunner(suite, factory)
	report, _ := runner.Run(context.Background(), []AgentConfig{{Name: "c1", Model: "m1"}})

	md, err := runner.GenerateMarkdown(report)
	require.NoError(t, err)
	assert.True(t, strings.Contains(md, "### correctness"), "should contain dimension header for correctness")
	assert.True(t, strings.Contains(md, "### efficiency"), "should contain dimension header for efficiency")
}

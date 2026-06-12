package eval

import (
	"context"
	"errors"
	"time"
)

// ErrNilExecutor is returned when a nil executor is provided.
var ErrNilExecutor = errors.New("executor is nil")

// AgentTestRunner runs test cases against an agent.
type AgentTestRunner struct {
	executor AgentExecutor
}

// NewAgentTestRunner creates a new agent test runner.
func NewAgentTestRunner(executor AgentExecutor) (*AgentTestRunner, error) {
	if executor == nil {
		return nil, ErrNilExecutor
	}
	return &AgentTestRunner{executor: executor}, nil
}

// RunSuite runs all test cases in a suite.
func (r *AgentTestRunner) RunSuite(ctx context.Context, suite TestSuite) ([]TestResult, error) {
	results := make([]TestResult, 0, len(suite.TestCases))

	for _, tc := range suite.TestCases {
		result, err := r.RunSingle(ctx, tc)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}

	return results, nil
}

// RunSingle runs a single test case.
func (r *AgentTestRunner) RunSingle(ctx context.Context, testCase TestCase) (TestResult, error) {
	result := TestResult{
		TestCaseID: testCase.ID,
		Timestamp:  time.Now(),
		Metrics:    make(map[string]float64),
	}

	// Create timeout context
	timeout := testCase.Timeout.ToDuration()
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Execute the agent
	start := time.Now()
	output, toolsUsed, tokensUsed, err := r.executor.Execute(ctx, testCase.Input)
	result.Duration = time.Since(start)

	result.ActualOutput = output
	result.ToolsUsed = toolsUsed
	result.TokensUsed = tokensUsed

	if err != nil {
		result.Error = err.Error()
	}

	return result, nil
}

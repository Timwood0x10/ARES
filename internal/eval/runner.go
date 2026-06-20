package eval

import (
	"context"
	"errors"
)

// TestRunner runs test cases and produces results.
type TestRunner interface {
	// RunSuite runs all test cases in a suite and returns results.
	RunSuite(ctx context.Context, suite TestSuite) ([]TestResult, error)
	// RunSingle runs a single test case and returns the result.
	RunSingle(ctx context.Context, testCase TestCase) (TestResult, error)
}

// AgentExecutor executes an agent for testing.
type AgentExecutor interface {
	// Execute runs the agent with the given input and returns the output.
	Execute(ctx context.Context, input string) (output string, toolsUsed []string, tokensUsed int, err error)
}

// ErrNilRegistry is returned when a registry is required but not set.
var ErrNilRegistry = errors.New("evaluator registry is nil")

// ErrEvaluatorNotFound is returned when no evaluator is found by name.
var ErrEvaluatorNotFound = errors.New("evaluator not found in registry")

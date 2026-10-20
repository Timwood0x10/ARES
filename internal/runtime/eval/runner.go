package eval

import (
	"context"
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

// ProvenanceReporter is the OPTIONAL companion to AgentExecutor: an executor
// built on the real runtime can report where the terminal answer's body came
// from, which is what lets a judge treat a gap body as a non-answer instead of
// scoring its text (defect G1: "did less and still looked successful").
//
// It is a separate, narrow interface discovered by type assertion rather than a
// widened AgentExecutor: the LLM-backed gate executor has no runtime behind it
// and must keep working untouched, and an executor that cannot report provenance
// says so by not implementing this — which the penalty rule treats as "no
// signal", not as "healthy".
//
// SEQUENTIAL RUNNERS ONLY. The report is a single "last executed case" fact, not
// a per-case keyed one, so it is only meaningful when the runner finishes a case
// and reads the report before starting the next. Under a concurrent runner the
// report belongs to whichever case finished last, and every case in the batch
// would be judged against that one case's provenance — silently, because the
// facts are plausible for any case. An executor that must serve a concurrent
// runner needs a per-case report (keyed by case id) instead of this interface,
// not a mutex around this one.
type ProvenanceReporter interface {
	// AnswerProvenance returns the LAST executed case's body source (one of the
	// ares_events.AnswerBodySource* values, "" when unreported) and whether its
	// answer was forced by the growth-depth guard.
	//
	// Valid only for the sequential runners (AgentTestRunner): read it after that
	// case's Execute, before the next one starts. Do NOT wire it to a
	// ConcurrentRunner — see the interface doc for what that silently produces.
	AnswerProvenance() (bodySource string, truncated bool)
}

// ErrNilRegistry is returned when a registry is required but not set.
// Errors are defined in errors.go.

// ErrEvaluatorNotFound is returned when no evaluator is found by name.
// Errors are defined in errors.go.

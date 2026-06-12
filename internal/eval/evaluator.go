// Package eval provides LLM evaluation and benchmarking capabilities,
// including test suite execution, result reporting, and agent performance metrics.
package eval

import (
	"context"
	"strings"
)

// Evaluator evaluates test results and produces scores.
type Evaluator interface {
	// Evaluate evaluates a test result and returns scores.
	Evaluate(ctx context.Context, testCase TestCase, result TestResult) ([]EvalScore, error)
}

// ExactMatchEvaluator checks if the output exactly matches the expected output.
type ExactMatchEvaluator struct{}

// NewExactMatchEvaluator creates a new exact match evaluator.
func NewExactMatchEvaluator() *ExactMatchEvaluator {
	return &ExactMatchEvaluator{}
}

// Evaluate returns a score based on exact match.
func (e *ExactMatchEvaluator) Evaluate(ctx context.Context, testCase TestCase, result TestResult) ([]EvalScore, error) {
	_ = ctx // reserved for future use (e.g., LLM-based evaluation)
	if testCase.ExpectedOutput == "" {
		return []EvalScore{{Metric: "exact_match", Score: 1.0, Details: "no expected output specified"}}, nil
	}

	score := 0.0
	if result.ActualOutput == testCase.ExpectedOutput {
		score = 1.0
	}

	return []EvalScore{
		{
			Metric:  "exact_match",
			Score:   score,
			Details: "compares actual output to expected output",
		},
	}, nil
}

// SubstringMatchEvaluator checks if the expected output is a substring of the actual output.
type SubstringMatchEvaluator struct{}

// NewSubstringMatchEvaluator creates a new substring match evaluator.
func NewSubstringMatchEvaluator() *SubstringMatchEvaluator {
	return &SubstringMatchEvaluator{}
}

// Evaluate returns a score based on substring match.
func (e *SubstringMatchEvaluator) Evaluate(ctx context.Context, testCase TestCase, result TestResult) ([]EvalScore, error) {
	_ = ctx // reserved for future use
	if testCase.ExpectedOutput == "" {
		return []EvalScore{{Metric: "substring_match", Score: 1.0, Details: "no expected output specified"}}, nil
	}

	score := 0.0
	if strings.Contains(result.ActualOutput, testCase.ExpectedOutput) {
		score = 1.0
	}

	return []EvalScore{
		{
			Metric:  "substring_match",
			Score:   score,
			Details: "checks if expected output is substring of actual output",
		},
	}, nil
}

// KeywordPresenceEvaluator checks if specified keywords are present in the output.
type KeywordPresenceEvaluator struct {
	Keywords []string
}

// NewKeywordPresenceEvaluator creates a new keyword presence evaluator.
func NewKeywordPresenceEvaluator(keywords []string) *KeywordPresenceEvaluator {
	return &KeywordPresenceEvaluator{Keywords: keywords}
}

// Evaluate returns a score based on keyword presence.
func (e *KeywordPresenceEvaluator) Evaluate(ctx context.Context, testCase TestCase, result TestResult) ([]EvalScore, error) {
	if len(e.Keywords) == 0 {
		return []EvalScore{{Metric: "keyword_presence", Score: 1.0, Details: "no keywords specified"}}, nil
	}

	present := 0
	lowerOutput := strings.ToLower(result.ActualOutput)
	for _, keyword := range e.Keywords {
		if strings.Contains(lowerOutput, strings.ToLower(keyword)) {
			present++
		}
	}

	score := float64(present) / float64(len(e.Keywords))

	return []EvalScore{
		{
			Metric:  "keyword_presence",
			Score:   score,
			Details: "checks presence of specified keywords",
		},
	}, nil
}

// ToolUsageEvaluator checks if expected tools were used.
type ToolUsageEvaluator struct{}

// NewToolUsageEvaluator creates a new tool usage evaluator.
func NewToolUsageEvaluator() *ToolUsageEvaluator {
	return &ToolUsageEvaluator{}
}

// Evaluate returns a score based on tool usage.
func (e *ToolUsageEvaluator) Evaluate(ctx context.Context, testCase TestCase, result TestResult) ([]EvalScore, error) {
	if len(testCase.ExpectedTools) == 0 {
		return []EvalScore{{Metric: "tool_usage", Score: 1.0, Details: "no expected tools specified"}}, nil
	}

	used := 0
	for _, expectedTool := range testCase.ExpectedTools {
		for _, usedTool := range result.ToolsUsed {
			if expectedTool == usedTool {
				used++
				break
			}
		}
	}

	score := float64(used) / float64(len(testCase.ExpectedTools))

	return []EvalScore{
		{
			Metric:  "tool_usage",
			Score:   score,
			Details: "checks if expected tools were used",
		},
	}, nil
}

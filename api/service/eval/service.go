// Package evaluation provides the public API for agent output evaluation.
package evaluation

import (
	"context"
	"fmt"

	internal "github.com/Timwood0x10/ares/internal/ares_eval"
)

// ExactMatch wraps the internal exact match evaluator.
type ExactMatch struct {
	inner *internal.ExactMatchEvaluator
}

// NewExactMatch creates an exact match evaluator.
func NewExactMatch() *ExactMatch {
	return &ExactMatch{inner: internal.NewExactMatchEvaluator()}
}

// Evaluate checks exact string match and returns scores using public types.
func (e *ExactMatch) Evaluate(ctx context.Context, testCase TestCase, result TestResult) ([]EvalScore, error) {
	itc := internal.TestCase{
		ID:             testCase.ID,
		Name:           testCase.Name,
		Input:          testCase.Input,
		ExpectedOutput: testCase.ExpectedOutput,
		ExpectedTools:  testCase.ExpectedTools,
		Metadata:       testCase.Metadata,
		Tags:           testCase.Tags,
	}
	itr := internal.TestResult{
		TestCaseID:   result.TestCaseID,
		ActualOutput: result.ActualOutput,
		ToolsUsed:    result.ToolsUsed,
		Duration:     result.Duration,
		TokensUsed:   result.TokensUsed,
		Error:        result.Error,
		Metrics:      result.Metrics,
		Timestamp:    result.Timestamp,
	}
	scores, err := e.inner.Evaluate(ctx, itc, itr)
	if err != nil {
		return nil, err
	}
	out := make([]EvalScore, len(scores))
	for i, s := range scores {
		out[i] = EvalScore{
			Metric:  s.Metric,
			Score:   s.Score,
			Details: s.Details,
		}
	}
	return out, nil
}

// Registry wraps the internal evaluator registry.
type Registry struct {
	inner *internal.EvaluatorRegistry
}

// NewRegistry creates a new evaluator registry.
func NewRegistry() *Registry {
	return &Registry{inner: internal.NewEvaluatorRegistry()}
}

// LLMJudge wraps the internal LLM judge evaluator.
type LLMJudge struct {
	inner *internal.LLMJudgeEvaluator
}

// NewLLMJudge creates an LLM judge evaluator.
func NewLLMJudge(client internal.LLMClient, opts ...internal.LLMJudgeOption) (*LLMJudge, error) {
	judge, err := internal.NewLLMJudgeEvaluator(client, opts...)
	if err != nil {
		return nil, fmt.Errorf("eval: create LLM judge: %w", err)
	}
	return &LLMJudge{inner: judge}, nil
}

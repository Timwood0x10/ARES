// Package evaluation provides the public API for agent output evaluation.
package evaluation

import (
	"context"

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

// Evaluate checks exact string match and returns scores.
func (e *ExactMatch) Evaluate(ctx context.Context, testCase internal.TestCase, result internal.TestResult) ([]internal.EvalScore, error) {
	return e.inner.Evaluate(ctx, testCase, result)
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
		return nil, err
	}
	return &LLMJudge{inner: judge}, nil
}

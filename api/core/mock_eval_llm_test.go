package core

import (
	"context"
)

// Ensure compile-time checks.
var _ Evaluator = (*mockEvaluator)(nil)
var _ EvaluatorRegistry = (*mockEvaluatorRegistry)(nil)
var _ LLMClient = (*mockLLMClient)(nil)

// ── mockEvaluator ───────────────────────────────────

type mockEvaluator struct{}

func (m *mockEvaluator) Evaluate(_ context.Context, _, _, _ string) (float64, error) { return 0, nil }

// ── mockEvaluatorRegistry ───────────────────────────

type mockEvaluatorRegistry struct{}

func (m *mockEvaluatorRegistry) Register(_ string, _ Evaluator) error { return nil }
func (m *mockEvaluatorRegistry) Get(_ string) Evaluator { return nil }

// ── mockLLMClient ───────────────────────────────────

type mockLLMClient struct{}

func (m *mockLLMClient) Generate(_ context.Context, _ string) (string, error) { return "", nil }

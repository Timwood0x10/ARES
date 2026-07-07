// Package evaluation tests.
package evaluation

import (
	"context"
	"testing"
)

func TestNewExactMatch(t *testing.T) {
	e := NewExactMatch()
	if e == nil {
		t.Fatal("expected non-nil evaluator")
	}
}

func TestExactMatchEvaluate(t *testing.T) {
	e := NewExactMatch()
	ctx := context.Background()

	tc := TestCase{
		ID:             "test-1",
		Input:          "hello",
		ExpectedOutput: "hello",
	}
	tr := TestResult{
		TestCaseID:   "test-1",
		ActualOutput: "hello",
	}
	scores, err := e.Evaluate(ctx, tc, tr)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(scores) == 0 {
		t.Fatal("expected at least one score")
	}
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
}

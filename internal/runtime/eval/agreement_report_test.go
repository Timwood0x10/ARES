package eval

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// withCapturedLog routes the package logger's output into buf for the duration of a
// test. The eval logger resolves slog.Default() at call time, so swapping the default
// is what makes the readout assertable — restoring it keeps other tests unaffected.
func withCapturedLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buf
}

// TestAgreementReportingEvaluator_ReportsOnChange is the readout's contract: the
// counters are logged when they MOVE, which is the only way a running deployment can
// tell a judge that agrees with the facts from one drifting away from them.
func TestAgreementReportingEvaluator_ReportsOnChange(t *testing.T) {
	buf := withCapturedLog(t)

	judge, err := NewLLMJudgeEvaluator(&mockLLMClient{response: maxCreditJudge}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	reporting := NewAgreementReportingEvaluator(judge)
	ctx := context.Background()

	// A lenient disagreement: ground truth fails the case, the judge loves it.
	if _, err := reporting.Evaluate(ctx, TestCase{
		ID: "gt-fail", Input: "q", ExpectedOutput: "Paris", Checks: []string{CheckExact},
	}, TestResult{ActualOutput: "Lyon"}); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "judge vs ground truth") {
		t.Fatalf("the readout must be logged when the counters move, got: %s", out)
	}
	for _, want := range []string{"anchored=1", "lenient=1", "harsh=0"} {
		if !strings.Contains(out, want) {
			t.Errorf("readout missing %q, got: %s", want, out)
		}
	}
}

// TestAgreementReportingEvaluator_QuietWhileCountersHold: a suite is scored case by
// case, so a line per case would bury the signal it exists to carry.
//
// The counters must be NON-ZERO for this to test the dedupe at all: with an all-zero
// state the earlier "nothing anchored yet is not news" guard short-circuits before the
// comparison, so a version that reported every case would still look quiet here. The
// first case anchors, the rest do not move the counters.
func TestAgreementReportingEvaluator_QuietWhileCountersHold(t *testing.T) {
	buf := withCapturedLog(t)

	judge, err := NewLLMJudgeEvaluator(&mockLLMClient{response: maxCreditJudge}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	reporting := NewAgreementReportingEvaluator(judge)
	ctx := context.Background()

	if _, err := reporting.Evaluate(ctx, TestCase{
		ID: "gt-fail", Input: "q", ExpectedOutput: "Paris", Checks: []string{CheckExact},
	}, TestResult{ActualOutput: "Lyon"}); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := strings.Count(buf.String(), "judge vs ground truth"); got != 1 {
		t.Fatalf("the first movement must be reported once, got %d line(s): %s", got, buf.String())
	}

	for i := 0; i < 3; i++ {
		if _, err := reporting.Evaluate(ctx, TestCase{ID: "undeclared", Input: "q"}, TestResult{ActualOutput: "a"}); err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
	}
	if got := strings.Count(buf.String(), "judge vs ground truth"); got != 1 {
		t.Errorf("an unmoved counter must not be re-reported, got %d line(s): %s", got, buf.String())
	}
}

// TestAgreementReportingEvaluator_PassesScoresThrough: the decorator reports, it never
// decides — a caller must get exactly the scores and error the inner evaluator gave.
func TestAgreementReportingEvaluator_PassesScoresThrough(t *testing.T) {
	_ = withCapturedLog(t)

	judge, err := NewLLMJudgeEvaluator(&mockLLMClient{response: judgeMaxCredit}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	direct, err := judge.Evaluate(context.Background(), TestCase{ID: "x", Input: "q"}, TestResult{ActualOutput: "a"})
	if err != nil {
		t.Fatalf("direct Evaluate: %v", err)
	}
	through, err := NewAgreementReportingEvaluator(judge).Evaluate(context.Background(), TestCase{ID: "x2", Input: "q"}, TestResult{ActualOutput: "a"})
	if err != nil {
		t.Fatalf("decorated Evaluate: %v", err)
	}
	if len(direct) != len(through) || direct[0].Score != through[0].Score {
		t.Errorf("decorator changed the verdict: direct %v vs through %v", direct, through)
	}
}

// TestNewAgreementReportingEvaluator_LeavesCounterlessEvaluatorsAlone: an evaluator
// with nothing to report must not be wrapped in a layer that can only hide its type.
func TestNewAgreementReportingEvaluator_LeavesCounterlessEvaluatorsAlone(t *testing.T) {
	inner := NewExactMatchEvaluator()
	if got := NewAgreementReportingEvaluator(inner); got != Evaluator(inner) {
		t.Errorf("a counter-less evaluator must be returned unwrapped, got %T", got)
	}
}

// TestCountAnchoredCases pins the suite-level verifiability count the gate reports at
// wiring time: cases that declared at least one deterministic check.
func TestCountAnchoredCases(t *testing.T) {
	suite := TestSuite{TestCases: []TestCase{
		{ID: "a", Checks: []string{CheckExact}},
		{ID: "b"},
		{ID: "c", Checks: []string{CheckTools, CheckNoError}},
	}}
	if got := CountAnchoredCases(suite); got != 2 {
		t.Errorf("CountAnchoredCases = %d, want 2", got)
	}
	if got := CountAnchoredCases(TestSuite{}); got != 0 {
		t.Errorf("empty suite = %d, want 0", got)
	}
}

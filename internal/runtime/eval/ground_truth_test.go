package eval

import (
	"context"
	"testing"
)

// judgeMaxCredit is a judge response that gives an answer EVERY point available:
// the shape that makes a leniency test mean something, because a judge at 0
// could not have been lenient whatever the ground truth said.
const judgeMaxCredit = `{"correctness": 3, "completeness": 3, "efficiency": 2, "safety": 2, "reason": "looks right"}`

// judgeRejectsCorrectness is a judge that denies correctness while still
// crediting the other three dimensions: the harsh direction.
const judgeRejectsCorrectness = `{"correctness": 0, "completeness": 3, "efficiency": 2, "safety": 2, "reason": "no"}`

// --- check-level units -------------------------------------------------------

func TestGroundTruthCheck_ExactComparesNormalized(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		actual   string
		want     string
	}{
		{"identical", "Paris", "Paris", StatusPassed},
		{"case and whitespace fold", "Paris", "  paris  ", StatusPassed},
		{"different answer fails", "Paris", "Lyon", StatusFailed},
		{"missing answer fails", "Paris", "", StatusFailed},
		// Exact means exact: a correct fact wrapped in prose is what CheckContains
		// is for, and conflating them would make "exact" measure nothing.
		{"prose containing the fact fails", "Paris", "The capital is Paris.", StatusFailed},
		{"no expectation is missing, not passed", "", "Paris", StatusMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exactCheck(TestCase{ExpectedOutput: tt.expected}, TestResult{ActualOutput: tt.actual})
			if got.Status != tt.want {
				t.Errorf("status = %q, want %q (detail: %s)", got.Status, tt.want, got.Detail)
			}
		})
	}
}

func TestGroundTruthCheck_ContainsFindsTheFactInProse(t *testing.T) {
	tc := TestCase{ExpectedOutput: "Canberra"}
	if got := containsCheck(tc, TestResult{ActualOutput: "The capital of Australia is Canberra."}); got.Status != StatusPassed {
		t.Errorf("prose answer should pass contains, got %q (%s)", got.Status, got.Detail)
	}
	// The plausible wrong answer must NOT pass: this is the whole point of the
	// adversarial cell in the corpus.
	if got := containsCheck(tc, TestResult{ActualOutput: "The capital of Australia is Sydney."}); got.Status != StatusFailed {
		t.Errorf("wrong city must fail contains, got %q", got.Status)
	}
	if got := containsCheck(TestCase{}, TestResult{ActualOutput: "x"}); got.Status != StatusMissing {
		t.Errorf("no expectation should be missing, got %q", got.Status)
	}
}

func TestGroundTruthCheck_Numeric(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		actual   string
		want     string
	}{
		{"exact figure", "56088", "56088", StatusPassed},
		{"thousands separator tolerated", "56088", "56,088", StatusPassed},
		// The answer is SCANNED for the figure, not required to be it: these are
		// the shapes a real model produces, and failing them would make the
		// accuracy signal wrong rather than thin.
		{"figure inside prose", "56088", "The answer is 56088.", StatusPassed},
		{"figure with a unit and a prefix", "56088", "约 56,088 元", StatusPassed},
		{"figure among working", "408", "17 * 24 = 408", StatusPassed},
		{"wrong figure fails", "56088", "56089", StatusFailed},
		// No number at all FAILS rather than going indeterminate: the case declared
		// the answer must contain this number, and handing such a case back to the
		// judge is the leniency this layer exists to remove.
		{"non-numeric answer fails", "56088", "about fifty-six thousand", StatusFailed},
		{"empty answer fails", "56088", "", StatusFailed},
		{"non-numeric expectation is a corpus bug", "fifty-six thousand", "56088", StatusMissing},
		// A number that merely appears in a date or a step counter does not count
		// as the answer unless it IS the answer — but a wrong one is still wrong.
		{"right figure mentioned while arguing against it passes", "56088", "the answer is 99999, not 56088", StatusPassed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := numericCheck(TestCase{ExpectedOutput: tt.expected}, TestResult{ActualOutput: tt.actual})
			if got.Status != tt.want {
				t.Errorf("status = %q, want %q (detail: %s)", got.Status, tt.want, got.Detail)
			}
		})
	}
}

func TestGroundTruthCheck_NumericTolerance(t *testing.T) {
	if !numericClose(56088, 56088.0000001) {
		t.Error("a float artifact must compare equal")
	}
	if numericClose(56088, 56089) {
		t.Error("a wrong integer must NOT compare equal")
	}
}

func TestGroundTruthCheck_Tools(t *testing.T) {
	tc := TestCase{ExpectedTools: []string{"calculator", "web_search"}}
	if got := toolsCheck(tc, TestResult{ToolsUsed: []string{"calculator", "web_search"}}); got.Status != StatusPassed {
		t.Errorf("all expected tools ran, want passed, got %q (%s)", got.Status, got.Detail)
	}
	// Every expected tool must run: partial credit would let a session keep its
	// anchor after skipping half the declared work.
	if got := toolsCheck(tc, TestResult{ToolsUsed: []string{"calculator"}}); got.Status != StatusFailed {
		t.Errorf("a missing tool must fail, got %q", got.Status)
	}
	if got := toolsCheck(tc, TestResult{}); got.Status != StatusFailed {
		t.Errorf("no tool ran at all must fail, got %q", got.Status)
	}
	if got := toolsCheck(TestCase{}, TestResult{ToolsUsed: []string{"x"}}); got.Status != StatusMissing {
		t.Errorf("no expected tools should be missing, got %q", got.Status)
	}
}

func TestGroundTruthCheck_NoError(t *testing.T) {
	if got := noErrorCheck(TestResult{}); got.Status != StatusPassed {
		t.Errorf("clean run should pass, got %q", got.Status)
	}
	if got := noErrorCheck(TestResult{Error: "boom"}); got.Status != StatusFailed {
		t.Errorf("failed run must fail, got %q", got.Status)
	}
}

func TestGroundTruthChecks_UnknownKindIsMissingNotPass(t *testing.T) {
	checks := groundTruthChecks(TestCase{ID: "tc", Checks: []string{"telepathy"}}, TestResult{ActualOutput: "anything"})
	if len(checks) != 1 || checks[0].Status != StatusMissing {
		t.Fatalf("unknown kind must resolve to missing (cannot prove success), got %+v", checks)
	}
	if KnownCheckKind("telepathy") {
		t.Error("UnknownCheckKind must report false for a kind this package cannot run")
	}
	// Declaring nothing is not the same as declaring something unresolvable, and
	// both must stay distinguishable: no checks at all produces no opinion.
	if got := groundTruthChecks(TestCase{ID: "tc"}, TestResult{}); got != nil {
		t.Errorf("an undeclared case must produce no checks, got %+v", got)
	}
}

func TestEvaluateGroundTruth_VerdictComposition(t *testing.T) {
	// One failed check out of two is still a FAIL (the bottom layer must prove
	// success) — not a 50% score that could be averaged into a pass.
	gt := evaluateGroundTruth(TestCase{
		ID:             "tc",
		ExpectedOutput: "Paris",
		Checks:         []string{CheckExact, CheckNoError},
	}, TestResult{ActualOutput: "Lyon"}, "tester")
	if !gt.Declared || gt.Verdict != VerdictFail || !gt.Anchored() {
		t.Fatalf("a failed check must anchor to Fail, got %+v (anchored=%v)", gt, gt.Anchored())
	}

	// Declared but unresolvable: indeterminate, and therefore NOT anchored — the
	// judge keeps the case and the anomaly is counted, not silently passed.
	broken := evaluateGroundTruth(TestCase{ID: "tc", Checks: []string{CheckExact}}, TestResult{}, "tester")
	if !broken.Declared || broken.Verdict != VerdictUncertain || broken.Anchored() {
		t.Fatalf("an unresolvable declaration must be unanchored uncertain, got %+v (anchored=%v)", broken, broken.Anchored())
	}

	// Undeclared: no opinion at all, and the zero value (VerdictPass) must never
	// leak out of that.
	none := evaluateGroundTruth(TestCase{ID: "tc"}, TestResult{}, "tester")
	if none.Declared || none.Verdict != VerdictUncertain {
		t.Fatalf("an undeclared case must hold no verdict, got %+v", none)
	}
}

// --- the floor, through the real scoring path --------------------------------

// TestEvaluateWithDimensions_DeclaredCheckFailureOverrulesTheJudge is the core
// contract of this layer: a declared check that FAILED cannot be lifted by a
// judge, however generous — and the scalar cannot be diluted either (zeroing the
// correctness rubric alone would leave a wrong answer at 0.75).
func TestEvaluateWithDimensions_DeclaredCheckFailureOverrulesTheJudge(t *testing.T) {
	e, err := NewLLMJudgeEvaluator(&mockLLMClient{response: judgeMaxCredit}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}

	scores, err := e.Evaluate(context.Background(), TestCase{
		ID:             "gt-fail",
		Input:          "What is the capital of France?",
		ExpectedOutput: "Paris",
		Checks:         []string{CheckExact},
	}, TestResult{ActualOutput: "Lyon"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := scores[0].Score; got != 0 {
		t.Errorf("a failed declared check must floor the score to 0, got %v (judge alone would have said 1.0)", got)
	}

	// And the disagreement is recorded, because that is the number that says how
	// much of the unanchored signal is judge opinion.
	stats := e.GroundTruthAgreement()
	if stats.Anchored != 1 || stats.Lenient != 1 {
		t.Errorf("a max-credit judge over a failing check must count as lenient, got %s", stats)
	}
	if rate := stats.LeniencyRate(); rate != 1 {
		t.Errorf("leniency rate = %v, want 1", rate)
	}
}

// TestEvaluateWithDimensions_PassingCheckKeepsTheJudgeScore pins the other half:
// this layer is a FLOOR, never a ceiling. A passing check must not hand out
// points the judge did not give — otherwise a corpus of easily-passed checks
// would be a free score, which is the same reward hacking from the other side.
func TestEvaluateWithDimensions_PassingCheckKeepsTheJudgeScore(t *testing.T) {
	e, err := NewLLMJudgeEvaluator(&mockLLMClient{response: `{"correctness": 3, "completeness": 2, "efficiency": 2, "safety": 2, "reason": "ok"}`}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	scores, err := e.Evaluate(context.Background(), TestCase{
		ID:             "gt-pass",
		Input:          "What is the capital of France?",
		ExpectedOutput: "Paris",
		Checks:         []string{CheckContains},
	}, TestResult{ActualOutput: "The capital of France is Paris."})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	// (1 + 0.667 + 1 + 1) / 4 = 0.917 — the judge's own number, untouched.
	if got := scores[0].Score; got != 0.917 {
		t.Errorf("a passing check must leave the judge score alone, got %v want 0.917", got)
	}
	stats := e.GroundTruthAgreement()
	if stats.Anchored != 1 || stats.Lenient != 0 || stats.Harsh != 0 {
		t.Errorf("a judge at the same verdict must not count as a disagreement, got %s", stats)
	}
}

// TestEvaluateWithDimensions_UndeclaredCaseIsUnchanged is the legacy-corpus
// guard: a case that declares nothing must score exactly as it did before this
// layer existed, so shipping it cannot retro-fit an existing suite.
func TestEvaluateWithDimensions_UndeclaredCaseIsUnchanged(t *testing.T) {
	e, err := NewLLMJudgeEvaluator(&mockLLMClient{response: judgeMaxCredit}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	scores, err := e.Evaluate(context.Background(), TestCase{
		ID:    "legacy",
		Input: "anything",
		// ExpectedOutput present but NO Checks: the declaration is what counts.
		ExpectedOutput: "Paris",
	}, TestResult{ActualOutput: "Lyon"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := scores[0].Score; got != 1.0 {
		t.Errorf("an undeclared case must keep the judge score, got %v want 1.0", got)
	}
	if stats := e.GroundTruthAgreement(); stats != (GroundTruthAgreement{}) {
		t.Errorf("an undeclared case must not enter the agreement counts, got %s", stats)
	}
}

// TestEvaluateWithDimensions_BrokenDeclarationKeepsTheJudgeAndIsCounted: a
// declaration the layer cannot resolve must not be scored as a pass, and must
// not silently disappear either.
func TestEvaluateWithDimensions_BrokenDeclarationKeepsTheJudgeAndIsCounted(t *testing.T) {
	e, err := NewLLMJudgeEvaluator(&mockLLMClient{response: judgeMaxCredit}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	scores, err := e.Evaluate(context.Background(), TestCase{
		ID:     "broken",
		Input:  "anything",
		Checks: []string{CheckExact}, // declares exact with nothing to compare against
	}, TestResult{ActualOutput: "whatever"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := scores[0].Score; got != 1.0 {
		t.Errorf("an unresolvable declaration must leave the judge score alone, got %v", got)
	}
	stats := e.GroundTruthAgreement()
	if stats.Broken != 1 || stats.Anchored != 0 {
		t.Errorf("an unresolvable declaration must count as broken, not anchored, got %s", stats)
	}
}

// TestEvaluateWithDimensions_FailedCheckBeatsTheTruncatedFloor pins the ordering
// between the two floors. "Produced the wrong thing while cut short" must not
// score above "produced nothing": the answer-checks floor is applied last and
// wins, because the stricter statement is the true one.
func TestEvaluateWithDimensions_FailedCheckBeatsTheTruncatedFloor(t *testing.T) {
	newJudge := func(t *testing.T) *LLMJudgeEvaluator {
		t.Helper()
		e, err := NewLLMJudgeEvaluator(&mockLLMClient{response: judgeMaxCredit}, WithDimensionAveraging())
		if err != nil {
			t.Fatalf("NewLLMJudgeEvaluator: %v", err)
		}
		return e
	}

	// Control: same truncated ending, nothing declared — the provenance floor is
	// the only one applied.
	control, err := newJudge(t).Evaluate(context.Background(), TestCase{ID: "control", Input: "q"}, TestResult{
		ActualOutput: "some answer",
		Truncated:    true,
	})
	if err != nil {
		t.Fatalf("Evaluate control: %v", err)
	}
	if control[0].Score <= 0 || control[0].Score >= 1 {
		t.Fatalf("control: a truncated ending should sit between 0 and 1, got %v", control[0].Score)
	}

	// Same ending, but a declared check fails: strictly lower, and floored to 0.
	failed, err := newJudge(t).Evaluate(context.Background(), TestCase{
		ID:             "failed",
		Input:          "q",
		ExpectedOutput: "the right answer",
		Checks:         []string{CheckExact},
	}, TestResult{
		ActualOutput: "the wrong answer",
		Truncated:    true,
	})
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if failed[0].Score != 0 {
		t.Errorf("a failed check after a truncated ending must floor to 0, got %v", failed[0].Score)
	}
	if failed[0].Score >= control[0].Score {
		t.Errorf("a wrong truncated answer (%v) must score below a merely truncated one (%v)", failed[0].Score, control[0].Score)
	}
}

func TestGroundTruthAgreement_CountsBothDirections(t *testing.T) {
	e, err := NewLLMJudgeEvaluator(&mockLLMClient{response: judgeMaxCredit}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	ctx := context.Background()

	// Lenient: ground truth fails, judge gives credit.
	if _, err := e.Evaluate(ctx, TestCase{
		ID: "lenient", ExpectedOutput: "Paris", Checks: []string{CheckExact},
	}, TestResult{ActualOutput: "Lyon"}); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	// Harsh: ground truth passes, judge denies correctness.
	harsh, err := NewLLMJudgeEvaluator(&mockLLMClient{response: judgeRejectsCorrectness}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	if _, err := harsh.Evaluate(ctx, TestCase{
		ID: "harsh", ExpectedOutput: "Paris", Checks: []string{CheckContains},
	}, TestResult{ActualOutput: "Paris"}); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	// Undeclared: not counted at all.
	if _, err := harsh.Evaluate(ctx, TestCase{ID: "legacy", Input: "q"}, TestResult{ActualOutput: "a"}); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	lenientStats := e.GroundTruthAgreement()
	if lenientStats.Anchored != 1 || lenientStats.Lenient != 1 || lenientStats.Harsh != 0 {
		t.Errorf("leniency half = %s, want anchored=1 lenient=1 harsh=0", lenientStats)
	}
	harshStats := harsh.GroundTruthAgreement()
	if harshStats.Anchored != 1 || harshStats.Lenient != 0 || harshStats.Harsh != 1 {
		t.Errorf("harshness half = %s, want anchored=1 lenient=0 harsh=1", harshStats)
	}
	if lenientStats.LeniencyRate() != 1 {
		t.Errorf("leniency rate = %v, want 1", lenientStats.LeniencyRate())
	}
}

func TestGroundTruthAgreement_EmptyRateIsZero(t *testing.T) {
	// No anchored case must read as 0, not as a division by zero, so the number
	// can be logged before any anchor exists.
	var empty GroundTruthAgreement
	if empty.LeniencyRate() != 0 {
		t.Errorf("empty leniency rate = %v, want 0", empty.LeniencyRate())
	}
}

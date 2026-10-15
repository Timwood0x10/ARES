package eval

import (
	"context"
	"testing"
)

// stubReplayExecutor answers each input from a fixed table and reports the tools
// it "used", so a replay can be exercised end to end without a runtime, a
// database or an LLM. Provenance is reported (the optional half), because that
// is what a real runtime-backed executor does.
type stubReplayExecutor struct {
	answers map[string]string
	tools   map[string][]string
}

func (e *stubReplayExecutor) Execute(_ context.Context, input string) (string, []string, int, error) {
	answer, ok := e.answers[input]
	if !ok {
		return "", nil, 0, nil // no answer: the run records a gap-shaped result
	}
	return answer, e.tools[input], 0, nil
}

func (e *stubReplayExecutor) AnswerProvenance() (string, bool) { return "", false }

func newStubReplayRunner(t *testing.T, executor *stubReplayExecutor) *AgentTestRunner {
	t.Helper()
	runner, err := NewAgentTestRunner(executor)
	if err != nil {
		t.Fatalf("NewAgentTestRunner: %v", err)
	}
	return runner
}

// maxCreditJudge is a judge that finds nothing wrong — which is exactly what makes
// it the right probe here: without the deterministic floor it hands a perfect
// score to a wrong answer.
const maxCreditJudge = `{"correctness": 3, "completeness": 3, "efficiency": 2, "safety": 2, "reason": "looks right"}`

// TestReplayGolden_RunsTheCorpusAndFloorsIt is the driver contract: a golden suite
// executes, the deterministic layer floors the case it fails, the case it passes
// keeps the judge's own number, and the run reports how much of itself was
// verifiable.
func TestReplayGolden_RunsTheCorpusAndFloorsIt(t *testing.T) {
	executor := &stubReplayExecutor{answers: map[string]string{
		"capital of France?": "The capital of France is Paris.",
		"capital of Japan?":  "Kyoto",
		"open question":      "a confident but unverifiable paragraph",
	}}
	runner := newStubReplayRunner(t, executor)

	judge, err := NewLLMJudgeEvaluator(&mockLLMClient{response: maxCreditJudge}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}

	suite := TestSuite{Name: "golden", TestCases: []TestCase{
		// Anchored and correct: contains "Paris" → the judge's score stands.
		{ID: "g1", Input: "capital of France?", ExpectedOutput: "Paris", Checks: []string{CheckContains}},
		// Anchored and WRONG: exact "Tokyo" but the answer is Kyoto → floored to 0.
		{ID: "g2", Input: "capital of Japan?", ExpectedOutput: "Tokyo", Checks: []string{CheckExact}},
		// Undeclared: judge-only, exactly as before this layer existed.
		{ID: "g3", Input: "open question"},
	}}

	outcome, err := ReplayGolden(context.Background(), runner, judge, suite, ReplayOptions{})
	if err != nil {
		t.Fatalf("ReplayGolden: %v", err)
	}
	if len(outcome.Cases) != 3 {
		t.Fatalf("replayed %d cases, want 3", len(outcome.Cases))
	}

	byID := make(map[string]ReplayCase, 3)
	for _, c := range outcome.Cases {
		byID[c.Case.ID] = c
		if c.Err != nil {
			t.Errorf("case %s reported %v, want a clean run", c.Case.ID, c.Err)
		}
		if !c.HasScore {
			t.Errorf("case %s produced no score", c.Case.ID)
		}
	}

	if byID["g2"].Score != 0 {
		t.Errorf("a case the ground truth FAILED scored %v, want 0 even from a max-credit judge", byID["g2"].Score)
	}
	if !byID["g2"].Anchored || byID["g2"].GroundTruth != VerdictFail {
		t.Errorf("g2 verdict = %v (anchored=%v), want Fail and anchored", byID["g2"].GroundTruth, byID["g2"].Anchored)
	}
	if byID["g1"].Score <= 0 {
		t.Errorf("a case the ground truth PASSED scored %v, want the judge's own number", byID["g1"].Score)
	}
	if byID["g3"].Anchored {
		t.Error("an undeclared case must not be reported as anchored")
	}

	// The share is the honest headline: 2 of 3 cases were verifiable.
	if outcome.AnchoredShare < 0.66 || outcome.AnchoredShare > 0.67 {
		t.Errorf("AnchoredShare = %v, want 2/3", outcome.AnchoredShare)
	}
	// Coverage travels with the Mean, so a Mean over a fraction of the corpus can
	// never be read as a Mean over the corpus.
	if outcome.Scored != 3 || outcome.Total != 3 {
		t.Errorf("scored/total = %d/%d, want 3/3", outcome.Scored, outcome.Total)
	}
	if outcome.Coverage() != 1 {
		t.Errorf("Coverage = %v, want 1", outcome.Coverage())
	}
	// And the judge disagreed with the ground truth on exactly the case it failed.
	if outcome.Agreement.Anchored != 2 || outcome.Agreement.Lenient != 1 {
		t.Errorf("agreement = %s, want anchored=2 lenient=1", outcome.Agreement)
	}
}

// TestReplayGolden_HonoursTheLimit pins comparability: the subset is the first N of
// the suite, never a sample, or two candidates cannot be compared at all.
func TestReplayGolden_HonoursTheLimit(t *testing.T) {
	executor := &stubReplayExecutor{answers: map[string]string{"q1": "a1", "q2": "a2", "q3": "a3"}}
	runner := newStubReplayRunner(t, executor)
	judge, err := NewLLMJudgeEvaluator(&mockLLMClient{response: maxCreditJudge}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	suite := TestSuite{Name: "golden", TestCases: []TestCase{
		{ID: "g1", Input: "q1"},
		{ID: "g2", Input: "q2"},
		{ID: "g3", Input: "q3"},
	}}

	outcome, err := ReplayGolden(context.Background(), runner, judge, suite, ReplayOptions{Limit: 2})
	if err != nil {
		t.Fatalf("ReplayGolden: %v", err)
	}
	if len(outcome.Cases) != 2 {
		t.Fatalf("replayed %d cases, want the first 2", len(outcome.Cases))
	}
	if outcome.Cases[0].Case.ID != "g1" || outcome.Cases[1].Case.ID != "g2" {
		t.Errorf("limit must keep the FIRST cases in order, got %s,%s", outcome.Cases[0].Case.ID, outcome.Cases[1].Case.ID)
	}
}

// TestReplayGolden_RecordsPerCaseFailuresWithoutAborting: one broken case must not
// discard the cases that did run.
func TestReplayGolden_RecordsPerCaseFailuresWithoutAborting(t *testing.T) {
	executor := &stubReplayExecutor{answers: map[string]string{"q1": "a1"}}
	runner := newStubReplayRunner(t, executor)
	judge, err := NewLLMJudgeEvaluator(&mockLLMClient{response: maxCreditJudge}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	suite := TestSuite{Name: "golden", TestCases: []TestCase{
		{ID: "g1", Input: "q1"},
		{ID: "g2", Input: "unanswered by the stub"},
	}}

	outcome, err := ReplayGolden(context.Background(), runner, judge, suite, ReplayOptions{})
	if err != nil {
		t.Fatalf("ReplayGolden must not abort on a case failure: %v", err)
	}
	if len(outcome.Cases) != 2 {
		t.Fatalf("replayed %d cases, want both reported", len(outcome.Cases))
	}
	if outcome.Cases[0].Err != nil {
		t.Errorf("the answered case must be clean, got %v", outcome.Cases[0].Err)
	}
}

// TestReplayGolden_RejectsMissingDependencies: a driver handed nothing must say so
// rather than reporting an empty, green-looking replay.
func TestReplayGolden_RejectsMissingDependencies(t *testing.T) {
	runner := newStubReplayRunner(t, &stubReplayExecutor{})
	judge, err := NewLLMJudgeEvaluator(&mockLLMClient{response: maxCreditJudge}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}
	if _, err := ReplayGolden(context.Background(), nil, judge, TestSuite{}, ReplayOptions{}); err == nil {
		t.Error("a nil runner must be rejected")
	}
	if _, err := ReplayGolden(context.Background(), runner, nil, TestSuite{}, ReplayOptions{}); err == nil {
		t.Error("a nil judge must be rejected")
	}
}

// TestReplayGolden_ReplaysTheShippedCorpusShape is a wiring guard on the corpus
// itself: the driver must be able to run what is in test/eval, not only a
// hand-built suite. Every case is answered with its own expectation, so the run
// is the corpus's best case; a corpus edit that the driver cannot execute fails
// here.
func TestReplayGolden_ReplaysTheShippedCorpusShape(t *testing.T) {
	suites, err := NewLoader().LoadDir("../../../test/eval")
	if err != nil {
		t.Fatalf("load test/eval: %v", err)
	}
	var golden *TestSuite
	for i := range suites {
		if suites[i].Name == "Golden Replay Corpus" {
			golden = &suites[i]
			break
		}
	}
	if golden == nil {
		t.Fatal("the golden corpus must exist: the driver has nothing to replay otherwise")
	}

	executor := &stubReplayExecutor{answers: map[string]string{}}
	for _, tc := range golden.TestCases {
		executor.answers[tc.Input] = tc.ExpectedOutput
	}
	runner := newStubReplayRunner(t, executor)
	judge, err := NewLLMJudgeEvaluator(&mockLLMClient{response: maxCreditJudge}, WithDimensionAveraging())
	if err != nil {
		t.Fatalf("NewLLMJudgeEvaluator: %v", err)
	}

	outcome, err := ReplayGolden(context.Background(), runner, judge, *golden, ReplayOptions{})
	if err != nil {
		t.Fatalf("ReplayGolden over the shipped corpus: %v", err)
	}
	if len(outcome.Cases) != len(golden.TestCases) {
		t.Fatalf("replayed %d of %d corpus cases", len(outcome.Cases), len(golden.TestCases))
	}
	for _, c := range outcome.Cases {
		if c.Err != nil {
			t.Errorf("corpus case %s failed: %v", c.Case.ID, c.Err)
		}
	}
	// Answered with its own expectation, every declared check must anchor — a
	// corpus edit whose checks the driver cannot resolve would silently lose the
	// case's anchor instead.
	for _, c := range outcome.Cases {
		if !c.Anchored && len(c.Case.Checks) > 0 {
			t.Errorf("corpus case %s replayed unanchored despite declaring %v", c.Case.ID, c.Case.Checks)
		}
	}
	if outcome.AnchoredShare < 0.9 {
		t.Errorf("AnchoredShare = %v, want nearly the whole corpus anchored", outcome.AnchoredShare)
	}
}

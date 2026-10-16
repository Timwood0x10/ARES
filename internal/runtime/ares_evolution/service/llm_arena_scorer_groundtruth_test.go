package evolution

import (
	"context"
	"strings"
	"testing"

	ares_arena "github.com/Timwood0x10/ares/internal/runtime/arena"
)

// TestArenaCase_StringRendersOnlyTheInput is a prompt-compatibility contract: cases
// reach both prompts through fmt's %v, so String() is what keeps the reference answer
// out of the grading prompt. Seeding the grader with the expected answer would change
// what it is asked — and the repository measured that a stricter grading prompt
// inflates variance and destroys the significance test (see DefaultArenaEvalPrompt).
func TestArenaCase_StringRendersOnlyTheInput(t *testing.T) {
	c := ArenaCase{Input: "What is 2+2?", ExpectedOutput: "4", Checks: []string{"numeric"}}
	if got := c.String(); got != "What is 2+2?" {
		t.Errorf("String() = %q, want the input only", got)
	}
	if strings.Contains(c.String(), "4") && !strings.Contains(c.Input, "4") {
		t.Error("String() must not leak the expected answer into the prompt")
	}
}

// TestGroundTruthFloor_LowersOnlyOnAFailedCheck is the arena-side floor, tested where
// it is pure: a declared check the output FAILS caps the score at 0 regardless of what
// the grader said, and nothing else moves the score.
func TestGroundTruthFloor_LowersOnlyOnAFailedCheck(t *testing.T) {
	wrongAnswer := ArenaCase{Input: "capital of Japan?", ExpectedOutput: "Tokyo", Checks: []string{"exact"}}
	rightAnswer := ArenaCase{Input: "capital of Japan?", ExpectedOutput: "Tokyo", Checks: []string{"contains"}}

	tests := []struct {
		name      string
		caseValue any
		output    string
		grader    float64
		want      float64
	}{
		{"a wrong anchored answer is floored even when the grader loved it", wrongAnswer, "Kyoto", 1.0, 0},
		{"a right anchored answer keeps the grader's score", rightAnswer, "The capital of Japan is Tokyo.", 0.8, 0.8},
		{"an unanchored case keeps the grader's score", "open question", "anything", 0.7, 0.7},
		{"a bare string case (legacy wiring) keeps its score", "what is 2+2?", "4", 0.9, 0.9},
		// A declared but unresolvable check is a corpus bug, not a wrong answer: it must
		// not silently zero a candidate.
		{"an unresolvable check does not floor", ArenaCase{Input: "q", Checks: []string{"no_such_check"}}, "a", 0.6, 0.6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := groundTruthFloor(tc.caseValue, tc.output, tc.grader); got != tc.want {
				t.Errorf("groundTruthFloor = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestScoreBatch_AppliesTheDeterministicFloor is the production-path proof: a real run
// uses the batch path (2 LLM calls instead of 2×count), so a floor that lived only in
// the single-case method would never fire. The stub returns a glowing grade for both
// cases; the anchored one must still come back 0.
func TestScoreBatch_AppliesTheDeterministicFloor(t *testing.T) {
	client := funcLLMClient(func(_ context.Context, prompt string) (string, error) {
		if strings.Contains(prompt, "rate how well") {
			// The grader is fond of both answers.
			return "1.0\n0.8", nil
		}
		// Execution: the first case answers WRONG on purpose.
		return "Kyoto\nsomething reasonable", nil
	})
	scorer, err := NewLLMArenaScorer(LLMArenaScorerConfig{Client: client})
	if err != nil {
		t.Fatalf("NewLLMArenaScorer: %v", err)
	}

	cases := []any{
		ArenaCase{Input: "capital of Japan?", ExpectedOutput: "Tokyo", Checks: []string{"exact"}},
		ArenaCase{Input: "summarise this document", Checks: nil}, // open-ended: grader governs
	}
	scores, err := scorer.ScoreBatch(context.Background(), "be helpful", 2, cases)
	if err != nil {
		t.Fatalf("ScoreBatch: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("got %d scores, want 2", len(scores))
	}
	if scores[0] != 0 {
		t.Errorf("the anchored wrong answer scored %v, want 0 despite the grader's 1.0", scores[0])
	}
	if scores[1] != 0.8 {
		t.Errorf("the unanchored case scored %v, want the grader's 0.8", scores[1])
	}
}

// TestScore_SingleCaseAppliesTheFloor keeps the two paths consistent: the single-case
// method must floor the same way, or a caller that scores case by case would get a
// different verdict for the same wrong answer.
func TestScore_SingleCaseAppliesTheFloor(t *testing.T) {
	client := funcLLMClient(func(_ context.Context, prompt string) (string, error) {
		if strings.Contains(prompt, "rate how well") {
			return "1.0", nil
		}
		return "Kyoto", nil
	})
	scorer, err := NewLLMArenaScorer(LLMArenaScorerConfig{Client: client})
	if err != nil {
		t.Fatalf("NewLLMArenaScorer: %v", err)
	}

	score, err := scorer.Score(context.Background(), ares_arena.TestCaseInput{
		Strategy: "be helpful",
		TestCase: ArenaCase{Input: "capital of Japan?", ExpectedOutput: "Tokyo", Checks: []string{"exact"}},
	})
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score != 0 {
		t.Errorf("score = %v, want 0 for an anchored wrong answer", score)
	}
}

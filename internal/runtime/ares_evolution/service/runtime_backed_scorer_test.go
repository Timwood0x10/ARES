package evolution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ares_arena "github.com/Timwood0x10/ares/internal/runtime/arena"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// stubArenaExecutor stands in for a real per-candidate runtime: it records what it was
// asked to execute and returns a scripted output per call.
type stubArenaExecutor struct {
	outputs []string
	calls   []struct {
		strategy     any
		instructions string
		caseText     string
	}
}

func (e *stubArenaExecutor) Execute(_ context.Context, strategy any, instructions, caseText string) (string, error) {
	e.calls = append(e.calls, struct {
		strategy     any
		instructions string
		caseText     string
	}{strategy, instructions, caseText})
	i := len(e.calls) - 1
	if i >= len(e.outputs) {
		i = len(e.outputs) - 1
	}
	return e.outputs[i], nil
}

// gradingOnlyClient answers grading prompts with a fixed score and fails the test if it is
// ever asked to ROLE-PLAY the agent (the execution step this path replaces).
func gradingOnlyClient(t *testing.T, score string) funcLLMClient {
	t.Helper()
	return funcLLMClient(func(_ context.Context, prompt string) (string, error) {
		if isExecPrompt(prompt) || isBatchExecPrompt(prompt) {
			t.Error("the runtime-backed scorer must not ask the LLM to act as the agent")
		}
		return score, nil
	})
}

// TestRuntimeBackedScorer_ExecutesForRealAndStillGrades is the P0-1 seam: with an executor
// installed, the answer comes from the runner and the LLM is left with grading only.
//
// The strategy must arrive WHOLE: a runtime honours the candidate's declared tool
// whitelist, which a prompt template cannot carry.
func TestRuntimeBackedScorer_ExecutesForRealAndStillGrades(t *testing.T) {
	exec := &stubArenaExecutor{outputs: []string{"4"}}
	s, err := NewRuntimeBackedArenaScorer(
		LLMArenaScorerConfig{Client: gradingOnlyClient(t, "0.9")}, exec)
	require.NoError(t, err)

	cand := &mutation.Strategy{
		ID:             "cand",
		PromptTemplate: "be terse",
		Params:         map[string]any{"tools": "calculator"},
	}
	score, err := s.Score(context.Background(), ares_arena.TestCaseInput{Strategy: cand, TestCase: "add 2 and 2"})
	require.NoError(t, err)
	assert.Equal(t, 0.9, score, "grading is untouched: only execution changed")

	require.Len(t, exec.calls, 1, "the runner must execute exactly once per Score")
	assert.Equal(t, "be terse", exec.calls[0].instructions)
	assert.Equal(t, "add 2 and 2", exec.calls[0].caseText)

	whole, ok := exec.calls[0].strategy.(*mutation.Strategy)
	require.True(t, ok, "the strategy must arrive whole (got %T) — a runtime needs Params", exec.calls[0].strategy)
	assert.Equal(t, "calculator", whole.Params["tools"], "the tool whitelist must survive to the runner")
}

// TestRuntimeBackedScorer_AppliesTheDeterministicFloor: the floor is part of the shared
// judge, so switching the execution step must not lose it — otherwise a candidate that
// gets the answer WRONG could still score well on a generous grader.
func TestRuntimeBackedScorer_AppliesTheDeterministicFloor(t *testing.T) {
	exec := &stubArenaExecutor{outputs: []string{"5"}} // the case wants 4
	s, err := NewRuntimeBackedArenaScorer(
		LLMArenaScorerConfig{Client: gradingOnlyClient(t, "0.9")}, exec)
	require.NoError(t, err)

	score, err := s.Score(context.Background(), ares_arena.TestCaseInput{
		Strategy: "be terse",
		TestCase: ArenaCase{Input: "what is 2+2?", ExpectedOutput: "4", Checks: []string{"numeric"}},
	})
	require.NoError(t, err)
	assert.Zero(t, score, "a failed ground-truth check pins the score to 0 regardless of the grader")
}

// TestRuntimeBackedScorer_BatchRunsOncePerCase: a runtime runs one case per call, so the
// batch path must NOT fall back to the role-play economy — it executes count times, which
// is the cost the wiring reports at startup.
func TestRuntimeBackedScorer_BatchRunsOncePerCase(t *testing.T) {
	exec := &stubArenaExecutor{outputs: []string{"a", "b", "c"}}
	s, err := NewRuntimeBackedArenaScorer(
		LLMArenaScorerConfig{Client: gradingOnlyClient(t, "0.5")}, exec)
	require.NoError(t, err)

	scores, err := s.ScoreBatch(context.Background(), "be terse", 3, []any{"c1", "c2"})
	require.NoError(t, err)
	assert.Len(t, scores, 3)
	require.Len(t, exec.calls, 3, "one execution per case: a runtime cannot answer three cases in one call")

	// The suite cycles, exactly as the LLM batch path does.
	assert.Equal(t, "c1", exec.calls[0].caseText)
	assert.Equal(t, "c2", exec.calls[1].caseText)
	assert.Equal(t, "c1", exec.calls[2].caseText)
}

// TestNewRuntimeBackedArenaScorer_RefusesNoExecutor: a "runtime-backed" scorer without a
// runner would silently be the role-play scorer, and the flag that turned it on would
// read as "we are replaying" while nothing was.
func TestNewRuntimeBackedArenaScorer_RefusesNoExecutor(t *testing.T) {
	_, err := NewRuntimeBackedArenaScorer(LLMArenaScorerConfig{Client: gradingOnlyClient(t, "0.5")}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires an executor")
}

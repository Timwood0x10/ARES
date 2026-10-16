package agentruntime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents"
)

func names(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, n := range in {
		out[n] = true
	}
	return out
}

// TestSandboxCandidateTools_CannotWidenTheCeiling: the ceiling is a maximum. A candidate
// that asks for a mutating tool keeps everything else it asked for and simply does not
// get that one — the thing under evaluation may not decide how much of the environment
// it is allowed to touch.
func TestSandboxCandidateTools_CannotWidenTheCeiling(t *testing.T) {
	candidate := &agents.ActiveStrategy{
		ID:     "cand-1",
		Prompt: "be thorough",
		Params: map[string]any{agents.ParamKeyTools: "calculator,file_tools,code_runner"},
	}

	sandboxed, err := SandboxCandidateTools(candidate, EvaluationToolCeiling)
	require.NoError(t, err)

	got := agents.ToolNamesFromParams(sandboxed.Params)
	assert.Equal(t, []string{"calculator"}, got, "only the in-ceiling tool survives")
	assert.False(t, names(got)["file_tools"], "a mutating tool must not pass the ceiling")
	assert.False(t, names(got)["code_runner"], "arbitrary code execution must not pass the ceiling")
}

// TestSandboxCandidateTools_NoWhitelistGetsTheCeiling: a candidate that says nothing about
// tools must NOT inherit the production default ("all tools advertised"), which is what a
// missing whitelist would otherwise mean.
func TestSandboxCandidateTools_NoWhitelistGetsTheCeiling(t *testing.T) {
	candidate := &agents.ActiveStrategy{ID: "cand-2", Prompt: "be terse"}

	sandboxed, err := SandboxCandidateTools(candidate, EvaluationToolCeiling)
	require.NoError(t, err)
	assert.Equal(t, EvaluationToolCeiling, agents.ToolNamesFromParams(sandboxed.Params))

	// The caller's strategy is untouched: it is usually the object the evolution path
	// would go on to promote, and evaluation-only restrictions must not leak there.
	assert.Nil(t, candidate.Params, "SandboxCandidateTools mutated the caller's strategy")
}

// TestSandboxCandidateTools_KeepsASubset: asking for less is allowed and preserved, so a
// candidate that deliberately runs with one tool is evaluated as such.
func TestSandboxCandidateTools_KeepsASubset(t *testing.T) {
	candidate := &agents.ActiveStrategy{
		ID:     "cand-3",
		Params: map[string]any{agents.ParamKeyTools: "hash_tool, calculator ,hash_tool"},
	}

	sandboxed, err := SandboxCandidateTools(candidate, EvaluationToolCeiling)
	require.NoError(t, err)
	assert.Equal(t, []string{"hash_tool", "calculator"}, agents.ToolNamesFromParams(sandboxed.Params))
}

// TestSandboxCandidateTools_RefusesWhenNothingSurvives is the trap this function exists to
// close. The whitelist encoding reads an empty value as "NO FILTER — advertise every
// tool", so quietly sandboxing to nothing would hand the candidate the WIDEST boundary in
// the system as a reward for asking for a forbidden one. It must refuse instead.
func TestSandboxCandidateTools_RefusesWhenNothingSurvives(t *testing.T) {
	candidate := &agents.ActiveStrategy{
		ID:     "cand-4",
		Params: map[string]any{agents.ParamKeyTools: "file_tools,code_runner"},
	}

	sandboxed, err := SandboxCandidateTools(candidate, EvaluationToolCeiling)
	require.Error(t, err)
	assert.Nil(t, sandboxed)
	assert.Contains(t, err.Error(), "file_tools")
	assert.Contains(t, err.Error(), "no filter", "the refusal must explain why an empty list is not an option")
}

// TestSandboxCandidateTools_RefusesAnEmptyCeiling: an empty ceiling would refuse every
// candidate, which reads like a per-candidate bug instead of a misconfigured harness.
func TestSandboxCandidateTools_RefusesAnEmptyCeiling(t *testing.T) {
	_, err := SandboxCandidateTools(&agents.ActiveStrategy{ID: "c"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ceiling is empty")
}

// TestNewCandidateRuntime_InstallsTheToolCeiling: the factory applies the sandbox itself,
// so a fence does not depend on the caller remembering to install it, and a candidate
// whose tools are all outside the ceiling cannot be assembled at all.
func TestNewCandidateRuntime_InstallsTheToolCeiling(t *testing.T) {
	t.Run("default ceiling for a silent candidate", func(t *testing.T) {
		rt, err := NewCandidateRuntime(candidateFixture(nil), &agents.ActiveStrategy{ID: "cand-a"})
		require.NoError(t, err)
		defer rt.Close()
		assert.Equal(t, EvaluationToolCeiling, rt.Tools())
	})

	t.Run("candidate subset is preserved", func(t *testing.T) {
		rt, err := NewCandidateRuntime(candidateFixture(nil), &agents.ActiveStrategy{
			ID:     "cand-b",
			Params: map[string]any{agents.ParamKeyTools: "calculator"},
		})
		require.NoError(t, err)
		defer rt.Close()
		assert.Equal(t, []string{"calculator"}, rt.Tools())
	})

	t.Run("a wholly forbidden tool set is refused", func(t *testing.T) {
		rt, err := NewCandidateRuntime(candidateFixture(nil), &agents.ActiveStrategy{
			ID:     "cand-c",
			Params: map[string]any{agents.ParamKeyTools: "file_tools"},
		})
		require.Error(t, err)
		assert.Nil(t, rt)
	})

	t.Run("an explicit tighter ceiling is honoured", func(t *testing.T) {
		cfg := candidateFixture(nil)
		cfg.ToolCeiling = []string{"hash_tool"}
		rt, err := NewCandidateRuntime(cfg, &agents.ActiveStrategy{
			ID:     "cand-d",
			Params: map[string]any{agents.ParamKeyTools: "hash_tool,calculator"},
		})
		require.NoError(t, err)
		defer rt.Close()
		assert.Equal(t, []string{"hash_tool"}, rt.Tools(), "the caller's tighter ceiling wins")
	})
}

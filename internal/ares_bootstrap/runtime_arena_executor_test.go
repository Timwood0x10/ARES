package ares_bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// newTestRuntimeExecutor builds the executor over a REAL candidate-runtime factory (the
// factory's own tests cover its refusal paths), so these tests exercise the caching and
// the candidate conversion rather than a stubbed factory.
func newTestRuntimeExecutor(t *testing.T) *RuntimeArenaExecutor {
	t.Helper()
	exec, err := NewRuntimeArenaExecutor(context.Background(), testReplayProvider(t), nil)
	require.NoError(t, err)
	t.Cleanup(exec.Close)
	return exec
}

// testReplayProvider is the provider a fully wired process would install: it builds the
// factory from the two things only the process has (a chat client and the serving tool
// binder).
func testReplayProvider(t *testing.T) func() (*CandidateReplayFactory, error) {
	t.Helper()
	return func() (*CandidateReplayFactory, error) {
		return NewCandidateReplayFactory(CandidateReplayConfig{
			ChatClient: replayChatStub{},
			ToolBinder: replayBinderStub{},
		})
	}
}

// TestRuntimeArenaExecutor_ResolvesTheFactoryOnFirstUse is the boot-ordering contract: the
// provider must NOT be called at construction, because at construction time the serving
// tool binder does not exist yet (cmd/ares wires it after the evolution components).
//
// It must also be called at most once: the factory holds no per-case state, so resolving it
// per case would rebuild the same thing forever.
func TestRuntimeArenaExecutor_ResolvesTheFactoryOnFirstUse(t *testing.T) {
	calls := 0
	inner := testReplayProvider(t)
	exec, err := NewRuntimeArenaExecutor(context.Background(), func() (*CandidateReplayFactory, error) {
		calls++
		return inner()
	}, nil)
	require.NoError(t, err)
	defer exec.Close()

	assert.Zero(t, calls, "construction must not resolve the factory: the tool binder is wired later")

	cand := &mutation.Strategy{ID: "c1", PromptTemplate: "be terse"}
	_, err = exec.runtimeFor(cand, cand.PromptTemplate)
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "the first use resolves it")

	other := &mutation.Strategy{ID: "c2", PromptTemplate: "be thorough"}
	_, err = exec.runtimeFor(other, other.PromptTemplate)
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "a second candidate must reuse the resolved factory")
}

// TestRuntimeArenaExecutor_ProviderFailureIsLoud: a provider that cannot produce a factory
// must fail the case it was needed for — silently scoring 0 would look like a bad candidate.
func TestRuntimeArenaExecutor_ProviderFailureIsLoud(t *testing.T) {
	exec, err := NewRuntimeArenaExecutor(context.Background(), func() (*CandidateReplayFactory, error) {
		return nil, errors.New("no tool binder in this process")
	}, nil)
	require.NoError(t, err)
	defer exec.Close()

	cand := &mutation.Strategy{ID: "c1", PromptTemplate: "be terse"}
	_, err = exec.runtimeFor(cand, cand.PromptTemplate)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tool binder in this process")

	_, err = NewRuntimeArenaExecutor(context.Background(), nil, nil)
	require.Error(t, err, "a nil provider can never be honoured")
	assert.Contains(t, err.Error(), "factory provider")
}

// TestRuntimeArenaExecutor_OneRuntimePerCandidate is the cost contract: a runtime owns a
// pair of fabrics and a drain loop, so it must be built once per candidate — not once per
// case — and a second candidate must NOT share the first one's environment.
func TestRuntimeArenaExecutor_OneRuntimePerCandidate(t *testing.T) {
	exec := newTestRuntimeExecutor(t)

	candA := &mutation.Strategy{ID: "cand-a", PromptTemplate: "be terse"}
	candB := &mutation.Strategy{ID: "cand-b", PromptTemplate: "be thorough"}

	a1, err := exec.runtimeFor(candA, candA.PromptTemplate)
	require.NoError(t, err)
	a2, err := exec.runtimeFor(candA, candA.PromptTemplate)
	require.NoError(t, err)
	assert.Same(t, a1, a2, "the same candidate must reuse its runtime across cases")

	b, err := exec.runtimeFor(candB, candB.PromptTemplate)
	require.NoError(t, err)
	assert.NotSame(t, a1, b, "two candidates must never share one runtime")

	assert.Len(t, exec.runtimes, 2, "one cached runtime per candidate")
	// Fabric-level isolation is asserted where the fabrics are reachable:
	// agentruntime's own isolation test compares the pointers the factory handed its
	// executors ("two candidates must never share a task fabric"). Here the fact that
	// the two runtimes are distinct values is the reachable half.
}

// TestRuntimeArenaExecutor_ExecuteReportsAnUnanswerableCase is the seam test for the method
// the arena scorer actually calls.
//
// The happy path needs a scripted cognition inside the runtime (the factory builds real
// executors), so it is covered by the integration-tagged live test; what this pins is the
// invariant that matters when a case cannot answer: Execute must return an ERROR, never an
// empty answer. The scorer would happily grade "" as a wrong answer, so a runtime that
// stalled would read as a candidate that answered badly — a measurement that never happened,
// reported as a low score.
func TestRuntimeArenaExecutor_ExecuteReportsAnUnanswerableCase(t *testing.T) {
	exec := newTestRuntimeExecutor(t)

	cand := &mutation.Strategy{ID: "cand-exec", PromptTemplate: "be terse"}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	answer, err := exec.Execute(ctx, cand, cand.PromptTemplate, "what is 2+2?")
	require.Error(t, err, "a case with nothing draining it must be an error, not an empty answer")
	assert.Empty(t, answer, "no answer text may accompany the failure")

	// The attempt still resolved and cached a runtime for this candidate: the failure is in
	// the case, not in the wiring.
	assert.Contains(t, exec.runtimes, "cand-exec")
}

// TestRuntimeArenaExecutor_BoundsWhatItKeeps: a rolling deployment compares candidates
// indefinitely, so "one runtime per candidate ever" is unbounded growth — each entry holds
// a scheduler loop and two fabrics. The executor keeps only what is still being compared
// and releases the rest.
//
// The assertion covers what is reachable from here (the bound and which candidate was
// dropped); that Close actually stops a runtime's loop is asserted in agentruntime
// (TestCandidateRuntime_CloseStopsTheLoop), where the scheduler is visible.
func TestRuntimeArenaExecutor_BoundsWhatItKeeps(t *testing.T) {
	exec := newTestRuntimeExecutor(t)

	for _, id := range []string{"c1", "c2", "c3"} {
		c := &mutation.Strategy{ID: id, PromptTemplate: "be terse"}
		_, err := exec.runtimeFor(c, c.PromptTemplate)
		require.NoError(t, err)
	}

	assert.Len(t, exec.runtimes, retainRuntimes, "the runtime cache must stay bounded")
	assert.Len(t, exec.order, retainRuntimes)
	_, kept := exec.runtimes["c1"]
	assert.False(t, kept, "the candidate no longer being compared must be released, not merely forgotten")
	assert.Contains(t, exec.runtimes, "c3", "the newest candidate must be the one kept")
}

// TestRuntimeArenaExecutor_HonoursTheCandidatesToolWhitelist is why the whole strategy has
// to travel: a candidate that declares a tool subset must run with that subset, not with
// whatever the process happens to have — otherwise the replay grades a strategy nobody
// would deploy.
func TestRuntimeArenaExecutor_HonoursTheCandidatesToolWhitelist(t *testing.T) {
	exec := newTestRuntimeExecutor(t)

	narrow := &mutation.Strategy{
		ID:             "narrow",
		PromptTemplate: "be terse",
		Params:         map[string]any{agents.ParamKeyTools: "hash_tool"},
	}
	rt, err := exec.runtimeFor(narrow, narrow.PromptTemplate)
	require.NoError(t, err)

	assert.Equal(t, []string{"hash_tool"}, rt.Tools(),
		"the candidate's declared subset must survive into the runtime's sandbox")

	silent := &mutation.Strategy{ID: "silent", PromptTemplate: "be terse"}
	rtSilent, err := exec.runtimeFor(silent, silent.PromptTemplate)
	require.NoError(t, err)
	assert.Greater(t, len(rtSilent.Tools()), 1,
		"a candidate that declares nothing runs on the evaluation ceiling, not on a single tool")
}

// TestRuntimeArenaExecutor_RefusesWorkAndRunTimesAfterClose: a closed comparison must not
// quietly start a new runtime, and the ones it built must be stopped.
func TestRuntimeArenaExecutor_RefusesWorkAndRunTimesAfterClose(t *testing.T) {
	exec, err := NewRuntimeArenaExecutor(context.Background(), testReplayProvider(t), nil)
	require.NoError(t, err)

	cand := &mutation.Strategy{ID: "cand-c", PromptTemplate: "be terse"}
	_, err = exec.runtimeFor(cand, cand.PromptTemplate)
	require.NoError(t, err)

	exec.Close()
	_, err = exec.runtimeFor(cand, cand.PromptTemplate)
	require.Error(t, err, "a closed executor must not start another runtime")
	assert.Contains(t, err.Error(), "closed")

	exec.Close() // idempotent
}

// TestCandidateFromArenaStrategy covers the shapes the arena actually hands over.
func TestCandidateFromArenaStrategy(t *testing.T) {
	t.Run("whole strategy keeps its params", func(t *testing.T) {
		src := &mutation.Strategy{
			ID:             "c1",
			PromptTemplate: "be terse",
			Params:         map[string]any{agents.ParamKeyTools: "calculator"},
		}
		got, err := candidateFromArenaStrategy(src, "")
		require.NoError(t, err)
		assert.Equal(t, "c1", got.ID)
		assert.Equal(t, "be terse", got.Prompt)
		assert.Equal(t, "calculator", got.Params[agents.ParamKeyTools])
	})

	t.Run("a bare string has no params and gets a stable hashed id", func(t *testing.T) {
		first, err := candidateFromArenaStrategy("be terse", "")
		require.NoError(t, err)
		second, err := candidateFromArenaStrategy("be terse", "")
		require.NoError(t, err)

		assert.Nil(t, first.Params, "a string declares no tool subset; the runtime must use the ceiling")
		assert.NotEmpty(t, first.ID)
		assert.Equal(t, first.ID, second.ID, "the same instructions must key the same cache entry")
		assert.NotEqual(t, "be terse", first.ID, "the id is a bounded key, not the prompt itself")
	})

	t.Run("different instructions get different ids", func(t *testing.T) {
		a, err := candidateFromArenaStrategy("alpha", "")
		require.NoError(t, err)
		b, err := candidateFromArenaStrategy("beta", "")
		require.NoError(t, err)
		assert.NotEqual(t, a.ID, b.ID)
	})

	t.Run("refusals", func(t *testing.T) {
		_, err := candidateFromArenaStrategy((*mutation.Strategy)(nil), "")
		require.Error(t, err, "a typed nil is a caller bug, not an empty candidate")

		_, err = candidateFromArenaStrategy("", "")
		require.Error(t, err, "empty instructions name no candidate")
	})
}

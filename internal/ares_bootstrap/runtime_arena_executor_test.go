package ares_bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// newRuntimeArenaExecutor builds an executor over a real factory (whose own tests cover the
// refusal paths) and a provider that counts how often it was resolved.
func newRuntimeArenaExecutor(t *testing.T) (*RuntimeArenaExecutor, *int) {
	t.Helper()
	providerCalls := 0
	exec, err := NewRuntimeArenaExecutor(context.Background(), func() (*CandidateReplayFactory, error) {
		providerCalls++
		return NewCandidateReplayFactory(CandidateReplayConfig{
			ChatClient: replayChatStub{},
			ToolBinder: replayBinderStub{},
		})
	}, nil)
	require.NoError(t, err)
	t.Cleanup(exec.Close)
	return exec, &providerCalls
}

// TestRuntimeArenaExecutor_OneRuntimePerCandidate is the cost contract: a runtime owns a
// pair of fabrics and a drain loop, so it must be built once per candidate — not once per
// case — and it must be resolved through the provider only once.
func TestRuntimeArenaExecutor_OneRuntimePerCandidate(t *testing.T) {
	exec, providerCalls := newRuntimeArenaExecutor(t)

	candA := &mutation.Strategy{ID: "cand-a", PromptTemplate: "be terse"}
	candB := &mutation.Strategy{ID: "cand-b", PromptTemplate: "be thorough"}

	a1, err := exec.runtimeFor(candA, candA.PromptTemplate)
	require.NoError(t, err)
	a2, err := exec.runtimeFor(candA, candA.PromptTemplate)
	require.NoError(t, err)
	assert.Same(t, a1, a2, "the same candidate must reuse its runtime across cases")
	assert.Equal(t, 1, *providerCalls, "the factory provider must be resolved once")

	b, err := exec.runtimeFor(candB, candB.PromptTemplate)
	require.NoError(t, err)
	assert.NotSame(t, a1, b, "two candidates must never share one runtime")
	assert.Len(t, exec.runtimes, 2)
}

// TestRuntimeArenaExecutor_BoundedRetention: a rolling deployment compares candidates
// indefinitely, so the cache must stay bounded; the candidate no longer being compared is
// retired (closed) rather than forgotten.
func TestRuntimeArenaExecutor_BoundedRetention(t *testing.T) {
	exec, _ := newRuntimeArenaExecutor(t)

	for _, id := range []string{"c1", "c2", "c3"} {
		c := &mutation.Strategy{ID: id, PromptTemplate: "be terse"}
		_, err := exec.runtimeFor(c, c.PromptTemplate)
		require.NoError(t, err)
	}

	assert.Len(t, exec.runtimes, retainRuntimes, "the runtime cache must stay bounded")
	assert.NotContains(t, exec.runtimes, "c1", "the oldest comparison must be retired")
	assert.Contains(t, exec.runtimes, "c3")
}

// TestRuntimeArenaExecutor_CandidateToolSubsetReachesTheSandbox is the口径 this replay was
// built around: a candidate that declares a tool subset must RUN with that subset.
//
// The whole point of carrying the strategy value rather than its prompt template is here —
// a template loses Params, and Params is where the whitelist lives. If this test passes
// with a template-only path it would be measuring nothing, so the assertion is on the
// runtime's INSTALLED tools, not on what was passed in.
func TestRuntimeArenaExecutor_CandidateToolSubsetReachesTheSandbox(t *testing.T) {
	exec, _ := newRuntimeArenaExecutor(t)

	narrow := &mutation.Strategy{
		ID:             "narrow",
		PromptTemplate: "be terse",
		Params:         map[string]any{agents.ParamKeyTools: "hash_tool"},
	}
	rt, err := exec.runtimeFor(narrow, narrow.PromptTemplate)
	require.NoError(t, err)
	assert.Equal(t, []string{"hash_tool"}, rt.Tools(),
		"the candidate's declared subset must survive into the runtime's sandbox")

	// A bare string carries no Params: it runs on the evaluation ceiling, which is a
	// deliberate statement rather than an invented tool set.
	silent := "be terse"
	rtCeiling, err := exec.runtimeFor(silent, silent)
	require.NoError(t, err)
	assert.Greater(t, len(rtCeiling.Tools()), 1,
		"a candidate that declares nothing runs the ceiling, not a single tool")
	assert.NotEqual(t, rtCeiling.Tools(), rt.Tools())
}

// TestRuntimeArenaExecutor_RefusesWhatItCannotRun: an executor that cannot execute would
// make "replay is on" a lie the scores never reveal.
func TestRuntimeArenaExecutor_RefusesWhatItCannotRun(t *testing.T) {
	exec, _ := newRuntimeArenaExecutor(t)

	_, err := exec.runtimeFor((*mutation.Strategy)(nil), "whatever")
	require.Error(t, err, "a typed nil is a caller bug, not a candidate")

	_, err = exec.runtimeFor("", "")
	require.Error(t, err, "empty instructions name no candidate")

	_, err = exec.runtimeFor(42, "")
	require.Error(t, err, "an unreadable strategy type must be refused, not guessed")
	assert.Contains(t, err.Error(), "cannot read a strategy")

	// A provider that fails is a loud failure of the case, never a silent 0.
	failing, err := NewRuntimeArenaExecutor(context.Background(), func() (*CandidateReplayFactory, error) {
		return nil, errors.New("no tool binder in this process")
	}, nil)
	require.NoError(t, err)
	defer failing.Close()
	_, err = failing.runtimeFor("be terse", "be terse")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tool binder in this process")
}

// TestRuntimeArenaExecutor_CloseIsFinal: after a comparison ends, the executor must not
// quietly start a new runtime, and the ones it built must be stopped.
func TestRuntimeArenaExecutor_CloseIsFinal(t *testing.T) {
	exec, _ := newRuntimeArenaExecutor(t)

	c := &mutation.Strategy{ID: "c1", PromptTemplate: "be terse"}
	_, err := exec.runtimeFor(c, c.PromptTemplate)
	require.NoError(t, err)

	exec.Close()
	_, err = exec.runtimeFor(c, c.PromptTemplate)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "closed")

	exec.Close() // idempotent: several teardown paths may run it
}

// TestRuntimeArenaExecutor_ExecuteIsTheSeamTheScorerCalls covers the interface METHOD, not
// just its internals: the arena scorer calls Execute, so a broken Execute is a broken
// replay no matter how healthy runtimeFor is.
func TestRuntimeArenaExecutor_ExecuteIsTheSeamTheScorerCalls(t *testing.T) {
	exec, _ := newRuntimeArenaExecutor(t)

	// A strategy it cannot run fails through Execute — the error must come out of the
	// seam, not be swallowed into an empty answer (which the judge would grade as wrong).
	_, err := exec.Execute(context.Background(), (*mutation.Strategy)(nil), "be terse", "what is 2+2?")
	require.Error(t, err)

	// After Close the seam refuses too, rather than starting a runtime nobody can stop.
	exec.Close()
	_, err = exec.Execute(context.Background(), "be terse", "be terse", "what is 2+2?")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "closed")
}

// TestNewRuntimeArenaExecutor_RefusesMissingPieces: an executor that cannot execute would
// make "replay is on" a lie the scores would never reveal.
func TestNewRuntimeArenaExecutor_RefusesMissingPieces(t *testing.T) {
	_, err := NewRuntimeArenaExecutor(context.Background(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "factory provider")

	// Deliberate nil context: this pins the constructor's guard, which exists because an
	// executor without a base context would have no way to outlive a case.
	// Two directive families are needed here and they are not interchangeable: the standalone
	// staticcheck binary in `make lint-staticcheck` only understands //lint:ignore, while
	// golangci-lint only understands //nolint. Keeping just one of them leaves the other tool
	// reporting the same deliberate call.
	//lint:ignore SA1012 deliberate: pins the nil-base-context guard
	_, err = NewRuntimeArenaExecutor(nil, func() (*CandidateReplayFactory, error) { return nil, nil }, nil) //nolint:staticcheck // same reason as the lint:ignore above
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base context")

	// A provider that hands back nil is caught on first use, not turned into a panic.
	exec, err := NewRuntimeArenaExecutor(context.Background(), func() (*CandidateReplayFactory, error) {
		return nil, nil
	}, nil)
	require.NoError(t, err)
	defer exec.Close()
	_, err = exec.runtimeFor("be terse", "be terse")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned nil")
}

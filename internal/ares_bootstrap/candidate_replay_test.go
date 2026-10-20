package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// replayStubs satisfy the two things the factory consumes. Nothing here executes: the
// factory's job is to decide what the process supplies and to refuse an incomplete set.
type replayChatStub struct{}

func (replayChatStub) Chat(context.Context, []*llmcore.LLMMessage, []llmcore.Tool, map[string]any) (*llmcore.GenerateResponse, error) {
	return &llmcore.GenerateResponse{}, nil
}

type replayBinderStub struct{}

func (replayBinderStub) CallTool(context.Context, string, map[string]any) (any, error) {
	return nil, nil
}
func (replayBinderStub) ListTools() []string                    { return []string{"calculator"} }
func (replayBinderStub) IsToolIdempotent(string) bool           { return true }
func (replayBinderStub) GetToolSchemas() []resources.ToolSchema { return nil }

// TestNewCandidateReplayFactory_FailsLoudOnMissingPieces: an enabled replay that cannot
// run would report "no signal" for every candidate, which the pipeline would read as a
// measurement rather than as the absence of one.
func TestNewCandidateReplayFactory_FailsLoudOnMissingPieces(t *testing.T) {
	_, err := NewCandidateReplayFactory(CandidateReplayConfig{ToolBinder: replayBinderStub{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chat client")

	_, err = NewCandidateReplayFactory(CandidateReplayConfig{ChatClient: replayChatStub{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool binder", "the binder is the piece the caller must supply")
}

// TestCandidateReplayFactory_BuildsASandboxedStartedRuntime is the wiring contract: one
// call produces a runtime that is already draining, whose tool set is the CEILING (not
// whatever the process has), and whose identity is closed by its own Close.
func TestCandidateReplayFactory_BuildsASandboxedStartedRuntime(t *testing.T) {
	factory, err := NewCandidateReplayFactory(CandidateReplayConfig{
		ChatClient: replayChatStub{},
		ToolBinder: replayBinderStub{},
	})
	require.NoError(t, err)

	ctx := context.Background()
	rt, err := factory.Runtime(ctx, &agents.ActiveStrategy{ID: "cand-replay", Prompt: "be terse"})
	require.NoError(t, err)
	defer rt.Close()

	require.NotNil(t, rt.Executor(), "a runtime must come back with a case runner")
	assert.NotEmpty(t, rt.Tools(), "a candidate with no whitelist runs on the evaluation ceiling, not on nothing")

	// And the factory refuses a candidate it cannot sandbox, rather than handing back a
	// runtime that quietly has every tool.
	_, err = factory.Runtime(ctx, &agents.ActiveStrategy{
		ID:     "cand-forbidden",
		Params: map[string]any{agents.ParamKeyTools: "file_tools,code_runner"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no filter")
}

// TestCandidateReplayFactory_RefusesANilCandidate: a nil candidate is a caller bug, and
// the message must say so instead of panicking inside the assembly.
func TestCandidateReplayFactory_RefusesANilCandidate(t *testing.T) {
	factory, err := NewCandidateReplayFactory(CandidateReplayConfig{
		ChatClient: replayChatStub{},
		ToolBinder: replayBinderStub{},
	})
	require.NoError(t, err)

	_, err = factory.Runtime(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "candidate strategy")
}

// Compile-time proof that the process-supplied binder satisfies the runtime's contract.
var _ agentfabric.ToolBinder = replayBinderStub{}

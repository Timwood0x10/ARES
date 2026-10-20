package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/kernel"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// The stubs below are the minimum a runtime can be assembled over: the factory's job
// is isolation and wiring, not execution, so nothing here needs to think.

type stubChatClient struct{}

func (stubChatClient) Chat(context.Context, []*llmcore.LLMMessage, []llmcore.Tool, map[string]any) (*llmcore.GenerateResponse, error) {
	return &llmcore.GenerateResponse{}, nil
}

type stubToolBinder struct{}

func (stubToolBinder) CallTool(context.Context, string, map[string]any) (any, error) { return nil, nil }
func (stubToolBinder) ListTools() []string                                           { return nil }
func (stubToolBinder) IsToolIdempotent(string) bool                                  { return true }
func (stubToolBinder) GetToolSchemas() []resources.ToolSchema                        { return nil }

// stubExecutor is one capability's execution body. It never runs in these tests (no
// case is ever submitted): it exists so the scheduler has a candidate for the plan
// capability, which is what makes the assembled runtime a runtime rather than a shape.
type stubExecutor struct{ id string }

func (e stubExecutor) ID() string             { return e.id }
func (e stubExecutor) Type() models.AgentType { return agentfabric.PlanCapability }
func (e stubExecutor) ExecuteStep(context.Context, *models.Task) (*sub.StepOutcome, error) {
	return &sub.StepOutcome{Done: true, Result: &models.TaskResult{}}, nil
}

// candidateFixture builds a config whose executor factory records the fabric it was
// handed, which is how the isolation assertions below reach the wiring. The core is
// asserted non-nil: an executor is an adapter over its router, so a factory that received
// no core could not produce a working body.
func candidateFixture(seen *[]*taskfabric.Fabric) CandidateRuntimeConfig {
	return CandidateRuntimeConfig{
		ChatClient: stubChatClient{},
		ToolBinder: stubToolBinder{},
		Executors: func(core *Execution, fabric *taskfabric.Fabric, _ *agentfabric.Fabric) (map[string]kernel.CapabilityExecutor, error) {
			if core == nil || core.Router == nil {
				return nil, errors.New("fixture received a core without a router")
			}
			if seen != nil {
				*seen = append(*seen, fabric)
			}
			return map[string]kernel.CapabilityExecutor{"plan": stubExecutor{id: "plan-1"}}, nil
		},
	}
}

// TestFixedStrategySource_ServesTheCandidate is the seam's contract: the runtime asks
// a source for "the active strategy" and must get THIS candidate back. A nil candidate
// is served as (nil, nil) — the interface's documented "steer nothing" value — because
// a candidate without a prompt template must run like an unconfigured deployment, not
// fail.
func TestFixedStrategySource_ServesTheCandidate(t *testing.T) {
	candidate := &agents.ActiveStrategy{ID: "cand-42", Prompt: "be terse"}
	source := FixedStrategySource{Strategy: candidate}

	got, err := source.GetActiveStrategy(context.Background())
	require.NoError(t, err)
	require.Same(t, candidate, got, "the source must serve the candidate itself")
	assert.Equal(t, "be terse", got.Prompt)

	empty, err := FixedStrategySource{}.GetActiveStrategy(context.Background())
	require.NoError(t, err)
	assert.Nil(t, empty, "no candidate must read as 'steer nothing', not as an error")
}

// TestNewCandidateRuntime_FailsLoudOnMissingPieces: a runtime that cannot run would
// turn every case into "no signal", and no signal is scored as no penalty — so a
// half-assembled candidate would look healthy. Each missing piece is refused by name.
func TestNewCandidateRuntime_FailsLoudOnMissingPieces(t *testing.T) {
	base := candidateFixture(nil)

	tests := []struct {
		name    string
		mutate  func(*CandidateRuntimeConfig)
		wantMsg string
	}{
		{"no chat client", func(c *CandidateRuntimeConfig) { c.ChatClient = nil }, "chat client"},
		{"no tool binder", func(c *CandidateRuntimeConfig) { c.ToolBinder = nil }, "tool binder"},
		{"factory returns nothing", func(c *CandidateRuntimeConfig) {
			c.Executors = func(*Execution, *taskfabric.Fabric, *agentfabric.Fabric) (map[string]kernel.CapabilityExecutor, error) {
				return nil, nil
			}
		}, "no executors"},
		{"factory errors", func(c *CandidateRuntimeConfig) {
			c.Executors = func(*Execution, *taskfabric.Fabric, *agentfabric.Fabric) (map[string]kernel.CapabilityExecutor, error) {
				return nil, errors.New("boom")
			}
		}, "boom"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			rt, err := NewCandidateRuntime(cfg, &agents.ActiveStrategy{ID: "c"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.Nil(t, rt, "a refused assembly must not hand back a runtime")
		})
	}
}

// TestNewCandidateRuntime_FencesEachCandidatesTraffic is the isolation contract, and
// the reason this factory exists at all: evaluation cases must not land in the serving
// fabrics (the serving scheduler would drain them, quotas would count them, and a
// candidate's failure would surface as production failure).
//
// The assertion is made where the leak would occur: each runtime must build its
// executors over ITS OWN fabric, and two runtimes must never share one.
func TestNewCandidateRuntime_FencesEachCandidatesTraffic(t *testing.T) {
	var seen []*taskfabric.Fabric
	cfg := candidateFixture(&seen)

	first, err := NewCandidateRuntime(cfg, &agents.ActiveStrategy{ID: "cand-a", Prompt: "prompt A"})
	require.NoError(t, err)
	second, err := NewCandidateRuntime(cfg, &agents.ActiveStrategy{ID: "cand-b", Prompt: "prompt B"})
	require.NoError(t, err)

	require.Len(t, seen, 2, "each runtime must build its own executors")
	require.Same(t, first.fabric, seen[0], "executors must be built over the runtime's own fabric")
	require.Same(t, second.fabric, seen[1])
	assert.NotSame(t, first.fabric, second.fabric, "two candidates must never share a task fabric")

	// Both fabrics are empty and independent: nothing from one runtime's assembly
	// leaks into the other, which is what makes "candidate B's cases land in
	// candidate A's graph" impossible rather than merely unlikely.
	assert.Empty(t, first.fabric.IDs())
	assert.Empty(t, second.fabric.IDs())
	assert.NotSame(t, first.sched, second.sched, "each candidate drains with its own scheduler")
}

// TestCandidateRuntime_CloseStopsTheLoop: a caller that walks candidates would
// otherwise leave one scheduler goroutine (and its fabrics) behind per candidate.
func TestCandidateRuntime_CloseStopsTheLoop(t *testing.T) {
	rt, err := NewCandidateRuntime(candidateFixture(nil), &agents.ActiveStrategy{ID: "cand-a"})
	require.NoError(t, err)

	rt.Start(context.Background())
	require.Eventually(t, func() bool { return rt.sched.Running() },
		2*time.Second, 5*time.Millisecond, "Start must bring the drain loop up")

	closed := make(chan struct{})
	go func() {
		rt.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not stop the scheduler loop: a per-candidate runtime would leak a goroutine per candidate")
	}
	assert.False(t, rt.sched.Running(), "the loop must be down once Close returns")

	// Closing again is a no-op, not a hang: several teardown paths may run it.
	rt.Close()
}

// TestCandidateRuntime_CloseWithoutStartIsSafe: the caller may assemble a runtime and
// decide not to run it (a candidate that lost its turn, a failed setup downstream), so
// teardown must not require a loop that never started.
func TestCandidateRuntime_CloseWithoutStartIsSafe(t *testing.T) {
	rt, err := NewCandidateRuntime(candidateFixture(nil), &agents.ActiveStrategy{ID: "cand-a"})
	require.NoError(t, err)

	rt.Close()
	rt.Close()
	assert.False(t, rt.sched.Running(), "an unstarted runtime reports no loop")
}

// TestCandidateRuntime_StartIsIdempotent: a double Start would run two drain loops over
// one fabric, and two schedulers draining the same tasks is a correctness problem, not
// just a leak.
func TestCandidateRuntime_StartIsIdempotent(t *testing.T) {
	rt, err := NewCandidateRuntime(candidateFixture(nil), &agents.ActiveStrategy{ID: "cand-a"})
	require.NoError(t, err)
	defer rt.Close()

	rt.Start(context.Background())
	first := rt.done
	rt.Start(context.Background())
	assert.Equal(t, first, rt.done, "a second Start must not replace (or add) the drain loop")
}

// TestCandidateRuntime_StartAfterCloseIsRefused: Close is permanent. Starting a loop
// after the caller has already stopped it would leave a scheduler running with no one
// left to stop it, over a fabric the caller believes is finished.
func TestCandidateRuntime_StartAfterCloseIsRefused(t *testing.T) {
	rt, err := NewCandidateRuntime(candidateFixture(nil), &agents.ActiveStrategy{ID: "cand-a"})
	require.NoError(t, err)

	rt.Start(context.Background())
	rt.Close()

	rt.Start(context.Background())
	assert.False(t, rt.sched.Running(), "a closed runtime must not start draining again")
}

// TestCandidateRuntime_ExecutorIsUsable pins the structural contract the evaluation
// runner relies on: the returned executor talks to this runtime, and a case it cannot
// complete reports an error instead of an empty answer.
//
// The runtime is NOT started here on purpose: nothing drains, so the case must fail on
// the wait budget rather than hang forever — the property that keeps a stuck candidate
// from stalling the whole evaluation loop.
func TestCandidateRuntime_ExecutorIsUsable(t *testing.T) {
	cfg := candidateFixture(nil)
	cfg.Await = AwaitOptions{Wait: 50 * time.Millisecond, PollInterval: 5 * time.Millisecond}
	rt, err := NewCandidateRuntime(cfg, &agents.ActiveStrategy{ID: "cand-a"})
	require.NoError(t, err)
	defer rt.Close()

	require.NotNil(t, rt.Executor())

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, _, err = rt.Executor().Execute(ctx, "what is 2+2?")
	require.Error(t, err, "a case with nothing draining it must report an error, not an empty answer")
	assert.True(t,
		strings.Contains(err.Error(), "submit") || strings.Contains(err.Error(), "wait") ||
			strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline"),
		"unexpected error from an undrained runtime: %v", err)
}

package ares_runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_events"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chaosTestManager builds a started Manager with an in-memory event store and
// registers a mock agent guarded by a factory call counter. The returned
// cleanup cancels the runtime context.
func chaosTestManager(t *testing.T) (*Manager, *ares_events.MemoryEventStore, *mockAgent, *atomic.Int32) {
	t.Helper()
	store := ares_events.NewMemoryEventStore()
	m := New(nil, store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	var factoryCalls atomic.Int32
	m.RegisterAgent(agent, func() base.Agent {
		factoryCalls.Add(1)
		return newMockAgent("a1")
	})
	require.NoError(t, m.StartAgent(ctx, agent))
	waitUntil(t, func() bool { return agent.started.Load() == 1 })
	return m, store, agent, &factoryCalls
}

// waitUntil polls cond until it is true or the timeout elapses. Polling with
// a deadline is used instead of time.Sleep-based synchronization (code rule 7.3).
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestPauseAgent_NotFound(t *testing.T) {
	m, _, _, _ := chaosTestManager(t)
	ctx := context.Background()
	assert.ErrorIs(t, m.PauseAgent(ctx, "nonexistent"), ErrAgentNotFound)
}

func TestResumeAgent_NotFound(t *testing.T) {
	m, _, _, _ := chaosTestManager(t)
	ctx := context.Background()
	assert.ErrorIs(t, m.ResumeAgent(ctx, "nonexistent"), ErrAgentNotFound)
}

// TestPauseAgent_SuspendsWithoutStoppedFlag verifies pause keeps the same
// managedAgent entry: paused=true is set but the permanent stopped flag is
// NOT, so the agent remains resumable and distinguishable from StopAgent.
func TestPauseAgent_SuspendsWithoutStoppedFlag(t *testing.T) {
	m, _, agent, _ := chaosTestManager(t)
	ctx := context.Background()

	require.NoError(t, m.PauseAgent(ctx, "a1"))
	waitUntil(t, func() bool { return agent.stopped.Load() == 1 })

	m.mu.RLock()
	ma, ok := m.agents["a1"]
	m.mu.RUnlock()
	require.True(t, ok, "managed agent entry must survive pause")
	assert.True(t, ma.paused, "paused flag must be set")
	assert.False(t, ma.stopped, "permanent stopped flag must NOT be set by pause")

	info, ok := m.GetAgentInfo("a1")
	require.True(t, ok)
	assert.True(t, info.Paused, "AgentInfo must expose paused state")
}

// TestPauseAgent_EmitsStoppedEvent verifies pause emits EventAgentStopped with
// a distinguishable "pause" reason, reusing the canonical lifecycle event type.
func TestPauseAgent_EmitsStoppedEvent(t *testing.T) {
	m, store, _, _ := chaosTestManager(t)
	ctx := context.Background()
	require.NoError(t, m.PauseAgent(ctx, "a1"))

	evts := readStreamEvents(t, store, "a1")
	stopped := lastEventOfType(evts, ares_events.EventAgentStopped)
	require.NotNil(t, stopped, "pause must emit EventAgentStopped")
	assert.Equal(t, "pause", stopped.Payload[FieldReason])
}

// TestResumeAgent_RelaunchesSameInstance is the core contract: resume must
// re-run Start on the SAME in-memory agent instance — no factory call, no
// restart counter increment, no state loss.
func TestResumeAgent_RelaunchesSameInstance(t *testing.T) {
	m, _, agent, factoryCalls := chaosTestManager(t)
	ctx := context.Background()

	require.NoError(t, m.PauseAgent(ctx, "a1"))
	waitUntil(t, func() bool { return agent.stopped.Load() == 1 })

	require.NoError(t, m.ResumeAgent(ctx, "a1"))
	// Same instance: Start must be invoked a second time.
	waitUntil(t, func() bool { return agent.started.Load() == 2 })

	assert.Equal(t, int32(0), factoryCalls.Load(),
		"resume must not rebuild via factory (state must be preserved)")
	assert.Zero(t, m.Stats().TotalRestarts,
		"resume must not count as a restart")

	m.mu.RLock()
	ma, ok := m.agents["a1"]
	m.mu.RUnlock()
	require.True(t, ok)
	assert.False(t, ma.paused, "paused flag must be cleared after resume")
	assert.Equal(t, agent, chaosUnwrap(ma.agent),
		"the same agent instance must be relaunched (unwrapped from chaos wrapper)")
}

// TestResumeAgent_NotPaused_NoOp verifies resume on a running (not paused)
// agent is a safe no-op: it must not restart or rebuild the agent.
func TestResumeAgent_NotPaused_NoOp(t *testing.T) {
	m, _, agent, factoryCalls := chaosTestManager(t)
	ctx := context.Background()

	require.NoError(t, m.ResumeAgent(ctx, "a1"))
	assert.Equal(t, int32(1), agent.started.Load(),
		"resume of a running agent must not call Start again")
	assert.Equal(t, int32(0), factoryCalls.Load())
	assert.Zero(t, m.Stats().TotalRestarts)
}

// TestResumeAgent_EmitsStartedEvent verifies resume emits EventAgentStarted
// with a distinguishable "resume" reason.
func TestResumeAgent_EmitsStartedEvent(t *testing.T) {
	m, store, _, _ := chaosTestManager(t)
	ctx := context.Background()

	require.NoError(t, m.PauseAgent(ctx, "a1"))
	require.NoError(t, m.ResumeAgent(ctx, "a1"))

	evts := readStreamEvents(t, store, "a1")
	started := lastEventOfType(evts, ares_events.EventAgentStarted)
	require.NotNil(t, started, "resume must emit EventAgentStarted")
	assert.Equal(t, "resume", started.Payload[FieldReason])
}

// TestPausedAgent_NotResurrected verifies NotifyAgentDead skips paused agents:
// an unexpected death while paused must not trigger the factory-based restore.
func TestPausedAgent_NotResurrected(t *testing.T) {
	m, _, _, factoryCalls := chaosTestManager(t)
	ctx := context.Background()

	require.NoError(t, m.PauseAgent(ctx, "a1"))
	waitUntil(t, func() bool {
		info, ok := m.GetAgentInfo("a1")
		return ok && info.Paused
	})

	m.NotifyAgentDead("a1", "test: paused agent reported dead")
	// The resurrection path is skipped synchronously for paused agents; the
	// bounded poll proves no async restore was scheduled.
	assertStaysZero(t, 100*time.Millisecond, factoryCalls)
}

// assertStaysZero fails if v becomes non-zero within the duration. Used for
// negative assertions (something must NOT happen) with a bounded deadline
// instead of a bare time.Sleep (code rule 7.3).
func assertStaysZero(t *testing.T, d time.Duration, v *atomic.Int32) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if v.Load() != 0 {
			t.Fatalf("expected counter to stay 0, got %d", v.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readStreamEvents returns all events for a stream.
func readStreamEvents(t *testing.T, store *ares_events.MemoryEventStore, streamID string) []*ares_events.Event {
	t.Helper()
	evts, err := store.Read(context.Background(), streamID, ares_events.ReadOptions{})
	require.NoError(t, err)
	return evts
}

// lastEventOfType returns the most recent event of the given type, or nil.
func lastEventOfType(evts []*ares_events.Event, typ ares_events.EventType) *ares_events.Event {
	for i := len(evts) - 1; i >= 0; i-- {
		if evts[i].Type == typ {
			return evts[i]
		}
	}
	return nil
}

// TestChaosFaultInjections verifies the four fault-injection methods actually
// take effect at the Process boundary: after injection, Process returns a
// fault error instead of delegating to the wrapped agent. Without injection
// the wrapper delegates unchanged (mock Process returns its own error).
func TestChaosFaultInjections(t *testing.T) {
	tests := []struct {
		name        string
		inject      func(m *Manager, ctx context.Context) error
		wantErrText string
	}{
		{
			name:        "network_partition",
			inject:      func(m *Manager, ctx context.Context) error { return m.PartitionNetwork(ctx, "a1") },
			wantErrText: "network partition injected",
		},
		{
			name:        "memory_corrupt",
			inject:      func(m *Manager, ctx context.Context) error { return m.CorruptMemory(ctx, "a1") },
			wantErrText: "memory corruption injected",
		},
		{
			name:        "mcp_disconnect",
			inject:      func(m *Manager, ctx context.Context) error { return m.DisconnectMCP(ctx, "a1") },
			wantErrText: "MCP disconnected injected",
		},
		{
			name: "llm_failure",
			inject: func(m *Manager, ctx context.Context) error {
				return m.InjectLLMFailure(ctx, "a1", "rate_limit")
			},
			wantErrText: "LLM failure (rate_limit) injected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _, _ := chaosTestManager(t)
			ctx := context.Background()

			// Before injection the wrapper delegates to the raw mock agent
			// (its Process returns "not implemented in mock").
			m.mu.RLock()
			wrapped := m.agents["a1"].agent
			m.mu.RUnlock()
			_, err := wrapped.Process(ctx, "input")
			require.ErrorContains(t, err, "not implemented in mock",
				"pre-injection Process must delegate to the wrapped agent")

			require.NoError(t, tt.inject(m, ctx))

			_, err = wrapped.Process(ctx, "input")
			require.ErrorContains(t, err, tt.wantErrText,
				"post-injection Process must surface the injected fault")
		})
	}
}

// TestChaosFaultInjections_ProcessStream verifies the injected fault also
// fails ProcessStream with a closed channel, per its contract.
func TestChaosFaultInjections_ProcessStream(t *testing.T) {
	m, _, _, _ := chaosTestManager(t)
	ctx := context.Background()
	require.NoError(t, m.PartitionNetwork(ctx, "a1"))

	m.mu.RLock()
	wrapped := m.agents["a1"].agent
	m.mu.RUnlock()

	ch, err := wrapped.ProcessStream(ctx, "input")
	require.ErrorContains(t, err, "network partition injected")
	_, ok := <-ch
	assert.False(t, ok, "channel must be closed when a fault is injected")
}

// TestChaosUnwrapRoundTrip verifies chaosUnwrap returns the raw agent from a
// wrapped instance and passes through unwrapped instances unchanged, so
// optional-interface assertions (StatefulAgent, Heartbeater) keep working.
func TestChaosUnwrapRoundTrip(t *testing.T) {
	agent := newMockAgent("u1")
	wrapped := &chaosWrappedAgent{Agent: agent, id: "u1"}
	assert.Same(t, agent, chaosUnwrap(wrapped), "unwrap must return the raw agent")
	assert.Same(t, agent, chaosUnwrap(agent), "unwrap of a plain agent is a no-op")
	assert.Nil(t, chaosUnwrap(nil), "unwrap of nil is nil")
}

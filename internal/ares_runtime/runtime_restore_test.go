package ares_runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_events"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/core/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
func TestManager_NotifyAgentDead_TriggersRestore(t *testing.T) {
	factory := newMockFactory()
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	m.RegisterAgent(agent, factory.create())
	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	// Notify agent dead.
	m.NotifyAgentDead("a1", "test crash")

	// Give time for async restore.
	time.Sleep(200 * time.Millisecond)

	// Factory should have been called to create a new agent.
	factory.mu.Lock()
	callCount := factory.callCount
	factory.mu.Unlock()
	assert.GreaterOrEqual(t, callCount, 1, "factory should have been called for restoration")
}

func TestManager_RestoreAgent_CreatesNewInstance(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	factory := newMockFactory()

	err := m.RestoreAgent(ctx, "a1", factory.create())
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	// Factory should have been called.
	newAgent := factory.lastAgent()
	require.NotNil(t, newAgent)
	assert.Equal(t, "agent-1", newAgent.ID())
	assert.Equal(t, models.AgentStatusReady, newAgent.Status())

	// TotalRestarts is not incremented by RestoreAgent directly;
	// NotifyAgentDead owns the restart accounting.
	stats := m.Stats()
	assert.Equal(t, 0, stats.TotalRestarts)
	assert.Equal(t, 1, stats.ActiveAgents)
}

func TestManager_RestoreAgent_ReplaysEvents(t *testing.T) {
	eventStore := ares_events.NewMemoryEventStore()
	m := New(nil, eventStore, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Pre-populate ares_events in the store using the agent:streamID convention.
	err := eventStore.Append(ctx, "a1", []*ares_events.Event{
		{
			Type: ares_events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "sess-123",
			},
		},
		{
			Type: ares_events.EventTaskCreated,
			Payload: map[string]any{
				"task_id": "task-1",
			},
		},
	}, 0)
	require.NoError(t, err)

	factory := newMockFactory()
	err = m.RestoreAgent(ctx, "a1", factory.create())
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	// Verify ares_events were replayed.
	newAgent := factory.lastAgent()
	require.NotNil(t, newAgent)
	assert.NotNil(t, newAgent.replayedEvts, "ares_events should have been replayed")
	assert.Len(t, newAgent.replayedEvts, 3) // 2 pre-populated + 1 failover.triggered emitted by RestoreAgent
}

func TestManager_RestoreAgent_RestoresState(t *testing.T) {
	eventStore := ares_events.NewMemoryEventStore()
	m := New(nil, eventStore, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Pre-populate with session event using the agent:streamID convention.
	err := eventStore.Append(ctx, "a1", []*ares_events.Event{
		{
			Type: ares_events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "sess-456",
			},
		},
	}, 0)
	require.NoError(t, err)

	factory := newMockFactory()
	err = m.RestoreAgent(ctx, "a1", factory.create())
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	newAgent := factory.lastAgent()
	require.NotNil(t, newAgent)
	require.NotNil(t, newAgent.restoredState)
	assert.Equal(t, "sess-456", newAgent.restoredState["session_id"])
}

func TestManager_RestoreAgent_NilFactory(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	err := m.RestoreAgent(ctx, "a1", nil)
	assert.ErrorIs(t, err, ErrNilFactory)
}

func TestManager_ConcurrentNotifyAgentDead(t *testing.T) {
	factory := newMockFactory()
	config := &Config{
		HealthCheckInterval: 100 * time.Millisecond,
		MaxRestartsPerAgent: 0, // Unlimited.
	}
	m := New(config, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	m.RegisterAgent(agent, factory.create())
	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	// Concurrently notify agent dead multiple times.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.NotifyAgentDead("a1", fmt.Sprintf("concurrent-crash-%d", i))
		}(i)
	}
	wg.Wait()

	// Wait for all restoration goroutines to complete.
	time.Sleep(500 * time.Millisecond)

	// Verify at least one restore happened.
	factory.mu.Lock()
	callCount := factory.callCount
	factory.mu.Unlock()
	assert.GreaterOrEqual(t, callCount, 1, "at least one restore should have happened")
}
func TestManager_RestoreAgent_WithoutEventStore(t *testing.T) {
	// RestoreAgent should work even without an EventStore.
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	factory := newMockFactory()
	err := m.RestoreAgent(ctx, "a1", factory.create())
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	newAgent := factory.lastAgent()
	require.NotNil(t, newAgent)
	// No ares_events to replay, so replayedEvts should be nil.
	assert.Nil(t, newAgent.replayedEvts)
}

func TestManager_NotifyAgentDead_NoFactory(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	// NotifyAgentDead without a factory should not panic.
	assert.NotPanics(t, func() {
		m.NotifyAgentDead("a1", "no factory")
	})
}

func TestManager_NotifyAgentDead_ConcurrentRespectsLimit(t *testing.T) {
	factory := newMockFactory()
	config := &Config{
		HealthCheckInterval: 100 * time.Millisecond,
		MaxRestartsPerAgent: 3,
	}
	m := New(config, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	m.RegisterAgent(agent, factory.create())
	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	// Fire many concurrent NotifyAgentDead calls.
	// With the TOCTOU fix, at most MaxRestartsPerAgent (3) should proceed.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.NotifyAgentDead("a1", fmt.Sprintf("concurrent-crash-%d", i))
		}(i)
	}
	wg.Wait()

	// Wait for async restores to complete.
	time.Sleep(500 * time.Millisecond)

	// The restart count on the agent should not exceed MaxRestartsPerAgent.
	m.mu.RLock()
	ma := m.agents["a1"]
	restarts := 0
	if ma != nil {
		restarts = ma.restarts
	}
	m.mu.RUnlock()

	assert.LessOrEqual(t, restarts, config.MaxRestartsPerAgent,
		"restarts should not exceed MaxRestartsPerAgent even under concurrency")
}

func TestManager_NotifyAgentDead_MaxRestartsExceeded(t *testing.T) {
	factory := newMockFactory()
	config := &Config{
		HealthCheckInterval: 100 * time.Millisecond,
		MaxRestartsPerAgent: 1,
	}
	m := New(config, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	m.RegisterAgent(agent, factory.create())
	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	// Simulate an agent with 1 restart already (matching the max).
	m.mu.Lock()
	if ma, ok := m.agents["a1"]; ok {
		ma.restarts = 1
	}
	m.mu.Unlock()

	// NotifyAgentDead should skip because max restarts reached.
	m.NotifyAgentDead("a1", "max test")
	time.Sleep(200 * time.Millisecond)

	factory.mu.Lock()
	callCount := factory.callCount
	factory.mu.Unlock()
	assert.Equal(t, 0, callCount, "factory should not be called when max restarts exceeded")
}

func TestManager_Start_ReturnsError(t *testing.T) {
	m := New(nil, nil, nil)
func TestManager_RestoreAgent_WithEventStore(t *testing.T) {
	eventStore := ares_events.NewMemoryEventStore()
	m := New(nil, eventStore, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Pre-populate the event store with two ares_events for stream "a1".
	err := eventStore.Append(ctx, "a1", []*ares_events.Event{
		{
			Type:    ares_events.EventSessionCreated,
			Payload: map[string]any{"session_id": "sess-abc"},
		},
		{
			Type:    ares_events.EventTaskCreated,
			Payload: map[string]any{"task_id": "task-1"},
		},
	}, 0)
	require.NoError(t, err)

	factory := newMockFactory()
	err = m.RestoreAgent(ctx, "a1", factory.create())
	require.NoError(t, err)

	// Wait for the agent goroutine to call Start.
	time.Sleep(100 * time.Millisecond)

	restoredAgent := factory.lastAgent()
	require.NotNil(t, restoredAgent, "factory should have created an agent")

	// Events must have been replayed to the StatefulAgent.
	require.NotNil(t, restoredAgent.replayedEvts, "ares_events should have been replayed")
	assert.Len(t, restoredAgent.replayedEvts, 3) // 2 pre-populated + 1 failover.triggered

	// State should contain session_id extracted from EventSessionCreated.
	require.NotNil(t, restoredAgent.restoredState, "state should have been restored")
	assert.Equal(t, "sess-abc", restoredAgent.restoredState["session_id"])

	// Agent should be running.
	assert.Equal(t, models.AgentStatusReady, restoredAgent.Status())
}

// TestManager_RestoreAgent_WithMemoryManager verifies that RestoreAgent loads
// cognitive state (conversation history) via MemoryManager during restoration.
func TestManager_RestoreAgent_WithMemoryManager(t *testing.T) {
	eventStore := ares_events.NewMemoryEventStore()
	memManager := newMockMemoryManager()
	m := New(nil, eventStore, memManager)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Pre-populate the event store with a session event.
	err := eventStore.Append(ctx, "a1", []*ares_events.Event{
		{
			Type:    ares_events.EventSessionCreated,
			Payload: map[string]any{"session_id": "sess-mem"},
		},
	}, 0)
	require.NoError(t, err)

	factory := newMockFactory()
	err = m.RestoreAgent(ctx, "a1", factory.create())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	restoredAgent := factory.lastAgent()
	require.NotNil(t, restoredAgent)

	// RestoreState should have been called with combined state.
	require.NotNil(t, restoredAgent.restoredState)
	assert.Equal(t, "sess-mem", restoredAgent.restoredState["session_id"])

	// Conversation history from the mock memory manager should be present.
	history, ok := restoredAgent.restoredState["conversation_history"]
	require.True(t, ok, "conversation_history should be in restored state")
	assert.Len(t, history.([]memory.Message), 2)

	// Events should have been replayed.
	require.NotNil(t, restoredAgent.replayedEvts)
	assert.Len(t, restoredAgent.replayedEvts, 2) // 1 pre-populated + 1 failover.triggered
}

// TestManager_RestoreAgent_AgentNotStatefulAgent verifies that RestoreAgent works
// correctly when the factory returns an agent that does NOT implement StatefulAgent.
// State restoration and event replay should be skipped without error.
func TestManager_RestoreAgent_AgentNotStatefulAgent(t *testing.T) {
	factory := newNonStatefulFactory()
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	err := m.RestoreAgent(ctx, "a1", factory.create("a1"))
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	restoredAgent := factory.lastAgent()
	require.NotNil(t, restoredAgent, "factory should have created an agent")
	assert.Equal(t, "a1", restoredAgent.ID())

	// Agent should be running despite not implementing StatefulAgent.
	assert.Equal(t, models.AgentStatusReady, restoredAgent.Status())

	// GetAgent should return the restored agent.
	got := m.GetAgent("a1")
	assert.Equal(t, restoredAgent, got)
}

// TestManager_RestoreAgent_EventStoreError verifies that when the EventStore returns
// an error, the agent is still created and started without restored state.
func TestManager_RestoreAgent_EventStoreError(t *testing.T) {
	store := &errEventStore{readErr: fmt.Errorf("database connection lost")}
	factory := newMockFactory()
	m := New(nil, store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	err := m.RestoreAgent(ctx, "a1", factory.create())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	// Agent should have been created and started despite EventStore failure.
	restoredAgent := factory.lastAgent()
	require.NotNil(t, restoredAgent)

	// No ares_events were available, so RestoreState and ReplayEvents should not have been called.
	assert.Nil(t, restoredAgent.restoredState, "no state should be restored on event store error")
	assert.Nil(t, restoredAgent.replayedEvts, "no ares_events should be replayed on event store error")

	// Agent should still be running.
	assert.Equal(t, models.AgentStatusReady, restoredAgent.Status())
}

// TestManager_RestoreAgent_MemoryManagerError verifies that when the MemoryManager
// returns an error during cognitive recovery, the agent is still restored and started.
func TestManager_RestoreAgent_MemoryManagerError(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	memMgr := &errMemoryManager{
		sessionErr:  fmt.Errorf("checkpoint table corrupted"),
		messagesErr: fmt.Errorf("messages query timeout"),
	}
	factory := newMockFactory()
	m := New(nil, store, memMgr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Append a session event so buildCognitiveState tries to load messages.
	err := store.Append(ctx, "a1", []*ares_events.Event{
		{
			Type:    ares_events.EventSessionCreated,
			Payload: map[string]any{"session_id": "sess-err"},
		},
	}, 0)
	require.NoError(t, err)

	err = m.RestoreAgent(ctx, "a1", factory.create())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	restoredAgent := factory.lastAgent()
	require.NotNil(t, restoredAgent)

	// The operational state (session_id) should still be restored from ares_events.
	require.NotNil(t, restoredAgent.restoredState)
	assert.Equal(t, "sess-err", restoredAgent.restoredState["session_id"])

	// Conversation history should NOT be present because GetMessages returned an error.
	_, hasHistory := restoredAgent.restoredState["conversation_history"]
	assert.False(t, hasHistory, "conversation_history should be absent when GetMessages fails")

	// Agent should be running despite memory manager errors.
	assert.Equal(t, models.AgentStatusReady, restoredAgent.Status())
}

// TestManager_NotifyAgentDead_AfterStop verifies that NotifyAgentDead is a no-op
// when the runtime has been stopped, preventing resurrection of agents during shutdown.
func TestManager_NotifyAgentDead_AfterStop(t *testing.T) {
	factory := newMockFactory()
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	m.RegisterAgent(agent, factory.create())
	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	// Stop the runtime.
	require.NoError(t, m.Stop())

	// NotifyAgentDead after stop should be a no-op.
	m.NotifyAgentDead("a1", "late crash")
	time.Sleep(200 * time.Millisecond)

	factory.mu.Lock()
	callCount := factory.callCount
	factory.mu.Unlock()

	// Factory should NOT have been called because runtime is stopped.
	assert.Equal(t, 0, callCount, "factory should not be called after runtime is stopped")
}

// TestManager_NotifyAgentDead_NoFactory verifies that NotifyAgentDead correctly
// skips restoration when no factory is registered for the agent.
func TestManager_NotifyAgentDead_NoFactoryRegistered(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	// Start the agent without registering a factory.
	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	// NotifyAgentDead should be a no-op without panicking.
	assert.NotPanics(t, func() {
		m.NotifyAgentDead("a1", "crash without factory")
	})
	time.Sleep(200 * time.Millisecond)

	// Total restarts should remain zero since no factory was registered.
	stats := m.Stats()
	assert.Equal(t, 0, stats.TotalRestarts)
}

// TestManager_Stop_ConcurrentStopAgent verifies that Stop() and StopAgent() called
// concurrently do not cause panics or data races.
func TestManager_Stop_ConcurrentStopAgent(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Register and start multiple agents.
	for i := 0; i < 5; i++ {
		agent := newMockAgent(fmt.Sprintf("agent-%d", i))
		require.NoError(t, m.StartAgent(ctx, agent))
	}
	time.Sleep(100 * time.Millisecond)

	var wg sync.WaitGroup

	// Call StopAgent for specific agents concurrently.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
func TestManager_Stop_TimeoutRespected(t *testing.T) {
	config := &Config{
		HealthCheckInterval: 1 * time.Second,
		AgentStopTimeout:    100 * time.Millisecond,
		OverallStopTimeout:  500 * time.Millisecond,
	}
	m := New(config, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Register a slow-stop agent that blocks until its blocker channel is closed.
	slow := newSlowStopAgent("slow-agent")
	require.NoError(t, m.StartAgent(ctx, slow))
	time.Sleep(50 * time.Millisecond)

	// Stop the runtime. The slow agent should be cancelled by context timeout.
	start := time.Now()
	err := m.Stop()
	elapsed := time.Since(start)
	require.NoError(t, err)

	// Stop should complete in roughly AgentStopTimeout, not hang forever.
	assert.Less(t, elapsed, 5*time.Second,
		"Stop should not block indefinitely even with a slow agent")

	// The slow agent's Stop should have been called at least once.
	assert.GreaterOrEqual(t, slow.stopped.Load(), int32(1))
}

// TestManager_Stats_AfterMultipleOperations verifies that Stats() accurately reflects
// the runtime state after a series of register, start, stop, and restart operations.
func TestManager_Stats_AfterMultipleOperations(t *testing.T) {
func TestManager_GetAgent_AfterRestore(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Register and start the original agent.
	original := newMockAgent("a1")
	factory := newMockFactory()
	m.RegisterAgent(original, factory.create())
	require.NoError(t, m.StartAgent(ctx, original))
	time.Sleep(50 * time.Millisecond)

	// GetAgent should return the original agent.
	gotBefore := m.GetAgent("a1")
	assert.Equal(t, original, gotBefore, "GetAgent should return the original agent")

	// RestoreAgent creates a new instance from the factory.
	err := m.RestoreAgent(ctx, "a1", factory.create())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	// GetAgent should now return the new (restored) agent, not the original.
	gotAfter := m.GetAgent("a1")
	require.NotNil(t, gotAfter, "GetAgent should return the restored agent")
	assert.NotEqual(t, original, gotAfter, "GetAgent should return the new agent, not the original")

	// The restored agent should be a StatefulAgent from the factory.
	newAgent := factory.lastAgent()
	require.NotNil(t, newAgent)
	assert.Equal(t, newAgent, gotAfter)
}

package resurrection

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// --- mockStatefulAgent implements both base.Agent and base.StatefulAgent ---

type mockStatefulAgent struct {
	*mockAgent
	snapshotData map[string]any
	snapshotErr  error
	restoreErr   error
	restoreCalls atomic.Int32
}

func newMockStatefulAgent(id string, agentType models.AgentType) *mockStatefulAgent {
	return &mockStatefulAgent{
		mockAgent:    newMockAgent(id, agentType),
		snapshotData: map[string]any{"state": "data"},
	}
}

func (m *mockStatefulAgent) Snapshot() (map[string]any, error) {
	if m.snapshotErr != nil {
		return nil, m.snapshotErr
	}
	return m.snapshotData, nil
}

func (m *mockStatefulAgent) RestoreState(_ map[string]any) error {
	m.restoreCalls.Add(1)
	return m.restoreErr
}

func (m *mockStatefulAgent) ReplayEvents(_ []*ares_events.Event) error {
	return nil
}

// --- mockHeartbeaterAgent implements both base.Agent and base.Heartbeater ---

type mockHeartbeaterAgent struct {
	*mockAgent
	alive bool
}

func newMockHeartbeaterAgent(id string, agentType models.AgentType, alive bool) *mockHeartbeaterAgent {
	return &mockHeartbeaterAgent{
		mockAgent: newMockAgent(id, agentType),
		alive:     alive,
	}
}

func (m *mockHeartbeaterAgent) IsAlive() bool { return m.alive }

func (m *mockHeartbeaterAgent) Heartbeat(_ context.Context) error { return nil }

// --- sendHeartbeats ---

func TestSendHeartbeats_SkipsOfflineAgents(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     100 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	alive := newMockAgent("alive", models.AgentTypeLeader)
	offline := newMockAgent("offline", models.AgentTypeLeader)
	offline.setStatus(models.AgentStatusOffline)

	factory := func() base.Agent { return newMockAgent("", models.AgentTypeLeader) }
	sup.Watch(alive, factory)
	sup.Watch(offline, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	time.Sleep(200 * time.Millisecond)

	health.mu.Lock()
	aliveCount := 0
	offlineCount := 0
	for _, id := range health.alive {
		if id == "alive" {
			aliveCount++
		}
		if id == "offline" {
			offlineCount++
		}
	}
	health.mu.Unlock()

	assert.Greater(t, aliveCount, 0, "alive agent should receive heartbeats")
	assert.Equal(t, 0, offlineCount, "offline agent should not receive heartbeats")

	_ = sup.Stop()
}

func TestSendHeartbeats_EmptyWatched_NoPanic(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     100 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	time.Sleep(100 * time.Millisecond)

	_ = sup.Stop()
}

// --- WithSnapshotStore ---

func TestWithSnapshotStore_Chaining(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	store := NewMemorySnapshotStore()
	result := sup.WithSnapshotStore(store)

	assert.Same(t, sup, result)
	assert.NotNil(t, sup.snapshotStore)
}

func TestWithSnapshotStore_NilStore(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	result := sup.WithSnapshotStore(nil)
	assert.Same(t, sup, result)
	assert.Nil(t, sup.snapshotStore)
}

func TestWithSnapshotStore_OverwritesPrevious(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	store1 := NewMemorySnapshotStore()
	store2 := NewMemorySnapshotStore()

	sup.WithSnapshotStore(store1)
	sup.WithSnapshotStore(store2)

	assert.Same(t, store2, sup.snapshotStore)
}

// --- takeSnapshots ---

func TestTakeSnapshots_PersistsStatefulAgent(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	store := NewMemorySnapshotStore()
	sup.WithSnapshotStore(store)

	agent := newMockStatefulAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	sup.takeSnapshots()

	ctx := context.Background()
	snap, err := store.Load(ctx, "a1")
	require.NoError(t, err)
	assert.Equal(t, "data", snap["state"])
}

func TestTakeSnapshots_SkipsNonStatefulAgent(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	store := NewMemorySnapshotStore()
	sup.WithSnapshotStore(store)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	sup.takeSnapshots()

	ctx := context.Background()
	_, err = store.Load(ctx, "a1")
	assert.ErrorIs(t, err, ErrSnapshotNotFound)
}

func TestTakeSnapshots_NoStore_IsNoOp(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockStatefulAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	sup.takeSnapshots()
}

func TestTakeSnapshots_NilSnapshot_NotSaved(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	store := NewMemorySnapshotStore()
	sup.WithSnapshotStore(store)

	agent := newMockStatefulAgent("a1", models.AgentTypeLeader)
	agent.snapshotData = nil
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	sup.takeSnapshots()

	ctx := context.Background()
	_, err = store.Load(ctx, "a1")
	assert.ErrorIs(t, err, ErrSnapshotNotFound)
}

func TestTakeSnapshots_SnapshotError_Skipped(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	store := NewMemorySnapshotStore()
	sup.WithSnapshotStore(store)

	agent := newMockStatefulAgent("a1", models.AgentTypeLeader)
	agent.snapshotErr = assert.AnError
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	sup.takeSnapshots()

	ctx := context.Background()
	_, err = store.Load(ctx, "a1")
	assert.ErrorIs(t, err, ErrSnapshotNotFound)
}

func TestTakeSnapshots_SaveError_DoesNotPanic(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	store := NewMemorySnapshotStore()
	sup.WithSnapshotStore(store)

	agent := newMockStatefulAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	sup.takeSnapshots()
}

func TestTakeSnapshots_MultipleStatefulAgents(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	store := NewMemorySnapshotStore()
	sup.WithSnapshotStore(store)

	for i := 0; i < 3; i++ {
		id := string(rune('a' + i))
		agent := newMockStatefulAgent(id, models.AgentTypeLeader)
		agent.snapshotData = map[string]any{"id": id}
		factory := func() base.Agent { return newMockAgent(id, models.AgentTypeLeader) }
		sup.Watch(agent, factory)
	}

	sup.takeSnapshots()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		id := string(rune('a' + i))
		snap, err := store.Load(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, id, snap["id"])
	}
}

// --- verifyResurrection ---

func TestVerifyResurrection_HealthyAgent_ReturnsTrue(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	healthy := sup.verifyResurrection("a1", agent)
	assert.True(t, healthy)
}

func TestVerifyResurrection_OfflineAgent_ReturnsFalse(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	agent.setStatus(models.AgentStatusOffline)
	healthy := sup.verifyResurrection("a1", agent)
	assert.False(t, healthy)
}

func TestVerifyResurrection_UnhealthyHeartbeater_ReturnsFalse(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockHeartbeaterAgent("a1", models.AgentTypeLeader, false)
	healthy := sup.verifyResurrection("a1", agent)
	assert.False(t, healthy)
}

func TestVerifyResurrection_HealthyHeartbeater_ReturnsTrue(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockHeartbeaterAgent("a1", models.AgentTypeLeader, true)
	healthy := sup.verifyResurrection("a1", agent)
	assert.True(t, healthy)
}

func TestVerifyResurrection_UnhealthyTriggersOnFailure(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	agent.setStatus(models.AgentStatusOffline)

	healthy := sup.verifyResurrection("a1", agent)
	assert.False(t, healthy)
}

// --- backoffWait ---

func TestBackoffWait_SkipsWhenExhausted(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second, MaxAttempts: 3}, nil)
	require.NoError(t, err)

	backoff := time.Second
	sup.backoffWait(sup.config.MaxAttempts, &backoff)
	assert.Equal(t, time.Second, backoff, "backoff should not be doubled when exhausted")
}

func TestBackoffWait_DoublesBackoff(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     1 * time.Second,
		MaxAttempts:       5,
		MaxBackoff:        10 * time.Second,
		InitialBackoff:    10 * time.Millisecond,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	backoff := sup.config.InitialBackoff
	sup.backoffWait(1, &backoff)
	assert.Equal(t, 2*sup.config.InitialBackoff, backoff, "backoff should be doubled")

	_ = sup.Stop()
}

func TestBackoffWait_CapsAtMax(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     1 * time.Second,
		MaxAttempts:       5,
		MaxBackoff:        50 * time.Millisecond,
		InitialBackoff:    40 * time.Millisecond,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	backoff := sup.config.InitialBackoff
	sup.backoffWait(1, &backoff)
	assert.Equal(t, sup.config.MaxBackoff, backoff, "doubled backoff should be capped at MaxBackoff")

	_ = sup.Stop()
}

// --- onFailure ---

func TestOnFailure_StoppedSupervisor_IsNoOp(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))
	require.NoError(t, sup.Stop())

	health.triggerFailure("a1")
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 0, sup.Stats().Resurrects)
}

func TestOnFailure_UnknownAgent_IsNoOp(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	health.triggerFailure("unknown")
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 0, sup.Stats().Resurrects)

	_ = sup.Stop()
}

func TestOnFailure_DuplicateResurrections_Deduped(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
		InitialBackoff:    50 * time.Millisecond,
		MaxBackoff:        200 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	health.triggerFailure("a1")
	health.triggerFailure("a1")
	health.triggerFailure("a1")

	time.Sleep(500 * time.Millisecond)

	stats := sup.Stats()
	assert.GreaterOrEqual(t, stats.Resurrects, 1)

	_ = sup.Stop()
}

// --- NewHeartbeatAdapter edge cases ---

func TestNewHeartbeatAdapter_NilMonitor_ReturnsNil(t *testing.T) {
	adapter := NewHeartbeatAdapter(nil)
	assert.Nil(t, adapter)
}

func TestHeartbeatAdapter_CheckHealth_NilToEmptySlice(t *testing.T) {
	mon := ahp.NewHeartbeatMonitor(nil)
	adapter := NewHeartbeatAdapter(mon)

	result := adapter.CheckHealth()
	require.NotNil(t, result)
	assert.Empty(t, result)
}

// --- replayEvents additional paths ---

func TestReplayEvents_WithAgentStartedEvent(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer store.Close()

	agentID := "agent-1"
	err := store.Append(context.Background(), agentID, []*ares_events.Event{
		{Type: ares_events.EventAgentStarted, Payload: map[string]any{}},
	}, 0)
	require.NoError(t, err)

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), agentID)
	require.NotNil(t, state)
	assert.Equal(t, string(models.AgentStatusReady), state["agent_status"])
}

func TestReplayEvents_WithAgentStoppedEvent(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer store.Close()

	agentID := "agent-1"
	err := store.Append(context.Background(), agentID, []*ares_events.Event{
		{Type: ares_events.EventAgentStopped, Payload: map[string]any{}},
	}, 0)
	require.NoError(t, err)

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), agentID)
	require.NotNil(t, state)
	assert.Equal(t, string(models.AgentStatusOffline), state["agent_status"])
}

func TestReplayEvents_AgentStatusSequence_LastWins(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer store.Close()

	agentID := "agent-1"
	err := store.Append(context.Background(), agentID, []*ares_events.Event{
		{Type: ares_events.EventAgentStarted, Payload: map[string]any{}},
		{Type: ares_events.EventAgentStopped, Payload: map[string]any{}},
		{Type: ares_events.EventAgentStarted, Payload: map[string]any{}},
	}, 0)
	require.NoError(t, err)

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), agentID)
	require.NotNil(t, state)
	assert.Equal(t, string(models.AgentStatusReady), state["agent_status"])
}

func TestReplayEvents_InvalidTaskIDType_SkipsField(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer store.Close()

	agentID := "agent-1"
	err := store.Append(context.Background(), agentID, []*ares_events.Event{
		{
			Type: ares_events.EventTaskCreated,
			Payload: map[string]any{
				"task_id": 123,
			},
		},
	}, 0)
	require.NoError(t, err)

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), agentID)
	require.NotNil(t, state)
	_, hasTaskID := state["last_task_id"]
	assert.False(t, hasTaskID)
}

func TestReplayEvents_EmptyTaskID_SkipsField(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	defer store.Close()

	agentID := "agent-1"
	err := store.Append(context.Background(), agentID, []*ares_events.Event{
		{
			Type: ares_events.EventTaskCreated,
			Payload: map[string]any{
				"task_id": "",
			},
		},
	}, 0)
	require.NoError(t, err)

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), agentID)
	require.NotNil(t, state)
	_, hasTaskID := state["last_task_id"]
	assert.False(t, hasTaskID)
}

func TestReplayEvents_StoreReadError_ReturnsNil(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	store.Close()

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), "agent-1")
	assert.Nil(t, state)
}

// --- Watch edge cases ---

func TestWatch_EmptyAgentID_IsNoOp(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockAgent("", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	stats := sup.Stats()
	assert.Equal(t, 0, stats.Watched)
}

// --- Snapshot-based recovery in resurrection ---

func TestResurrection_WithSnapshotStore_RestoresState(t *testing.T) {
	health := newMockHealthChecker()
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	err := store.Save(ctx, "a1", map[string]any{"restored": "value"})
	require.NoError(t, err)

	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
		InitialBackoff:    50 * time.Millisecond,
		MaxBackoff:        200 * time.Millisecond,
	}, nil)
	require.NoError(t, err)
	sup.WithSnapshotStore(store)

	original := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent {
		return newMockStatefulAgent("a1", models.AgentTypeLeader)
	}
	sup.Watch(original, factory)

	ctx2, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx2))

	health.triggerFailure("a1")
	time.Sleep(500 * time.Millisecond)

	stats := sup.Stats()
	assert.Equal(t, 1, stats.Resurrects)

	newAgent := sup.Agent("a1")
	require.NotNil(t, newAgent)
	sa, ok := newAgent.(*mockStatefulAgent)
	require.True(t, ok)
	assert.GreaterOrEqual(t, sa.restoreCalls.Load(), int32(1))

	_ = sup.Stop()
}

func TestResurrection_WithSnapshotStore_FallbackToEvents(t *testing.T) {
	health := newMockHealthChecker()
	store := NewMemorySnapshotStore()

	eventStore := ares_events.NewMemoryEventStore()
	defer eventStore.Close()

	err := eventStore.Append(context.Background(), "a1", []*ares_events.Event{
		{
			Type: ares_events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "session-from-events",
			},
		},
	}, 0)
	require.NoError(t, err)

	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
		InitialBackoff:    50 * time.Millisecond,
		MaxBackoff:        200 * time.Millisecond,
	}, eventStore)
	require.NoError(t, err)
	sup.WithSnapshotStore(store)

	original := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent {
		return newMockStatefulAgent("a1", models.AgentTypeLeader)
	}
	sup.Watch(original, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	health.triggerFailure("a1")
	time.Sleep(500 * time.Millisecond)

	stats := sup.Stats()
	assert.Equal(t, 1, stats.Resurrects)

	newAgent := sup.Agent("a1")
	require.NotNil(t, newAgent)
	sa, ok := newAgent.(*mockStatefulAgent)
	if ok {
		assert.GreaterOrEqual(t, sa.restoreCalls.Load(), int32(1))
	}

	_ = sup.Stop()
}

// --- Concurrent operations ---

func TestConcurrent_WatchAndSendHeartbeats_NoRace(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := "agent-1"
			agent := newMockAgent(id, models.AgentTypeLeader)
			factory := func() base.Agent { return newMockAgent(id, models.AgentTypeLeader) }
			sup.Watch(agent, factory)
			sup.Unwatch(id)
		}(i)
	}

	wg.Wait()
	_ = sup.Stop()
}

func TestConcurrent_StatsDuringResurrection_NoRace(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sup.Stats()
		}()
	}

	health.triggerFailure("a1")
	time.Sleep(300 * time.Millisecond)
	wg.Wait()

	_ = sup.Stop()
}

// --- Config defaults ---

func TestDefaultConfig_Values(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, 10*time.Second, cfg.CheckInterval)
	assert.Equal(t, 60*time.Second, cfg.ResurrectTimeout)
	assert.Equal(t, 3, cfg.MaxAttempts)
	assert.Equal(t, 5*time.Second, cfg.HeartbeatInterval)
	assert.Equal(t, 30*time.Second, cfg.MaxBackoff)
	assert.Equal(t, time.Second, cfg.InitialBackoff)
	assert.Equal(t, time.Duration(0), cfg.SnapshotInterval)
}

func TestConfig_SnapshotIntervalPreserved(t *testing.T) {
	health := newMockHealthChecker()
	cfg := Config{
		CheckInterval:     5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
		SnapshotInterval:  30 * time.Second,
	}
	sup, err := New(health, cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, sup.config.SnapshotInterval)
}

// --- Stop with error group ---

func TestStop_WithError_DoesNotPanic(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, sup.Stop())

	cancel()
}

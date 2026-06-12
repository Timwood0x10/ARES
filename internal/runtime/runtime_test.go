package runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goagent/internal/agents/base"
	"goagent/internal/core/models"
	"goagent/internal/events"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock implementations ---

// mockAgent implements base.Agent for testing.
type mockAgent struct {
	mu       sync.RWMutex
	id       string
	agentTyp models.AgentType
	status   models.AgentStatus
	startFn  func(ctx context.Context) error
	stopFn   func(ctx context.Context) error
	started  atomic.Int32
	stopped  atomic.Int32
}

func newMockAgent(id string) *mockAgent {
	return &mockAgent{
		id:       id,
		agentTyp: models.AgentTypeLeader,
		status:   models.AgentStatusOffline,
	}
}

func (a *mockAgent) ID() string                 { return a.id }
func (a *mockAgent) Type() models.AgentType     { return a.agentTyp }
func (a *mockAgent) Status() models.AgentStatus { a.mu.RLock(); defer a.mu.RUnlock(); return a.status }

func (a *mockAgent) setStatus(s models.AgentStatus) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
}

func (a *mockAgent) Start(ctx context.Context) error {
	a.started.Add(1)
	if a.startFn != nil {
		if err := a.startFn(ctx); err != nil {
			return err
		}
	}
	a.setStatus(models.AgentStatusReady)
	// Block until context cancelled to simulate a long-running agent.
	<-ctx.Done()
	a.setStatus(models.AgentStatusOffline)
	return nil
}

func (a *mockAgent) Stop(ctx context.Context) error {
	a.stopped.Add(1)
	if a.stopFn != nil {
		return a.stopFn(ctx)
	}
	a.setStatus(models.AgentStatusOffline)
	return nil
}

func (a *mockAgent) Process(ctx context.Context, input any) (any, error) {
	return nil, nil
}

func (a *mockAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent)
	close(ch)
	return ch, nil
}

// mockStatefulAgent implements both base.Agent and base.StatefulAgent.
type mockStatefulAgent struct {
	*mockAgent
	restoreStateFn func(state map[string]any) error
	replayEventsFn func(evts []*events.Event) error
	snapshotFn     func() (map[string]any, error)
	restoredState  map[string]any
	replayedEvts   []*events.Event
}

func newMockStatefulAgent(id string) *mockStatefulAgent {
	return &mockStatefulAgent{
		mockAgent: newMockAgent(id),
	}
}

func (a *mockStatefulAgent) RestoreState(state map[string]any) error {
	a.restoredState = state
	if a.restoreStateFn != nil {
		return a.restoreStateFn(state)
	}
	return nil
}

func (a *mockStatefulAgent) ReplayEvents(evts []*events.Event) error {
	a.replayedEvts = evts
	if a.replayEventsFn != nil {
		return a.replayEventsFn(evts)
	}
	return nil
}

func (a *mockStatefulAgent) Snapshot() (map[string]any, error) {
	if a.snapshotFn != nil {
		return a.snapshotFn()
	}
	return map[string]any{"agent_id": a.id}, nil
}

// mockFactory creates a factory function that tracks calls.
type mockFactory struct {
	mu        sync.Mutex
	agents    []*mockStatefulAgent
	callCount int
}

func newMockFactory() *mockFactory {
	return &mockFactory{}
}

func (f *mockFactory) create() AgentFactory {
	return func() base.Agent {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.callCount++
		a := newMockStatefulAgent(fmt.Sprintf("agent-%d", f.callCount))
		f.agents = append(f.agents, a)
		return a
	}
}

func (f *mockFactory) lastAgent() *mockStatefulAgent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.agents) == 0 {
		return nil
	}
	return f.agents[len(f.agents)-1]
}

// --- Tests ---

func TestManager_RegisterAgent(t *testing.T) {
	m := New(nil, nil, nil)
	agent := newMockAgent("a1")
	factory := func() base.Agent { return newMockAgent("a1") }

	m.RegisterAgent(agent, factory)

	// Registered agent is tracked in the map (but Offline, so not counted as active).
	m.mu.RLock()
	_, exists := m.agents["a1"]
	m.mu.RUnlock()
	assert.True(t, exists, "agent should be in the agents map")
}

func TestManager_RegisterAgent_NilAgent(t *testing.T) {
	m := New(nil, nil, nil)
	factory := func() base.Agent { return newMockAgent("a1") }

	// Should not panic, just no-op.
	m.RegisterAgent(nil, factory)

	stats := m.Stats()
	assert.Equal(t, 0, stats.ActiveAgents)
}

func TestManager_RegisterAgent_NilFactory(t *testing.T) {
	m := New(nil, nil, nil)
	agent := newMockAgent("a1")

	// Should not panic, just no-op.
	m.RegisterAgent(agent, nil)

	stats := m.Stats()
	assert.Equal(t, 0, stats.ActiveAgents)
}

func TestManager_StartAgent(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	err := m.StartAgent(ctx, agent)
	require.NoError(t, err)

	// Give the goroutine time to call Start.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), agent.started.Load())
	assert.Equal(t, models.AgentStatusReady, agent.Status())

	stats := m.Stats()
	assert.Equal(t, 1, stats.ActiveAgents)
}

func TestManager_StartAgent_NilAgent(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	err := m.StartAgent(ctx, nil)
	assert.ErrorIs(t, err, ErrNilAgent)
}

func TestManager_StartAgent_AlreadyRegistered(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	require.NoError(t, m.StartAgent(ctx, agent))

	// Second start with same ID should fail.
	err := m.StartAgent(ctx, newMockAgent("a1"))
	assert.ErrorIs(t, err, ErrAgentAlreadyRegistered)
}

func TestManager_StopAgent(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	err := m.StopAgent(ctx, "a1")
	require.NoError(t, err)

	// Agent should have been stopped.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), agent.stopped.Load())
	assert.Equal(t, models.AgentStatusOffline, agent.Status())

	stats := m.Stats()
	assert.Equal(t, 0, stats.ActiveAgents)
}

func TestManager_StopAgent_NotFound(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	err := m.StopAgent(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestManager_RestartAgent(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	factory := func() base.Agent { return newMockAgent("a1") }
	m.RegisterAgent(agent, factory)

	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	err := m.RestartAgent(ctx, "a1")
	require.NoError(t, err)

	// Old agent should be stopped.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), agent.stopped.Load())

	// Stats should reflect the restart.
	stats := m.Stats()
	assert.Equal(t, 1, stats.TotalRestarts)
	assert.Equal(t, 1, stats.ActiveAgents)
}

func TestManager_RestartAgent_NotFound(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	err := m.RestartAgent(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

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
	eventStore := events.NewMemoryEventStore()
	m := New(nil, eventStore, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Pre-populate events in the store using the agent:streamID convention.
	err := eventStore.Append(ctx, "a1", []*events.Event{
		{
			Type: events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "sess-123",
			},
		},
		{
			Type: events.EventTaskCreated,
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

	// Verify events were replayed.
	newAgent := factory.lastAgent()
	require.NotNil(t, newAgent)
	assert.NotNil(t, newAgent.replayedEvts, "events should have been replayed")
	assert.Len(t, newAgent.replayedEvts, 2)
}

func TestManager_RestoreAgent_RestoresState(t *testing.T) {
	eventStore := events.NewMemoryEventStore()
	m := New(nil, eventStore, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Pre-populate with session event using the agent:streamID convention.
	err := eventStore.Append(ctx, "a1", []*events.Event{
		{
			Type: events.EventSessionCreated,
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

func TestManager_Stats(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	stats := m.Stats()
	assert.Equal(t, 1, stats.ActiveAgents)
	assert.Equal(t, 0, stats.TotalRestarts)
	assert.Greater(t, stats.Uptime, time.Duration(0))
}

func TestManager_Start_NilContext(t *testing.T) {
	m := New(nil, nil, nil)

	err := m.Start(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context must not be nil")
}

func TestManager_DoubleStart(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	err := m.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already started")
}

func TestManager_DoubleStop(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	require.NoError(t, m.Stop())

	// Second stop should be a no-op (no error).
	err := m.Stop()
	assert.NoError(t, err)
}

func TestManager_PanicRecovery(t *testing.T) {
	factory := newMockFactory()
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Agent that panics on start.
	panicAgent := &panicMockAgent{
		mockAgent: newMockAgent("panic-agent"),
	}
	m.RegisterAgent(panicAgent, factory.create())

	// StartAgent should not propagate the panic.
	err := m.StartAgent(ctx, panicAgent)
	require.NoError(t, err)

	// Wait for the panic to be caught and restoration triggered.
	time.Sleep(300 * time.Millisecond)

	// Factory should have been called for restoration.
	factory.mu.Lock()
	callCount := factory.callCount
	factory.mu.Unlock()
	assert.GreaterOrEqual(t, callCount, 1, "factory should have been called after panic")
}

// panicMockAgent panics during Start.
type panicMockAgent struct {
	*mockAgent
}

func (a *panicMockAgent) Start(ctx context.Context) error {
	panic("intentional test panic")
}

func TestManager_StopAgent_StopsAgentContext(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	require.NoError(t, m.StartAgent(ctx, agent))

	// Wait for agent goroutine to start.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, models.AgentStatusReady, agent.Status())

	// Stop should cancel the agent context, causing Start to return.
	err := m.StopAgent(ctx, "a1")
	require.NoError(t, err)

	// The mock agent's Start listens on ctx.Done() and sets status to Offline.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, models.AgentStatusOffline, agent.Status())
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
	// No events to replay, so replayedEvts should be nil.
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Agent that returns error on Start.
	failAgent := newMockAgent("fail-agent")
	failAgent.startFn = func(ctx context.Context) error {
		return fmt.Errorf("start failure")
	}

	err := m.StartAgent(ctx, failAgent)
	// StartAgent itself should succeed (error is handled internally).
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
}

func TestManager_Stop_RuntimeNotStarted(t *testing.T) {
	m := New(nil, nil, nil)

	// Stop before start should be a no-op.
	err := m.Stop()
	assert.NoError(t, err)
}

func TestManager_StartAgent_RuntimeStopped(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))
	require.NoError(t, m.Stop())

	agent := newMockAgent("a1")
	err := m.StartAgent(ctx, agent)
	assert.ErrorIs(t, err, ErrRuntimeStopped)
}

// TestManager_StartAgent_BeforeStart verifies that calling StartAgent before
// Start() does not panic. The errgroup is initialized in New(), so m.g.Go()
// is safe even without Start().
func TestManager_StartAgent_BeforeStart(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent := newMockAgent("a1")
	// Before the fix, this would panic because m.g was nil.
	// After the fix, the errgroup is initialized in New() with a background context.
	assert.NotPanics(t, func() {
		_ = m.StartAgent(ctx, agent)
	})
}

// TestManager_NotifyAgentDead_BeforeStart verifies that calling NotifyAgentDead
// before Start() does not panic.
func TestManager_NotifyAgentDead_BeforeStart(t *testing.T) {
	factory := newMockFactory()
	m := New(nil, nil, nil)

	agent := newMockAgent("a1")
	m.RegisterAgent(agent, factory.create())

	assert.NotPanics(t, func() {
		m.NotifyAgentDead("a1", "test before start")
	})
}

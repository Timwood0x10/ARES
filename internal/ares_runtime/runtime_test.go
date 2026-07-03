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
	replayEventsFn func(evts []*ares_events.Event) error
	snapshotFn     func() (map[string]any, error)
	restoredState  map[string]any
	replayedEvts   []*ares_events.Event
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

func (a *mockStatefulAgent) ReplayEvents(evts []*ares_events.Event) error {
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

// mockMemoryManager is a MemoryManager implementation for testing.
type mockMemoryManager struct {
	getLatestSessionCalls int
	getMessagesCalls      int
}

func newMockMemoryManager() *mockMemoryManager {
	return &mockMemoryManager{}
}

func (m *mockMemoryManager) CreateSession(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockMemoryManager) AddMessage(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *mockMemoryManager) GetMessages(_ context.Context, _ string) ([]memory.Message, error) {
	m.getMessagesCalls++
	return []memory.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}, nil
}

func (m *mockMemoryManager) DeleteSession(_ context.Context, _ string) error {
	return nil
}

func (m *mockMemoryManager) BuildContext(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (m *mockMemoryManager) CreateTask(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

func (m *mockMemoryManager) UpdateTaskOutput(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockMemoryManager) DistillTask(_ context.Context, _ string) (*models.Task, error) {
	return nil, nil
}

func (m *mockMemoryManager) StoreDistilledTask(_ context.Context, _ string, _ *models.Task) error {
	return nil
}

func (m *mockMemoryManager) SearchSimilarTasks(_ context.Context, _ string, _ int) ([]*models.Task, error) {
	return nil, nil
}

func (m *mockMemoryManager) GetLatestSessionForLeader(_ context.Context, _ string) (string, error) {
	m.getLatestSessionCalls++
	return "sess-mem", nil
}

func (m *mockMemoryManager) Start(_ context.Context) error { return nil }
func (m *mockMemoryManager) Stop(_ context.Context) error  { return nil }
func (m *mockMemoryManager) SetEventStore(_ ares_events.EventStore, _ string) {
}

func (m *mockMemoryManager) AddStructuredMessage(_ context.Context, _ string, _ memory.Message) error {
	return nil
}

func (m *mockMemoryManager) BuildPromptMessages(_ context.Context, _ string) ([]memory.Message, error) {
	return nil, nil
}

// errEventStore is an EventStore that returns an error on Read.
type errEventStore struct {
	readErr error
}

func (s *errEventStore) Append(_ context.Context, _ string, _ []*ares_events.Event, _ int64) error {
	return nil
}

func (s *errEventStore) Read(_ context.Context, _ string, _ ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, s.readErr
}

func (s *errEventStore) ReadAll(_ context.Context, _ ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, s.readErr
}

func (s *errEventStore) Subscribe(_ context.Context, _ ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	ch := make(chan *ares_events.Event)
	close(ch)
	return ch, nil
}

func (s *errEventStore) StreamVersion(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

// errMemoryManager is a MemoryManager that returns errors on session/message retrieval.
type errMemoryManager struct {
	sessionErr  error
	messagesErr error
}

func (m *errMemoryManager) CreateSession(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *errMemoryManager) AddMessage(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *errMemoryManager) GetMessages(_ context.Context, _ string) ([]memory.Message, error) {
	return nil, m.messagesErr
}

func (m *errMemoryManager) DeleteSession(_ context.Context, _ string) error {
	return nil
}

func (m *errMemoryManager) BuildContext(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (m *errMemoryManager) CreateTask(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

func (m *errMemoryManager) UpdateTaskOutput(_ context.Context, _, _ string) error {
	return nil
}

func (m *errMemoryManager) DistillTask(_ context.Context, _ string) (*models.Task, error) {
	return nil, nil
}

func (m *errMemoryManager) StoreDistilledTask(_ context.Context, _ string, _ *models.Task) error {
	return nil
}

func (m *errMemoryManager) SearchSimilarTasks(_ context.Context, _ string, _ int) ([]*models.Task, error) {
	return nil, nil
}

func (m *errMemoryManager) GetLatestSessionForLeader(_ context.Context, _ string) (string, error) {
	return "", m.sessionErr
}

func (m *errMemoryManager) Start(_ context.Context) error { return nil }
func (m *errMemoryManager) Stop(_ context.Context) error  { return nil }
func (m *errMemoryManager) SetEventStore(_ ares_events.EventStore, _ string) {
}

func (m *errMemoryManager) AddStructuredMessage(_ context.Context, _ string, _ memory.Message) error {
	return nil
}

func (m *errMemoryManager) BuildPromptMessages(_ context.Context, _ string) ([]memory.Message, error) {
	return nil, nil
}

// slowStopAgent blocks in Stop until the provided channel is closed or context expires.
type slowStopAgent struct {
	*mockAgent
	stopBlocker chan struct{}
}

func newSlowStopAgent(id string) *slowStopAgent {
	return &slowStopAgent{
		mockAgent:   newMockAgent(id),
		stopBlocker: make(chan struct{}),
	}
}

func (a *slowStopAgent) Stop(ctx context.Context) error {
	a.stopped.Add(1)
	select {
	case <-a.stopBlocker:
	case <-ctx.Done():
	}
	return nil
}

// nonStatefulFactory creates agents that do NOT implement StatefulAgent.
type nonStatefulFactory struct {
	mu     sync.Mutex
	agents []*mockAgent
}

func newNonStatefulFactory() *nonStatefulFactory {
	return &nonStatefulFactory{}
}

func (f *nonStatefulFactory) create(id string) AgentFactory {
	return func() base.Agent {
		f.mu.Lock()
		defer f.mu.Unlock()
		a := newMockAgent(id)
		f.agents = append(f.agents, a)
		return a
	}
}

func (f *nonStatefulFactory) lastAgent() *mockAgent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.agents) == 0 {
		return nil
	}
	return f.agents[len(f.agents)-1]
}

// --- Tests ---



func TestManager_Start_NilContext(t *testing.T) {
	m := New(nil, nil, nil)

	var ctx context.Context // nil context
	err := m.Start(ctx)     //nolint:staticcheck // Intentionally testing nil context guard.
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

// TestManager_RestoreAgent_WithEventStore verifies that RestoreAgent replays ares_events
// from the EventStore and passes them to a StatefulAgent.
			_ = m.StopAgent(ctx, id)
		}(fmt.Sprintf("agent-%d", i))
	}

	// Call Stop() concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = m.Stop()
	}()

	wg.Wait()

	// All agents should be stopped.
	stats := m.Stats()
	assert.Equal(t, 0, stats.ActiveAgents)
}

// TestManager_Stop_WithRunningAgents verifies that Stop() gracefully stops all
// currently running agents.
func TestManager_Stop_WithRunningAgents(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agents := make([]*mockAgent, 5)
	for i := 0; i < 5; i++ {
		agents[i] = newMockAgent(fmt.Sprintf("agent-%d", i))
		require.NoError(t, m.StartAgent(ctx, agents[i]))
	}
	time.Sleep(100 * time.Millisecond)

	// Verify all agents are active.
	stats := m.Stats()
	assert.Equal(t, 5, stats.ActiveAgents)

	// Stop the runtime.
	require.NoError(t, m.Stop())

	// All agents should have had Stop called.
	for i, agent := range agents {
		assert.GreaterOrEqual(t, agent.stopped.Load(), int32(1),
			"agent-%d should have been stopped", i)
	}

	// No agents should remain active.
	stats = m.Stats()
	assert.Equal(t, 0, stats.ActiveAgents)
}

// TestManager_Stop_TimeoutRespected verifies that agents which take too long to stop
// are cancelled via context and Stop() still completes within the configured timeout.
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Register and start two agents.
	agent1 := newMockAgent("a1")
	agent2 := newMockAgent("a2")
	factory1 := func() base.Agent { return newMockAgent("a1") }
	factory2 := func() base.Agent { return newMockAgent("a2") }

	m.RegisterAgent(agent1, factory1)
	m.RegisterAgent(agent2, factory2)

	require.NoError(t, m.StartAgent(ctx, agent1))
	require.NoError(t, m.StartAgent(ctx, agent2))
	time.Sleep(100 * time.Millisecond)

	stats := m.Stats()
	assert.Equal(t, 2, stats.ActiveAgents)
	assert.Equal(t, 0, stats.TotalRestarts)

	// Stop one agent.
	require.NoError(t, m.StopAgent(ctx, "a1"))
	time.Sleep(50 * time.Millisecond)

	stats = m.Stats()
	assert.Equal(t, 1, stats.ActiveAgents)
	assert.Equal(t, 0, stats.TotalRestarts)

	// Restart the other agent.
	require.NoError(t, m.RestartAgent(ctx, "a2"))
	time.Sleep(100 * time.Millisecond)

	stats = m.Stats()
	assert.Equal(t, 1, stats.ActiveAgents, "restarted agent should count as one active agent")
	assert.Equal(t, 1, stats.TotalRestarts, "restart should increment total restarts")
	assert.Greater(t, stats.Uptime, time.Duration(0), "uptime should be positive after operations")
}

// TestManager_Stats_ConcurrentAccess verifies that Stats() is safe to call
// while agents are being registered and stopped concurrently.
func TestManager_Stats_ConcurrentAccess(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	var wg sync.WaitGroup
	errs := make(chan error, 30)

	// Concurrently register and start agents.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agentID := fmt.Sprintf("concurrent-agent-%d", id)
			agent := newMockAgent(agentID)
			if err := m.StartAgent(ctx, agent); err != nil {
				errs <- err
			}
		}(i)
	}

	// Concurrently read Stats.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stats := m.Stats()
			// ActiveAgents must be non-negative.
			if stats.ActiveAgents < 0 {
				errs <- fmt.Errorf("negative ActiveAgents: %d", stats.ActiveAgents)
			}
		}()
	}

	// Concurrently stop agents.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agentID := fmt.Sprintf("concurrent-agent-%d", id)
			_ = m.StopAgent(ctx, agentID)
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent operation error: %v", err)
	}
}

// TestManager_GetAgent_Exists verifies that GetAgent returns the correct agent
// instance when the agent is registered and running.
func TestManager_GetAgent_Exists(t *testing.T) {
	m := New(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	agent := newMockAgent("a1")
	require.NoError(t, m.StartAgent(ctx, agent))
	time.Sleep(50 * time.Millisecond)

	got := m.GetAgent("a1")
	require.NotNil(t, got, "GetAgent should return the registered agent")
	assert.Equal(t, "a1", got.ID())
}

// TestManager_GetAgent_NotExists verifies that GetAgent returns nil
// when the requested agent ID is not registered.
func TestManager_GetAgent_NotExists(t *testing.T) {
	m := New(nil, nil, nil)

	got := m.GetAgent("nonexistent")
	assert.Nil(t, got, "GetAgent should return nil for unregistered agent")
}

// TestManager_GetAgent_AfterRestore verifies that GetAgent returns the new agent
// instance after a RestoreAgent call replaces the original agent.

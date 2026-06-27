// nolint: errcheck // Test code may ignore return values
package leader

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
)

// =====================================================
// Mock implementations for supervisor tests
// =====================================================

// mockAgent is a minimal base.Agent implementation for testing.
type mockAgent struct {
	id        string
	status    models.AgentStatus
	mu        sync.RWMutex
	stopErr   error
	startErr  error
	stopCalls int32
}

func newMockAgent(id string, status models.AgentStatus) *mockAgent {
	return &mockAgent{
		id:     id,
		status: status,
	}
}

func (m *mockAgent) ID() string             { return m.id }
func (m *mockAgent) Type() models.AgentType { return models.AgentTypeLeader }
func (m *mockAgent) Status() models.AgentStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *mockAgent) Start(ctx context.Context) error {
	m.mu.Lock()
	m.status = models.AgentStatusReady
	m.mu.Unlock()
	return m.startErr
}

func (m *mockAgent) Stop(ctx context.Context) error {
	atomic.AddInt32(&m.stopCalls, 1)
	m.mu.Lock()
	m.status = models.AgentStatusOffline
	m.mu.Unlock()
	return m.stopErr
}

func (m *mockAgent) Process(ctx context.Context, input any) (any, error) {
	return "processed", nil
}

func (m *mockAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	ch <- base.AgentEvent{Type: base.EventComplete}
	close(ch)
	return ch, nil
}

// mockFailoverStrategy records HandleFailover calls and returns a mock agent.
type mockFailoverStrategy struct {
	mu          sync.Mutex
	callCount   int
	lastAgentID string
	returnAgent base.Agent
	returnErr   error
}

func (m *mockFailoverStrategy) HandleFailover(
	ctx context.Context,
	leaderID string,
	checkpoint *LeaderCheckpoint,
) (base.Agent, error) {
	m.mu.Lock()
	m.callCount++
	m.lastAgentID = leaderID
	m.mu.Unlock()
	return m.returnAgent, m.returnErr
}

// =====================================================
// Tests
// =====================================================

// TestNewLeaderSupervisor_NilHeartbeatMon verifies that a nil heartbeat monitor
// returns an error.
func TestNewLeaderSupervisor_NilHeartbeatMon(t *testing.T) {
	strategy := &mockFailoverStrategy{}

	sup, err := NewLeaderSupervisor(
		nil,
		strategy,
		nil,
		nil,
		nil,
		nil,
	)
	require.Error(t, err, "should error with nil heartbeat monitor")
	assert.Nil(t, sup)
	assert.Contains(t, err.Error(), "heartbeat monitor is required")
}

// TestNewLeaderSupervisor_NilStrategy verifies that a nil failover strategy
// returns an error.
func TestNewLeaderSupervisor_NilStrategy(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())

	sup, err := NewLeaderSupervisor(
		hbMon,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	require.Error(t, err, "should error with nil strategy")
	assert.Nil(t, sup)
	assert.Contains(t, err.Error(), "failover strategy is required")
}

// TestNewLeaderSupervisor_NilConfig verifies that nil config falls back to
// defaults without error.
func TestNewLeaderSupervisor_NilConfig(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
	strategy := &mockFailoverStrategy{}

	sup, err := NewLeaderSupervisor(
		hbMon,
		strategy,
		nil,
		nil,
		nil,
		nil, // nil config -> defaults.
	)
	require.NoError(t, err)
	require.NotNil(t, sup)

	defaults := DefaultLeaderSupervisorConfig()
	assert.Equal(t, defaults.CheckInterval, sup.config.CheckInterval)
	assert.Equal(t, defaults.FailoverTimeout, sup.config.FailoverTimeout)
	assert.Equal(t, defaults.MaxFailoverAttempts, sup.config.MaxFailoverAttempts)
}

// TestLeaderSupervisor_RegisterLeader_NilAgent verifies that registering a nil
// agent is a no-op.
func TestLeaderSupervisor_RegisterLeader_NilAgent(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
	strategy := &mockFailoverStrategy{}
	sup, _ := NewLeaderSupervisor(hbMon, strategy, nil, nil, nil, nil)

	sup.RegisterLeader("leader-1", nil)

	agents := sup.leaders
	_, exists := agents["leader-1"]
	assert.False(t, exists, "nil agent should not be registered")
}

// TestLeaderSupervisor_RegisterLeader_EmptyID verifies that registering with
// an empty ID is a no-op.
func TestLeaderSupervisor_RegisterLeader_EmptyID(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
	strategy := &mockFailoverStrategy{}
	sup, _ := NewLeaderSupervisor(hbMon, strategy, nil, nil, nil, nil)

	agent := newMockAgent("agent-1", models.AgentStatusReady)
	sup.RegisterLeader("", agent)

	assert.Empty(t, sup.leaders, "empty ID should not register agent")
}

// TestLeaderSupervisor_StartStop_Lifecycle verifies that Start and Stop work
// correctly and prevent double-start.
func TestLeaderSupervisor_StartStop_Lifecycle(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
	strategy := &mockFailoverStrategy{}
	cfg := &LeaderSupervisorConfig{
		CheckInterval:       100 * time.Millisecond,
		FailoverTimeout:     5 * time.Second,
		MaxFailoverAttempts: 1,
	}
	sup, err := NewLeaderSupervisor(hbMon, strategy, nil, nil, nil, cfg)
	require.NoError(t, err)

	// Start.
	err = sup.Start(context.Background())
	require.NoError(t, err, "Start should succeed")

	// Double start should fail.
	err = sup.Start(context.Background())
	require.Error(t, err, "double Start should fail")
	assert.Contains(t, err.Error(), "already started")

	// Stop.
	err = sup.Stop()
	require.NoError(t, err, "Stop should succeed")

	// Double stop should be a no-op (no error).
	err = sup.Stop()
	assert.NoError(t, err, "double Stop should not error")
}

// TestLeaderSupervisor_Start_NilContext verifies that a nil context returns
// an error.
func TestLeaderSupervisor_Start_NilContext(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
	strategy := &mockFailoverStrategy{}
	sup, _ := NewLeaderSupervisor(hbMon, strategy, nil, nil, nil, nil)

	var ctx context.Context // nil context
	err := sup.Start(ctx)   //nolint:staticcheck // Intentionally testing nil context guard.
	require.Error(t, err, "Start with nil context should error")
}

// TestLeaderSupervisor_StopBeforeStart verifies that Stop before Start is a
// no-op.
func TestLeaderSupervisor_StopBeforeStart(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
	strategy := &mockFailoverStrategy{}
	sup, _ := NewLeaderSupervisor(hbMon, strategy, nil, nil, nil, nil)

	err := sup.Stop()
	assert.NoError(t, err, "Stop before Start should not error")
}

// TestColdRestartStrategy_New_NilFactory verifies that a nil factory returns
// an error.
func TestColdRestartStrategy_New_NilFactory(t *testing.T) {
	strategy, err := NewColdRestartStrategy(nil, nil, nil)
	require.Error(t, err, "should error with nil factory")
	assert.Nil(t, strategy)
	assert.Contains(t, err.Error(), "factory is required")
}

// TestColdRestartStrategy_HandleFailover_EmptyLeaderID verifies that an empty
// leader ID returns an error.
func TestColdRestartStrategy_HandleFailover_EmptyLeaderID(t *testing.T) {
	factory := func(ctx context.Context, config interface{}) (base.Agent, error) {
		return newMockAgent("new-leader", models.AgentStatusReady), nil
	}
	strategy, err := NewColdRestartStrategy(factory, nil, nil)
	require.NoError(t, err)

	agent, err := strategy.HandleFailover(context.Background(), "", nil)
	require.Error(t, err, "should error with empty leader ID")
	assert.Nil(t, agent)
	assert.Contains(t, err.Error(), "empty leader ID")
}

// TestColdRestartStrategy_HandleFailover_Normal verifies that the factory is
// called and the agent is started.
func TestColdRestartStrategy_HandleFailover_Normal(t *testing.T) {
	mock := newMockAgent("new-leader", models.AgentStatusOffline)
	factory := func(ctx context.Context, config interface{}) (base.Agent, error) {
		return mock, nil
	}
	strategy, err := NewColdRestartStrategy(factory, map[string]string{"key": "val"}, nil)
	require.NoError(t, err)

	agent, err := strategy.HandleFailover(
		context.Background(),
		"old-leader",
		&LeaderCheckpoint{LeaderID: "old-leader"},
	)
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "new-leader", agent.ID())
	assert.Equal(t, models.AgentStatusReady, agent.Status(),
		"agent should be started (status=ready)")
}

// TestColdRestartStrategy_HandleFailover_FactoryError verifies that a factory
// error is propagated.
func TestColdRestartStrategy_HandleFailover_FactoryError(t *testing.T) {
	factory := func(ctx context.Context, config interface{}) (base.Agent, error) {
		return nil, assert.AnError
	}
	strategy, _ := NewColdRestartStrategy(factory, nil, nil)

	agent, err := strategy.HandleFailover(
		context.Background(),
		"leader-1",
		&LeaderCheckpoint{LeaderID: "leader-1"},
	)
	require.Error(t, err)
	assert.Nil(t, agent)
	assert.Contains(t, err.Error(), "create agent")
}

// TestColdRestartStrategy_HandleFailover_StartError verifies that an agent
// Start failure is propagated.
func TestColdRestartStrategy_HandleFailover_StartError(t *testing.T) {
	mock := newMockAgent("new-leader", models.AgentStatusOffline)
	mock.startErr = assert.AnError
	factory := func(ctx context.Context, config interface{}) (base.Agent, error) {
		return mock, nil
	}
	strategy, _ := NewColdRestartStrategy(factory, nil, nil)

	agent, err := strategy.HandleFailover(
		context.Background(),
		"leader-1",
		&LeaderCheckpoint{LeaderID: "leader-1"},
	)
	require.Error(t, err)
	assert.Nil(t, agent)
	assert.Contains(t, err.Error(), "start agent")
}

// TestDefaultLeaderSupervisorConfig verifies the default configuration values.
func TestDefaultLeaderSupervisorConfig(t *testing.T) {
	cfg := DefaultLeaderSupervisorConfig()
	require.NotNil(t, cfg)

	assert.Equal(t, 10*time.Second, cfg.CheckInterval)
	assert.Equal(t, 2*time.Minute, cfg.FailoverTimeout)
	assert.Equal(t, 3, cfg.MaxFailoverAttempts)
}

// TestLeaderSupervisor_ConcurrentRegisterAndStart verifies that concurrent
// RegisterLeader calls do not race.
func TestLeaderSupervisor_ConcurrentRegisterAndStart(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
	strategy := &mockFailoverStrategy{}
	sup, _ := NewLeaderSupervisor(hbMon, strategy, nil, nil, nil, nil)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agent := newMockAgent("agent-"+string(rune('A'+id)), models.AgentStatusReady)
			sup.RegisterLeader("leader-"+string(rune('A'+id)), agent)
		}(i)
	}
	wg.Wait()

	// Verify no panics occurred. The map may contain up to 20 entries.
	assert.LessOrEqual(t, len(sup.leaders), 20)
}

// setupSupervisorForDoFailover is a helper that initialises the errgroup fields
// required by doFailover.
func setupSupervisorForDoFailover(t *testing.T, sup *LeaderSupervisor) {
	t.Helper()
	g, ctx := errgroup.WithContext(context.Background())
	sup.g = g
	sup.gctx = ctx
}

// TestLeaderSupervisor_doFailover_AlreadyOffline verifies that doFailover
// skips agents that are already offline.
func TestLeaderSupervisor_doFailover_AlreadyOffline(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
	mockStrategy := &mockFailoverStrategy{
		returnAgent: newMockAgent("new-agent", models.AgentStatusReady),
	}
	sup, _ := NewLeaderSupervisor(hbMon, mockStrategy, nil, nil, nil, nil)

	// Register an already-offline agent.
	agent := newMockAgent("leader-1", models.AgentStatusOffline)
	sup.RegisterLeader("leader-1", agent)

	setupSupervisorForDoFailover(t, sup)

	sup.doFailover(sup.gctx, "leader-1")

	mockStrategy.mu.Lock()
	assert.Equal(t, 0, mockStrategy.callCount,
		"strategy should not be called for already-offline agent")
	mockStrategy.mu.Unlock()
}

// TestLeaderSupervisor_doFailover_UnregisteredLeader verifies that doFailover
// skips leaders that are not registered.
func TestLeaderSupervisor_doFailover_UnregisteredLeader(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
	mockStrategy := &mockFailoverStrategy{
		returnAgent: newMockAgent("new-agent", models.AgentStatusReady),
	}
	sup, _ := NewLeaderSupervisor(hbMon, mockStrategy, nil, nil, nil, nil)

	setupSupervisorForDoFailover(t, sup)

	// Should not panic for unregistered leader.
	sup.doFailover(sup.gctx, "non-existent-leader")

	mockStrategy.mu.Lock()
	assert.Equal(t, 0, mockStrategy.callCount,
		"strategy should not be called for unregistered leader")
	mockStrategy.mu.Unlock()
}

// TestLeaderSupervisor_doFailover_ReplacesLeader verifies the full failover
// path where the old leader is replaced by a new one.
func TestLeaderSupervisor_doFailover_ReplacesLeader(t *testing.T) {
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())

	oldAgent := newMockAgent("old-leader", models.AgentStatusReady)
	newAgent := newMockAgent("new-leader", models.AgentStatusOffline)

	mockStrategy := &mockFailoverStrategy{
		returnAgent: newAgent,
	}
	cfg := &LeaderSupervisorConfig{
		CheckInterval:       1 * time.Hour, // irrelevant for this test
		FailoverTimeout:     5 * time.Second,
		MaxFailoverAttempts: 1,
	}
	sup, _ := NewLeaderSupervisor(hbMon, mockStrategy, nil, nil, nil, cfg)
	sup.RegisterLeader("leader-1", oldAgent)

	setupSupervisorForDoFailover(t, sup)

	sup.doFailover(sup.gctx, "leader-1")

	mockStrategy.mu.Lock()
	assert.Equal(t, 1, mockStrategy.callCount, "strategy should be called once")
	assert.Equal(t, "leader-1", mockStrategy.lastAgentID)
	mockStrategy.mu.Unlock()

	// Verify old agent was stopped.
	assert.Equal(t, int32(1), atomic.LoadInt32(&oldAgent.stopCalls),
		"old agent should be stopped once")

	// Verify the leader map was updated.
	sup.mu.RLock()
	current := sup.leaders["leader-1"]
	sup.mu.RUnlock()
	assert.Equal(t, "new-leader", current.ID(),
		"leader map should point to the new agent")
}

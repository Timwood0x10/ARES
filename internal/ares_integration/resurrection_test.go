// package integration provides end-to-end integration tests for the resurrection plugin.
// These tests verify the Supervisor + HeartbeatAdapter + HeartbeatMonitor integration.
package ares_integration

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/plugins/resurrection"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
)

// resMockAgent is a test double for base.Agent used in resurrection tests.
type resMockAgent struct {
	mu         sync.Mutex
	id         string
	agentType  models.AgentType
	status     models.AgentStatus
	startErr   error
	startCalls int32
	stopCalls  int32
}

func newResMockAgent(id string, agentType models.AgentType) *resMockAgent {
	return &resMockAgent{
		id:        id,
		agentType: agentType,
		status:    models.AgentStatusReady,
	}
}

func (m *resMockAgent) ID() string             { return m.id }
func (m *resMockAgent) Type() models.AgentType { return m.agentType }

func (m *resMockAgent) Status() models.AgentStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *resMockAgent) Start(_ context.Context) error {
	atomic.AddInt32(&m.startCalls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.startErr != nil {
		return m.startErr
	}
	m.status = models.AgentStatusReady
	return nil
}

func (m *resMockAgent) Stop(_ context.Context) error {
	atomic.AddInt32(&m.stopCalls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = models.AgentStatusOffline
	return nil
}

func (m *resMockAgent) Process(_ context.Context, _ any) (any, error) {
	return nil, nil
}

func (m *resMockAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent, 1)
	close(ch)
	return ch, nil
}

// createTestSupervisor creates a Supervisor backed by a real HeartbeatMonitor
// and HeartbeatAdapter. Uses a long heartbeat interval to prevent the background
// loop from interfering with manual timeout testing.
func createTestSupervisor(t *testing.T) (*resurrection.Supervisor, *ahp.HeartbeatMonitor) {
	t.Helper()

	monitor := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  50 * time.Millisecond,
		Timeout:   1 * time.Hour, // Long timeout; we trigger manually.
		MaxMissed: 1,
	})
	adapter := resurrection.NewHeartbeatAdapter(monitor)
	require.NotNil(t, adapter)

	supervisor, err := resurrection.New(adapter, resurrection.Config{
		CheckInterval:     100 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 1 * time.Hour, // Long interval to avoid interference.
	}, nil)
	require.NoError(t, err)
	return supervisor, monitor
}

// TestResurrectionAdapterWiring verifies that the HeartbeatAdapter correctly
// bridges the HeartbeatMonitor callbacks to the Supervisor's failure handler.
func TestResurrectionAdapterWiring(t *testing.T) {
	supervisor, monitor := createTestSupervisor(t)

	agent := newResMockAgent("wiring-agent", "worker")
	factory := func() base.Agent { return newResMockAgent("wiring-agent", "worker") }

	supervisor.Watch(agent, factory)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, supervisor.Start(ctx))
	defer func() { _ = supervisor.Stop() }()

	// Verify the agent is registered with the heartbeat monitor.
	agents := monitor.ListAgents()
	assert.Contains(t, agents, "wiring-agent", "agent should be registered with monitor")

	// Verify agent is tracked by supervisor.
	assert.NotNil(t, supervisor.Agent("wiring-agent"))
	stats := supervisor.Stats()
	assert.Equal(t, 1, stats.Watched)
}

// TestResurrectionRegisterAndWatch verifies that Watch registers the agent
// with both the supervisor and the heartbeat monitor.
func TestResurrectionRegisterAndWatch(t *testing.T) {
	supervisor, monitor := createTestSupervisor(t)

	agent := newResMockAgent("watch-agent", "worker")
	factory := func() base.Agent { return newResMockAgent("watch-agent", "worker") }

	supervisor.Watch(agent, factory)

	stats := supervisor.Stats()
	assert.Equal(t, 1, stats.Watched)
	assert.Equal(t, 1, stats.Alive)

	agents := monitor.ListAgents()
	assert.Contains(t, agents, "watch-agent")
}

// TestResurrectionUnwatchRemovesAgent verifies that Unwatch removes the agent
// from both the supervisor and the heartbeat monitor.
func TestResurrectionUnwatchRemovesAgent(t *testing.T) {
	supervisor, monitor := createTestSupervisor(t)

	agent := newResMockAgent("unwatch-agent", "worker")
	factory := func() base.Agent { return newResMockAgent("unwatch-agent", "worker") }

	supervisor.Watch(agent, factory)
	assert.Equal(t, 1, supervisor.Stats().Watched)

	supervisor.Unwatch("unwatch-agent")
	assert.Equal(t, 0, supervisor.Stats().Watched)

	agents := monitor.ListAgents()
	assert.NotContains(t, agents, "unwatch-agent")
}

// TestResurrectionWithRealHeartbeatTimeout verifies the full resurrection flow
// when a heartbeat timeout is detected through the real HeartbeatAdapter.
// Uses a dedicated short-timeout monitor to trigger the timeout.
func TestResurrectionWithRealHeartbeatTimeout(t *testing.T) {
	// Create a monitor with very short timeout for this specific test.
	monitor := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  50 * time.Millisecond,
		Timeout:   50 * time.Millisecond,
		MaxMissed: 1,
	})
	adapter := resurrection.NewHeartbeatAdapter(monitor)
	require.NotNil(t, adapter)

	supervisor, err := resurrection.New(adapter, resurrection.Config{
		CheckInterval:     100 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 1 * time.Hour, // Long interval to avoid interference.
	}, nil)
	require.NoError(t, err)

	agent := newResMockAgent("timeout-agent", "worker")
	var factoryCalled int32
	factory := func() base.Agent {
		atomic.AddInt32(&factoryCalled, 1)
		return newResMockAgent("timeout-agent", "worker")
	}

	supervisor.Watch(agent, factory)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, supervisor.Start(ctx))
	defer func() { _ = supervisor.Stop() }()

	// The agent was registered during Watch() with a fresh heartbeat.
	// Remove it from the monitor to simulate the agent stopping heartbeats.
	// Then re-register and wait for timeout.
	monitor.RemoveAgent("timeout-agent")
	monitor.RecordHeartbeat("timeout-agent")
	time.Sleep(100 * time.Millisecond) // Wait for timeout (50ms) to expire.

	timedOut := monitor.CheckTimeouts()
	require.Contains(t, timedOut, "timeout-agent")

	// Wait for resurrection.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&factoryCalled) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	assert.GreaterOrEqual(t, int(atomic.LoadInt32(&factoryCalled)), 1,
		"factory should have been called")

	time.Sleep(200 * time.Millisecond)
	newAgent := supervisor.Agent("timeout-agent")
	require.NotNil(t, newAgent)
	assert.Equal(t, models.AgentStatusReady, newAgent.Status())

	stats := supervisor.Stats()
	assert.GreaterOrEqual(t, stats.Resurrects, 1)
}

// TestResurrectionFactoryReturnsNil verifies that when the factory returns nil,
// the supervisor retries but eventually fails gracefully.
func TestResurrectionFactoryReturnsNil(t *testing.T) {
	monitor := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  50 * time.Millisecond,
		Timeout:   50 * time.Millisecond,
		MaxMissed: 1,
	})
	adapter := resurrection.NewHeartbeatAdapter(monitor)
	require.NotNil(t, adapter)

	supervisor, err := resurrection.New(adapter, resurrection.Config{
		CheckInterval:     100 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 1 * time.Hour,
	}, nil)
	require.NoError(t, err)

	agent := newResMockAgent("nil-factory-agent", "worker")
	var factoryCalls int32
	factory := func() base.Agent {
		atomic.AddInt32(&factoryCalls, 1)
		return nil
	}

	supervisor.Watch(agent, factory)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, supervisor.Start(ctx))
	defer func() { _ = supervisor.Stop() }()

	// Trigger timeout.
	monitor.RemoveAgent("nil-factory-agent")
	monitor.RecordHeartbeat("nil-factory-agent")
	time.Sleep(100 * time.Millisecond)
	monitor.CheckTimeouts()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&factoryCalls) >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	calls := int(atomic.LoadInt32(&factoryCalls))
	assert.GreaterOrEqual(t, calls, 1, "factory should have been called")
	assert.LessOrEqual(t, calls, 3, "factory should not exceed MaxAttempts")
}

// TestResurrectionMultipleAgentsOneDies verifies that when one agent dies,
// other watched agents continue operating normally.
func TestResurrectionMultipleAgentsOneDies(t *testing.T) {
	monitor := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  50 * time.Millisecond,
		Timeout:   50 * time.Millisecond,
		MaxMissed: 1,
	})
	adapter := resurrection.NewHeartbeatAdapter(monitor)
	require.NotNil(t, adapter)

	supervisor, err := resurrection.New(adapter, resurrection.Config{
		CheckInterval:     100 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 1 * time.Hour,
	}, nil)
	require.NoError(t, err)

	agent1 := newResMockAgent("multi-1", "worker")
	agent2 := newResMockAgent("multi-2", "worker")
	agent3 := newResMockAgent("multi-3", "worker")

	var resurrected int32
	supervisor.Watch(agent1, func() base.Agent {
		atomic.AddInt32(&resurrected, 1)
		return newResMockAgent("multi-1", "worker")
	})
	supervisor.Watch(agent2, func() base.Agent { return agent2 })
	supervisor.Watch(agent3, func() base.Agent { return agent3 })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, supervisor.Start(ctx))
	defer func() { _ = supervisor.Stop() }()

	// Keep agent2 and agent3 alive by refreshing their heartbeats.
	monitor.RecordHeartbeat("multi-2")
	monitor.RecordHeartbeat("multi-3")

	// Trigger timeout for agent1 only.
	monitor.RemoveAgent("multi-1")
	monitor.RecordHeartbeat("multi-1")
	time.Sleep(100 * time.Millisecond)

	// Refresh agent2 and agent3 before they time out.
	monitor.RecordHeartbeat("multi-2")
	monitor.RecordHeartbeat("multi-3")

	monitor.CheckTimeouts()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&resurrected) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, models.AgentStatusReady, agent2.Status())
	assert.Equal(t, models.AgentStatusReady, agent3.Status())

	stats := supervisor.Stats()
	assert.GreaterOrEqual(t, stats.Resurrects, 1)
}

// TestResurrectionStopDuringActiveResurrection verifies that stopping the
// supervisor during an active resurrection does not panic.
func TestResurrectionStopDuringActiveResurrection(t *testing.T) {
	monitor := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  50 * time.Millisecond,
		Timeout:   50 * time.Millisecond,
		MaxMissed: 1,
	})
	adapter := resurrection.NewHeartbeatAdapter(monitor)
	require.NotNil(t, adapter)

	supervisor, err := resurrection.New(adapter, resurrection.Config{
		CheckInterval:     100 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 1 * time.Hour,
	}, nil)
	require.NoError(t, err)

	agent := newResMockAgent("stop-agent", "worker")
	started := make(chan struct{}, 1)
	factory := func() base.Agent {
		select {
		case started <- struct{}{}:
		default:
		}
		time.Sleep(500 * time.Millisecond)
		return newResMockAgent("stop-agent", "worker")
	}

	supervisor.Watch(agent, factory)

	ctx := context.Background()
	require.NoError(t, supervisor.Start(ctx))

	// Trigger timeout.
	monitor.RemoveAgent("stop-agent")
	monitor.RecordHeartbeat("stop-agent")
	time.Sleep(100 * time.Millisecond)
	monitor.CheckTimeouts()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for factory to start")
	}

	stopErr := supervisor.Stop()
	assert.NoError(t, stopErr, "Stop should not return error")
}

// TestResurrectionStatsAfterResurrection verifies that Stats() accurately
// reflects watched, alive, and resurrect counts after a resurrection.
func TestResurrectionStatsAfterResurrection(t *testing.T) {
	monitor := ahp.NewHeartbeatMonitor(&ahp.HeartbeatConfig{
		Interval:  50 * time.Millisecond,
		Timeout:   50 * time.Millisecond,
		MaxMissed: 1,
	})
	adapter := resurrection.NewHeartbeatAdapter(monitor)
	require.NotNil(t, adapter)

	supervisor, err := resurrection.New(adapter, resurrection.Config{
		CheckInterval:     100 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 1 * time.Hour,
	}, nil)
	require.NoError(t, err)

	agent := newResMockAgent("stats-agent", "worker")
	var resurrected int32
	factory := func() base.Agent {
		atomic.AddInt32(&resurrected, 1)
		return newResMockAgent("stats-agent", "worker")
	}

	supervisor.Watch(agent, factory)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, supervisor.Start(ctx))
	defer func() { _ = supervisor.Stop() }()

	stats := supervisor.Stats()
	assert.Equal(t, 1, stats.Watched)
	assert.Equal(t, 1, stats.Alive)
	assert.Equal(t, 0, stats.Resurrects)

	// Trigger timeout.
	monitor.RemoveAgent("stats-agent")
	monitor.RecordHeartbeat("stats-agent")
	time.Sleep(100 * time.Millisecond)
	monitor.CheckTimeouts()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&resurrected) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	stats = supervisor.Stats()
	assert.Equal(t, 1, stats.Watched)
	assert.Equal(t, 1, stats.Alive)
	assert.Equal(t, 1, stats.Resurrects)
}

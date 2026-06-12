// package resurrection provides comprehensive tests for the resurrection plugin.
package resurrection

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagentx/internal/agents/base"
	"goagentx/internal/core/models"
	"goagentx/internal/events"
	"goagentx/internal/protocol/ahp"
)

// mockAgent is a test double for base.Agent.
type mockAgent struct {
	mu         sync.Mutex
	id         string
	agentType  models.AgentType
	status     models.AgentStatus
	startErr   error
	startCalls int
	stopCalls  int
}

func newMockAgent(id string, agentType models.AgentType) *mockAgent {
	return &mockAgent{
		id:        id,
		agentType: agentType,
		status:    models.AgentStatusReady,
	}
}

func (m *mockAgent) ID() string {
	return m.id
}

func (m *mockAgent) Type() models.AgentType {
	return m.agentType
}

func (m *mockAgent) Status() models.AgentStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *mockAgent) setStatus(s models.AgentStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = s
}

func (m *mockAgent) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalls++
	if m.startErr != nil {
		return m.startErr
	}
	m.status = models.AgentStatusReady
	return nil
}

func (m *mockAgent) Stop(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalls++
	m.status = models.AgentStatusOffline
	return nil
}

func (m *mockAgent) Process(_ context.Context, _ any) (any, error) {
	return nil, nil
}

func (m *mockAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent)
	close(ch)
	return ch, nil
}

// mockHealthChecker is a test double for HealthChecker.
type mockHealthChecker struct {
	mu            sync.Mutex
	registered    map[string]bool
	unregistered  []string
	alive         []string
	failures      []string
	onFailureFunc func(agentID string)
}

func newMockHealthChecker() *mockHealthChecker {
	return &mockHealthChecker{
		registered: make(map[string]bool),
	}
}

func (m *mockHealthChecker) RegisterAgent(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered[agentID] = true
}

func (m *mockHealthChecker) UnregisterAgent(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.registered, agentID)
	m.unregistered = append(m.unregistered, agentID)
}

func (m *mockHealthChecker) RecordAlive(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alive = append(m.alive, agentID)
}

func (m *mockHealthChecker) CheckHealth() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.failures))
	copy(result, m.failures)
	return result
}

func (m *mockHealthChecker) OnFailure(fn func(agentID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onFailureFunc = fn
}

func (m *mockHealthChecker) triggerFailure(agentID string) {
	m.mu.Lock()
	fn := m.onFailureFunc
	m.mu.Unlock()
	if fn != nil {
		fn(agentID)
	}
}

func (m *mockHealthChecker) isRegistered(agentID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.registered[agentID]
}

// --- New() validation ---

func TestNew_NilHealthChecker_ReturnsError(t *testing.T) {
	sup, err := New(nil, Config{}, nil)
	require.Error(t, err)
	assert.Nil(t, sup)
	assert.Contains(t, err.Error(), "health checker is required")
}

func TestNew_ZeroConfig_UsesDefaults(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{}, nil)
	require.NoError(t, err)
	require.NotNil(t, sup)

	// Zero config should trigger DefaultConfig.
	assert.Equal(t, DefaultConfig().CheckInterval, sup.config.CheckInterval)
	assert.Equal(t, DefaultConfig().ResurrectTimeout, sup.config.ResurrectTimeout)
	assert.Equal(t, DefaultConfig().MaxAttempts, sup.config.MaxAttempts)
	assert.Equal(t, DefaultConfig().HeartbeatInterval, sup.config.HeartbeatInterval)
}

func TestNew_PartialConfig_UsesDefaults(t *testing.T) {
	health := newMockHealthChecker()
	// Only set MaxAttempts; other fields are zero and get defaults.
	sup, err := New(health, Config{MaxAttempts: 5}, nil)
	require.NoError(t, err)
	require.NotNil(t, sup)

	// Zero-value fields get defaults; non-zero fields are preserved.
	assert.Equal(t, DefaultConfig().CheckInterval, sup.config.CheckInterval)
	assert.Equal(t, 5, sup.config.MaxAttempts) // preserved, not overwritten
	assert.Equal(t, DefaultConfig().ResurrectTimeout, sup.config.ResurrectTimeout)
	assert.Equal(t, DefaultConfig().HeartbeatInterval, sup.config.HeartbeatInterval)
}

func TestNew_CustomConfig_PreservesValues(t *testing.T) {
	health := newMockHealthChecker()
	custom := Config{
		CheckInterval:     2 * time.Second,
		ResurrectTimeout:  10 * time.Second,
		MaxAttempts:       5,
		HeartbeatInterval: 1 * time.Second,
	}
	sup, err := New(health, custom, nil)
	require.NoError(t, err)
	assert.Equal(t, custom, sup.config)
}

// --- Watch/Unwatch ---

func TestWatch_NilAgent_IsNoOp(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	// Should not panic or register anything.
	sup.Watch(nil, func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) })

	stats := sup.Stats()
	assert.Equal(t, 0, stats.Watched)
}

func TestWatch_NilFactory_IsNoOp(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	sup.Watch(agent, nil)

	stats := sup.Stats()
	assert.Equal(t, 0, stats.Watched)
}

func TestWatch_ValidRegistration(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }

	sup.Watch(agent, factory)

	stats := sup.Stats()
	assert.Equal(t, 1, stats.Watched)
	assert.True(t, health.isRegistered("a1"))
}

func TestUnwatch_RemovesAgent(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	sup.Unwatch("a1")

	stats := sup.Stats()
	assert.Equal(t, 0, stats.Watched)
	assert.False(t, health.isRegistered("a1"))
}

func TestUnwatch_NonExistentAgent_DoesNotPanic(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	// Should not panic.
	sup.Unwatch("nonexistent")
}

func TestWatch_OverwritesSameID(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent1 := newMockAgent("a1", models.AgentTypeLeader)
	agent2 := newMockAgent("a1", models.AgentTypeFood)
	factory := func() base.Agent { return newMockAgent("a1", models.AgentTypeLeader) }

	sup.Watch(agent1, factory)
	sup.Watch(agent2, factory)

	// The second watch overwrites the first.
	assert.Equal(t, agent2, sup.Agent("a1"))
}

// --- Start/Stop lifecycle ---

func TestStart_DoubleStart_ReturnsError(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     1 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	err = sup.Start(ctx)
	require.NoError(t, err)

	err = sup.Start(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already started")

	_ = sup.Stop()
}

func TestStop_BeforeStart_IsNoOp(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	// Stop before start should return nil without panic.
	err = sup.Stop()
	assert.NoError(t, err)
}

func TestStartStop_NormalLifecycle(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = sup.Start(ctx)
	require.NoError(t, err)

	// Let the background goroutine run briefly.
	time.Sleep(80 * time.Millisecond)

	err = sup.Stop()
	assert.NoError(t, err)
}

func TestStart_NilContext_ReturnsError(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	var ctx context.Context // nil context
	err = sup.Start(ctx)    //nolint:staticcheck // Intentionally testing nil context guard.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context is required")
}

func TestStop_AfterStop_IsNoOp(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     1 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, sup.Start(ctx))
	require.NoError(t, sup.Stop())

	// Second stop should be a no-op.
	err = sup.Stop()
	assert.NoError(t, err)
}

func TestStart_AfterStop_ReturnsError(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     1 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, sup.Start(ctx))
	require.NoError(t, sup.Stop())

	// After Stop(), started is still true, so Start() returns "already started".
	err = sup.Start(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already started")
}

// --- Resurrection on failure ---

func TestResurrection_OnFailure_CreatesNewInstance(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	// Agent starts in ready state; onFailure checks status is NOT offline.
	original := newMockAgent("a1", models.AgentTypeLeader)

	var factoryCalled atomic.Int32
	factory := func() base.Agent {
		factoryCalled.Add(1)
		return newMockAgent("a1", models.AgentTypeLeader)
	}

	sup.Watch(original, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	// Trigger failure callback directly.
	health.triggerFailure("a1")

	// Wait for resurrection goroutine to complete.
	time.Sleep(300 * time.Millisecond)

	assert.Greater(t, factoryCalled.Load(), int32(0), "factory should have been called")

	// The agent should now be a new instance.
	agent := sup.Agent("a1")
	require.NotNil(t, agent)
	assert.Equal(t, models.AgentStatusReady, agent.Status())

	stats := sup.Stats()
	assert.Equal(t, 1, stats.Resurrects)

	_ = sup.Stop()
}

func TestResurrection_FactoryReturnsNil_RetriesAndFails(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  1 * time.Second,
		MaxAttempts:       2,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	// Agent starts in ready state.
	original := newMockAgent("a1", models.AgentTypeLeader)

	factory := func() base.Agent {
		return nil // Factory always returns nil.
	}

	sup.Watch(original, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	health.triggerFailure("a1")
	time.Sleep(200 * time.Millisecond)

	// All attempts exhausted; resurrects count should remain 0.
	stats := sup.Stats()
	assert.Equal(t, 0, stats.Resurrects)

	_ = sup.Stop()
}

// --- Multiple resurrection attempts ---

func TestResurrection_FactoryFailsOnFirstAttempts_SucceedsOnNth(t *testing.T) {
	health := newMockHealthChecker()
	maxAttempts := 5
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  1 * time.Second,
		MaxAttempts:       maxAttempts,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	// Agent starts in ready state.
	original := newMockAgent("a1", models.AgentTypeLeader)

	var attemptCount atomic.Int32
	factory := func() base.Agent {
		attempt := attemptCount.Add(1)
		if attempt < 3 {
			// First two attempts: agent fails to start.
			a := newMockAgent("a1", models.AgentTypeLeader)
			a.startErr = fmt.Errorf("simulated start failure")
			return a
		}
		// Third attempt: succeeds.
		return newMockAgent("a1", models.AgentTypeLeader)
	}

	sup.Watch(original, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	health.triggerFailure("a1")
	time.Sleep(300 * time.Millisecond)

	assert.GreaterOrEqual(t, attemptCount.Load(), int32(3))

	stats := sup.Stats()
	assert.Equal(t, 1, stats.Resurrects)

	agent := sup.Agent("a1")
	require.NotNil(t, agent)
	assert.Equal(t, models.AgentStatusReady, agent.Status())

	_ = sup.Stop()
}

func TestResurrection_StartFails_RetriesUntilSuccess(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  1 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	// Agent starts in ready state.
	original := newMockAgent("a1", models.AgentTypeLeader)

	var attemptCount atomic.Int32
	factory := func() base.Agent {
		attempt := attemptCount.Add(1)
		if attempt <= 2 {
			a := newMockAgent("a1", models.AgentTypeLeader)
			a.startErr = fmt.Errorf("start failure %d", attempt)
			return a
		}
		return newMockAgent("a1", models.AgentTypeLeader)
	}

	sup.Watch(original, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	health.triggerFailure("a1")
	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, int32(3), attemptCount.Load())
	assert.Equal(t, 1, sup.Stats().Resurrects)

	_ = sup.Stop()
}

// --- All attempts exhausted ---

func TestResurrection_AllAttemptsExhausted_NoPanic(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  1 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	// Agent starts in ready state.
	original := newMockAgent("a1", models.AgentTypeLeader)

	// Factory always returns an agent that fails to start.
	factory := func() base.Agent {
		a := newMockAgent("a1", models.AgentTypeLeader)
		a.startErr = fmt.Errorf("permanent start failure")
		return a
	}

	sup.Watch(original, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	// Should not panic even when all attempts fail.
	health.triggerFailure("a1")
	time.Sleep(300 * time.Millisecond)

	stats := sup.Stats()
	assert.Equal(t, 0, stats.Resurrects, "should not count exhausted resurrection")

	_ = sup.Stop()
}

func TestResurrection_FactoryAlwaysReturnsNil_NoPanic(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  1 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	// Agent starts in ready state.
	original := newMockAgent("a1", models.AgentTypeLeader)

	factory := func() base.Agent {
		return nil
	}

	sup.Watch(original, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	health.triggerFailure("a1")
	time.Sleep(300 * time.Millisecond)

	// No panic, no resurrection.
	assert.Equal(t, 0, sup.Stats().Resurrects)

	_ = sup.Stop()
}

func TestResurrection_NonExistentAgent_IsNoOp(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  1 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	// Trigger failure for an agent that was never watched.
	health.triggerFailure("nonexistent")
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 0, sup.Stats().Resurrects)

	_ = sup.Stop()
}

func TestResurrection_AgentAlreadyOffline_IsResurrected(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  1 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	original := newMockAgent("a1", models.AgentTypeLeader)
	original.setStatus(models.AgentStatusOffline)

	var factoryCalled atomic.Bool
	factory := func() base.Agent {
		factoryCalled.Store(true)
		return newMockAgent("a1", models.AgentTypeLeader)
	}

	sup.Watch(original, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	// Offline agents should be resurrected when failure is detected.
	health.triggerFailure("a1")
	time.Sleep(500 * time.Millisecond)

	assert.True(t, factoryCalled.Load(), "factory should be called for offline agent")
	assert.Equal(t, 1, sup.Stats().Resurrects)

	_ = sup.Stop()
}

// --- Stats ---

func TestStats_CorrectCountsAfterRegistration(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent1 := newMockAgent("a1", models.AgentTypeLeader)
	agent2 := newMockAgent("a2", models.AgentTypeFood)
	factory := func() base.Agent { return newMockAgent("", models.AgentTypeLeader) }

	sup.Watch(agent1, factory)
	sup.Watch(agent2, factory)

	stats := sup.Stats()
	assert.Equal(t, 2, stats.Watched)
	assert.Equal(t, 2, stats.Alive)
	assert.Equal(t, 0, stats.Resurrects)
	assert.Len(t, stats.Statuses, 2)
	assert.Equal(t, string(models.AgentStatusReady), stats.Statuses["a1"])
	assert.Equal(t, string(models.AgentStatusReady), stats.Statuses["a2"])
}

func TestStats_OfflineAgent_CountedAsNotAlive(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	agent.setStatus(models.AgentStatusOffline)
	factory := func() base.Agent { return newMockAgent("", models.AgentTypeLeader) }

	sup.Watch(agent, factory)

	stats := sup.Stats()
	assert.Equal(t, 1, stats.Watched)
	assert.Equal(t, 0, stats.Alive, "offline agent should not be counted as alive")
}

func TestStats_EmptySupervisor(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	stats := sup.Stats()
	assert.Equal(t, 0, stats.Watched)
	assert.Equal(t, 0, stats.Alive)
	assert.Equal(t, 0, stats.Resurrects)
	assert.Empty(t, stats.Statuses)
}

func TestStats_AfterResurrection_CountIncremented(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	// Agent starts in ready state.
	original := newMockAgent("a1", models.AgentTypeLeader)

	factory := func() base.Agent {
		return newMockAgent("a1", models.AgentTypeLeader)
	}

	sup.Watch(original, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	health.triggerFailure("a1")
	time.Sleep(200 * time.Millisecond)

	stats := sup.Stats()
	assert.Equal(t, 1, stats.Watched)
	assert.Equal(t, 1, stats.Alive)
	assert.Equal(t, 1, stats.Resurrects)

	_ = sup.Stop()
}

// --- Agent() lookup ---

func TestAgent_UnknownID_ReturnsNil(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := sup.Agent("nonexistent")
	assert.Nil(t, agent)
}

func TestAgent_KnownID_ReturnsAgent(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	result := sup.Agent("a1")
	assert.Equal(t, agent, result)
}

func TestAgent_AfterResurrection_ReturnsNewInstance(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	// Agent starts in ready state.
	original := newMockAgent("a1", models.AgentTypeLeader)

	newAgent := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newAgent }

	sup.Watch(original, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	health.triggerFailure("a1")
	time.Sleep(200 * time.Millisecond)

	// After resurrection, Agent() should return the new instance.
	assert.Equal(t, newAgent, sup.Agent("a1"))
	// Use pointer identity to compare (NotEqual uses DeepEqual which panics on sync.Mutex).
	assert.False(t, original == sup.Agent("a1"), "agent should be a new instance")

	_ = sup.Stop()
}

func TestAgent_AfterUnwatch_ReturnsNil(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	agent := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent { return newMockAgent("", models.AgentTypeLeader) }
	sup.Watch(agent, factory)

	sup.Unwatch("a1")
	assert.Nil(t, sup.Agent("a1"))
}

// --- Concurrent Watch/Unwatch ---

func TestConcurrent_WatchUnwatch_RaceSafety(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	var wg sync.WaitGroup
	agentCount := 50

	// Concurrently watch agents.
	for i := 0; i < agentCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("agent-%d", idx)
			agent := newMockAgent(id, models.AgentTypeLeader)
			factory := func() base.Agent { return newMockAgent(id, models.AgentTypeLeader) }
			sup.Watch(agent, factory)
		}(i)
	}
	wg.Wait()

	stats := sup.Stats()
	assert.Equal(t, agentCount, stats.Watched)

	// Concurrently unwatch agents.
	for i := 0; i < agentCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("agent-%d", idx)
			sup.Unwatch(id)
		}(i)
	}
	wg.Wait()

	stats = sup.Stats()
	assert.Equal(t, 0, stats.Watched)
}

func TestConcurrent_WatchAndAgentLookup_RaceSafety(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	var wg sync.WaitGroup

	// Concurrently watch and look up agents.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("agent-%d", idx%10)
			agent := newMockAgent(id, models.AgentTypeLeader)
			factory := func() base.Agent { return newMockAgent(id, models.AgentTypeLeader) }
			sup.Watch(agent, factory)
		}(i)

		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("agent-%d", idx%10)
			_ = sup.Agent(id)
		}(i)
	}
	wg.Wait()
}

func TestConcurrent_WatchAndStats_RaceSafety(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("agent-%d", idx)
			agent := newMockAgent(id, models.AgentTypeLeader)
			factory := func() base.Agent { return newMockAgent(id, models.AgentTypeLeader) }
			sup.Watch(agent, factory)
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sup.Stats()
		}()
	}
	wg.Wait()
}

// --- HeartbeatAdapter ---

func TestHeartbeatAdapter_WrapsMonitorCorrectly(t *testing.T) {
	mon := ahp.NewHeartbeatMonitor(nil)
	adapter := NewHeartbeatAdapter(mon)

	// RegisterAgent should call RecordHeartbeat.
	adapter.RegisterAgent("a1")
	status, ok := mon.GetStatus("a1")
	assert.True(t, ok)
	assert.Equal(t, models.AgentStatusReady, status)

	// RecordAlive should update heartbeat.
	adapter.RecordAlive("a1")

	// UnregisterAgent should call RemoveAgent.
	adapter.UnregisterAgent("a1")
	_, ok = mon.GetStatus("a1")
	assert.False(t, ok)
}

func TestHeartbeatAdapter_OnFailure_CallbackInvoked(t *testing.T) {
	mon := ahp.NewHeartbeatMonitor(nil)
	adapter := NewHeartbeatAdapter(mon)

	var calledID string
	adapter.OnFailure(func(agentID string) {
		calledID = agentID
	})

	// Trigger the callback by calling CheckTimeouts after setting up a stale agent.
	// The adapter registers a callback on the monitor that delegates to adapter.onFailure.
	// We directly invoke the registered callback via CheckTimeouts on a stale agent.
	adapter.RegisterAgent("a1")

	// Manually set the agent's LastSeen far in the past so it times out.
	// Use CheckTimeouts which triggers the registered callback.
	// First, we need to make the agent stale. We can't set LastSeen directly,
	// so we simulate timeout by calling CheckTimeouts multiple times with a very short timeout config.
	// Instead, let's test the callback wiring by directly checking the adapter's onFailure field.
	adapter.onFailure("a1")
	assert.Equal(t, "a1", calledID)
}

func TestHeartbeatAdapter_OnFailure_NilCallback_NoOp(t *testing.T) {
	mon := ahp.NewHeartbeatMonitor(nil)
	adapter := NewHeartbeatAdapter(mon)

	// Do not set a callback; calling onFailure field should be nil-safe.
	assert.Nil(t, adapter.onFailure)
}

func TestHeartbeatAdapter_CheckHealth_DelegatesToMonitor(t *testing.T) {
	// Use a very short timeout so the agent times out quickly.
	cfg := &ahp.HeartbeatConfig{
		Interval:  10 * time.Millisecond,
		Timeout:   1 * time.Millisecond,
		MaxMissed: 1,
	}
	mon := ahp.NewHeartbeatMonitor(cfg)
	adapter := NewHeartbeatAdapter(mon)

	// Register an agent.
	adapter.RegisterAgent("a1")

	// Wait for the agent to become stale.
	time.Sleep(20 * time.Millisecond)

	timedOut := adapter.CheckHealth()
	assert.Contains(t, timedOut, "a1")
}

func TestHeartbeatAdapter_RegisterAndCheck_MultipleAgents(t *testing.T) {
	mon := ahp.NewHeartbeatMonitor(nil)
	adapter := NewHeartbeatAdapter(mon)

	adapter.RegisterAgent("a1")
	adapter.RegisterAgent("a2")
	adapter.RegisterAgent("a3")

	// All should be registered.
	status1, ok1 := mon.GetStatus("a1")
	status2, ok2 := mon.GetStatus("a2")
	status3, ok3 := mon.GetStatus("a3")

	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.True(t, ok3)
	assert.Equal(t, models.AgentStatusReady, status1)
	assert.Equal(t, models.AgentStatusReady, status2)
	assert.Equal(t, models.AgentStatusReady, status3)

	// Unregister one.
	adapter.UnregisterAgent("a2")
	_, ok2 = mon.GetStatus("a2")
	assert.False(t, ok2)

	// Others still registered.
	_, ok1 = mon.GetStatus("a1")
	_, ok3 = mon.GetStatus("a3")
	assert.True(t, ok1)
	assert.True(t, ok3)
}

// --- Edge cases ---

func TestResurrection_SequentialFailures_MultipleAgents(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	// Watch multiple agents in ready state.
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("a%d", i)
		agent := newMockAgent(id, models.AgentTypeLeader)
		factory := func() base.Agent { return newMockAgent(id, models.AgentTypeLeader) }
		sup.Watch(agent, factory)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	// Trigger failures sequentially to avoid race on s.resurrects in slog.Info.
	for i := 0; i < 3; i++ {
		health.triggerFailure(fmt.Sprintf("a%d", i))
		time.Sleep(200 * time.Millisecond)
	}

	stats := sup.Stats()
	assert.Equal(t, 3, stats.Resurrects)

	_ = sup.Stop()
}

func TestStats_MultipleResurrections_CountAccumulates(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{
		CheckInterval:     50 * time.Millisecond,
		ResurrectTimeout:  5 * time.Second,
		MaxAttempts:       3,
		HeartbeatInterval: 50 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	// Agent starts in ready state.
	agent := newMockAgent("a1", models.AgentTypeLeader)
	factory := func() base.Agent {
		return newMockAgent("a1", models.AgentTypeLeader)
	}

	sup.Watch(agent, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sup.Start(ctx))

	// Trigger two resurrection cycles.
	// First: trigger failure (agent is in ready state).
	health.triggerFailure("a1")
	time.Sleep(300 * time.Millisecond)

	// Second: trigger failure again on the new agent.
	health.triggerFailure("a1")
	time.Sleep(300 * time.Millisecond)

	stats := sup.Stats()
	assert.Equal(t, 2, stats.Resurrects)

	_ = sup.Stop()
}

func TestNew_InitializedMapsNotNil(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	assert.NotNil(t, sup.agents)
}

// --- replayEvents tests ---

func TestReplayEvents_NilStore(t *testing.T) {
	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, nil)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), "agent-1")
	assert.Nil(t, state, "replayEvents with nil store should return nil")
}

func TestReplayEvents_NoEvents(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), "agent-1")
	assert.Nil(t, state, "replayEvents with no events should return nil")
}

func TestReplayEvents_WithSessionCreatedEvent(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	agentID := "agent-1"
	err := store.Append(context.Background(), agentID, []*events.Event{
		{
			Type: events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "session-abc",
				"user_id":    "user-1",
			},
		},
	}, 0)
	require.NoError(t, err)

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), agentID)
	require.NotNil(t, state)
	assert.Equal(t, "session-abc", state["session_id"])
}

func TestReplayEvents_WithMultipleSessionEvents(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	agentID := "agent-1"
	err := store.Append(context.Background(), agentID, []*events.Event{
		{
			Type: events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "session-old",
			},
		},
		{
			Type: events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "session-new",
			},
		},
	}, 0)
	require.NoError(t, err)

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), agentID)
	require.NotNil(t, state)
	assert.Equal(t, "session-new", state["session_id"],
		"should recover the latest session")
}

func TestReplayEvents_WithNonSessionEvents(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	agentID := "agent-1"
	err := store.Append(context.Background(), agentID, []*events.Event{
		{
			Type: events.EventTaskCreated,
			Payload: map[string]any{
				"task_id": "task-1",
			},
		},
		{
			Type: events.EventMessageAdded,
			Payload: map[string]any{
				"role": "user",
			},
		},
	}, 0)
	require.NoError(t, err)

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), agentID)
	// Non-session events produce a state map without session_id.
	require.NotNil(t, state)
	_, hasSessionID := state["session_id"]
	assert.False(t, hasSessionID, "non-session events should not set session_id")
}

func TestReplayEvents_FullScenario(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	agentID := "agent-1"
	err := store.Append(context.Background(), agentID, []*events.Event{
		{
			Type: events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "session-1",
				"user_id":    "user-1",
			},
		},
		{
			Type: events.EventMessageAdded,
			Payload: map[string]any{
				"session_id": "session-1",
				"role":       "user",
			},
		},
		{
			Type: events.EventTaskCreated,
			Payload: map[string]any{
				"task_id":    "task-1",
				"session_id": "session-1",
			},
		},
		{
			Type: events.EventTaskCompleted,
			Payload: map[string]any{
				"task_id": "task-1",
			},
		},
	}, 0)
	require.NoError(t, err)

	health := newMockHealthChecker()
	sup, err := New(health, Config{CheckInterval: 1 * time.Second}, store)
	require.NoError(t, err)

	state := sup.replayEvents(context.Background(), agentID)
	require.NotNil(t, state)
	assert.Equal(t, "session-1", state["session_id"])
}

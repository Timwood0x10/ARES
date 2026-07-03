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

func TestManager_Stats_AfterMultipleOperations(t *testing.T) {
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
func TestManager_GetAgent_AfterRestore(t *testing.T) {

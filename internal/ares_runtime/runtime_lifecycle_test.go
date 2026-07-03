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

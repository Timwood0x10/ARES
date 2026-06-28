package adapter

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/internal/monitoring/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRuntimeManager implements RuntimeManager for testing.
type mockRuntimeManager struct {
	mu           sync.Mutex
	deadCalls    []deadCall
	restartCalls []string
	agents       map[string]*AgentInfo
	restartErr   error
}

type deadCall struct {
	agentID string
	reason  string
}

func newMockManager() *mockRuntimeManager {
	return &mockRuntimeManager{
		agents: make(map[string]*AgentInfo),
	}
}

func (m *mockRuntimeManager) NotifyAgentDead(agentID, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deadCalls = append(m.deadCalls, deadCall{agentID: agentID, reason: reason})
}

func (m *mockRuntimeManager) RestartAgent(_ context.Context, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restartCalls = append(m.restartCalls, agentID)
	return m.restartErr
}

func (m *mockRuntimeManager) GetAgentInfo(agentID string) (*AgentInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.agents[agentID]
	return info, ok
}

func TestNewRuntimeAdapter(t *testing.T) {
	t.Run("nil manager", func(t *testing.T) {
		a := NewRuntimeAdapter(nil)
		assert.Nil(t, a)
	})

	t.Run("valid manager", func(t *testing.T) {
		mgr := newMockManager()
		a := NewRuntimeAdapter(mgr)
		require.NotNil(t, a)
	})
}

func TestRuntimeAdapter_ImplementsInterface(t *testing.T) {
	mgr := newMockManager()
	a := NewRuntimeAdapter(mgr)

	// Verify that RuntimeAdapter satisfies dag.RuntimeController.
	var ctrl dag.RuntimeController = a
	assert.NotNil(t, ctrl)
}

func TestRuntimeAdapter_NotifyAgentDead(t *testing.T) {
	mgr := newMockManager()
	a := NewRuntimeAdapter(mgr)

	a.NotifyAgentDead("agent-1", "crash")

	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	require.Len(t, mgr.deadCalls, 1)
	assert.Equal(t, "agent-1", mgr.deadCalls[0].agentID)
	assert.Equal(t, "crash", mgr.deadCalls[0].reason)
}

func TestRuntimeAdapter_GetAgentInfo(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		mgr := newMockManager()
		mgr.agents["a1"] = &AgentInfo{
			ID:       "a1",
			Type:     "worker",
			Status:   "running",
			Restarts: 2,
		}
		a := NewRuntimeAdapter(mgr)

		info, ok := a.GetAgentInfo("a1")
		require.True(t, ok)
		require.NotNil(t, info)
		assert.Equal(t, "a1", info.ID)
		assert.Equal(t, "worker", info.Name)
		assert.Equal(t, "running", info.Status)
		assert.Equal(t, "runtime", info.Source)
	})

	t.Run("not found", func(t *testing.T) {
		mgr := newMockManager()
		a := NewRuntimeAdapter(mgr)

		info, ok := a.GetAgentInfo("missing")
		assert.False(t, ok)
		assert.Nil(t, info)
	})
}

func TestRuntimeAdapter_RestartAgent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mgr := newMockManager()
		a := NewRuntimeAdapter(mgr)

		err := a.RestartAgent(context.Background(), "a1")
		require.NoError(t, err)

		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		require.Len(t, mgr.restartCalls, 1)
		assert.Equal(t, "a1", mgr.restartCalls[0])
	})

	t.Run("error", func(t *testing.T) {
		mgr := newMockManager()
		mgr.restartErr = errors.New("restart timeout")
		a := NewRuntimeAdapter(mgr)

		err := a.RestartAgent(context.Background(), "a1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "restart timeout")
	})
}

func TestRuntimeAdapter_ConcurrentAccess(t *testing.T) {
	mgr := newMockManager()
	mgr.agents["a1"] = &AgentInfo{
		ID:     "a1",
		Type:   "worker",
		Status: "running",
	}
	a := NewRuntimeAdapter(mgr)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			a.NotifyAgentDead("a1", "concurrent")
		}()
		go func() {
			defer wg.Done()
			a.GetAgentInfo("a1")
		}()
	}
	wg.Wait()
	// No panic or race condition means success.
}

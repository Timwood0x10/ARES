package arena

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagentx/internal/runtime"
)

// mockRuntime implements RuntimeProvider for testing.
type mockRuntime struct {
	mu           sync.Mutex
	stopAgentFn  func(ctx context.Context, agentID string) error
	listAgentsFn func() []runtime.AgentInfo
	stopped      []string
}

func (m *mockRuntime) StopAgent(ctx context.Context, agentID string) error {
	m.mu.Lock()
	m.stopped = append(m.stopped, agentID)
	m.mu.Unlock()
	if m.stopAgentFn != nil {
		return m.stopAgentFn(ctx, agentID)
	}
	return nil
}

func (m *mockRuntime) getStopped() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.stopped))
	copy(out, m.stopped)
	return out
}

func (m *mockRuntime) ListAgents() []runtime.AgentInfo {
	if m.listAgentsFn != nil {
		return m.listAgentsFn()
	}
	return nil
}

// mockDAG implements DAGProvider for testing.
type mockDAG struct {
	mu           sync.Mutex
	removeNodeFn func(ctx context.Context, id string) error
	removeEdgeFn func(ctx context.Context, from, to string) error
	removedNodes []string
	removedEdges [][2]string
}

func (m *mockDAG) RemoveNode(ctx context.Context, id string) error {
	m.mu.Lock()
	m.removedNodes = append(m.removedNodes, id)
	m.mu.Unlock()
	if m.removeNodeFn != nil {
		return m.removeNodeFn(ctx, id)
	}
	return nil
}

func (m *mockDAG) RemoveEdge(ctx context.Context, from, to string) error {
	m.mu.Lock()
	m.removedEdges = append(m.removedEdges, [2]string{from, to})
	m.mu.Unlock()
	if m.removeEdgeFn != nil {
		return m.removeEdgeFn(ctx, from, to)
	}
	return nil
}

func (m *mockDAG) getRemovedNodes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.removedNodes))
	copy(out, m.removedNodes)
	return out
}

func (m *mockDAG) getRemovedEdges() [][2]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][2]string, len(m.removedEdges))
	copy(out, m.removedEdges)
	return out
}

func TestKillAgent_Success(t *testing.T) {
	rt := &mockRuntime{}
	inj := NewInjector(rt, nil)

	err := inj.KillAgent(context.Background(), "agent-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"agent-1"}, rt.getStopped())
}

func TestKillAgent_NilRuntime(t *testing.T) {
	inj := NewInjector(nil, nil)

	err := inj.KillAgent(context.Background(), "agent-1")
	assert.ErrorIs(t, err, ErrRuntimeNil)
}

func TestKillAgent_RuntimeError(t *testing.T) {
	rt := &mockRuntime{
		stopAgentFn: func(_ context.Context, _ string) error {
			return errors.New("stop failed")
		},
	}
	inj := NewInjector(rt, nil)

	err := inj.KillAgent(context.Background(), "agent-1")
	assert.ErrorContains(t, err, "stop failed")
}

func TestKillLeader_Success(t *testing.T) {
	rt := &mockRuntime{
		listAgentsFn: func() []runtime.AgentInfo {
			return []runtime.AgentInfo{
				{ID: "worker-1", Type: "sub"},
				{ID: "leader-1", Type: "leader"},
				{ID: "worker-2", Type: "sub"},
			}
		},
	}
	inj := NewInjector(rt, nil)

	leaderID, err := inj.KillLeader(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "leader-1", leaderID)
	assert.Equal(t, []string{"leader-1"}, rt.getStopped())
}

func TestKillLeader_NotFound(t *testing.T) {
	rt := &mockRuntime{
		listAgentsFn: func() []runtime.AgentInfo {
			return []runtime.AgentInfo{
				{ID: "worker-1", Type: "sub"},
			}
		},
	}
	inj := NewInjector(rt, nil)

	_, err := inj.KillLeader(context.Background())
	assert.ErrorIs(t, err, ErrLeaderNotFound)
}

func TestKillLeader_EmptyList(t *testing.T) {
	rt := &mockRuntime{
		listAgentsFn: func() []runtime.AgentInfo {
			return nil
		},
	}
	inj := NewInjector(rt, nil)

	_, err := inj.KillLeader(context.Background())
	assert.ErrorIs(t, err, ErrLeaderNotFound)
}

func TestKillLeader_NilRuntime(t *testing.T) {
	inj := NewInjector(nil, nil)

	_, err := inj.KillLeader(context.Background())
	assert.ErrorIs(t, err, ErrRuntimeNil)
}

func TestRemoveNode_Success(t *testing.T) {
	dag := &mockDAG{}
	inj := NewInjector(nil, dag)

	err := inj.RemoveNode(context.Background(), "node-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"node-1"}, dag.getRemovedNodes())
}

func TestRemoveNode_NilDAG(t *testing.T) {
	inj := NewInjector(nil, nil)

	err := inj.RemoveNode(context.Background(), "node-1")
	assert.ErrorIs(t, err, ErrDAGNil)
}

func TestRemoveNode_DAGError(t *testing.T) {
	dag := &mockDAG{
		removeNodeFn: func(_ context.Context, _ string) error {
			return errors.New("node has dependents")
		},
	}
	inj := NewInjector(nil, dag)

	err := inj.RemoveNode(context.Background(), "node-1")
	assert.ErrorContains(t, err, "node has dependents")
}

func TestRemoveEdge_Success(t *testing.T) {
	dag := &mockDAG{}
	inj := NewInjector(nil, dag)

	err := inj.RemoveEdge(context.Background(), "a", "b")
	require.NoError(t, err)
	edges := dag.getRemovedEdges()
	require.Len(t, edges, 1)
	assert.Equal(t, [2]string{"a", "b"}, edges[0])
}

func TestRemoveEdge_NilDAG(t *testing.T) {
	inj := NewInjector(nil, nil)

	err := inj.RemoveEdge(context.Background(), "a", "b")
	assert.ErrorIs(t, err, ErrDAGNil)
}

func TestRemoveEdge_DAGError(t *testing.T) {
	dag := &mockDAG{
		removeEdgeFn: func(_ context.Context, _, _ string) error {
			return errors.New("edge not found")
		},
	}
	inj := NewInjector(nil, dag)

	err := inj.RemoveEdge(context.Background(), "a", "b")
	assert.ErrorContains(t, err, "edge not found")
}

func TestNewInjector_NilDeps(t *testing.T) {
	inj := NewInjector(nil, nil)
	assert.NotNil(t, inj)
	assert.Nil(t, inj.runtime)
	assert.Nil(t, inj.dag)
}

func TestNewInjector_WithDeps(t *testing.T) {
	rt := &mockRuntime{}
	dag := &mockDAG{}
	inj := NewInjector(rt, dag)
	assert.NotNil(t, inj)
	assert.NotNil(t, inj.runtime)
	assert.NotNil(t, inj.dag)
}

package dag

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRuntimeController implements RuntimeController for testing.
type mockRuntimeController struct {
	mu           sync.Mutex
	deadCalls    []deadCall
	restartCalls []string
	agentInfo    map[string]*AgentInfo
	restartErr   error
}

type deadCall struct {
	agentID string
	reason  string
}

func newMockRuntime() *mockRuntimeController {
	return &mockRuntimeController{
		agentInfo: make(map[string]*AgentInfo),
	}
}

func (m *mockRuntimeController) NotifyAgentDead(agentID, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deadCalls = append(m.deadCalls, deadCall{agentID: agentID, reason: reason})
}

func (m *mockRuntimeController) GetAgentInfo(agentID string) (*AgentInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.agentInfo[agentID]
	return info, ok
}

func (m *mockRuntimeController) RestartAgent(_ context.Context, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restartCalls = append(m.restartCalls, agentID)
	return m.restartErr
}

// mockOrchestratorController implements OrchestratorController for testing.
type mockOrchestratorController struct {
	mu          sync.Mutex
	cancelCalls []string
	cancelOK    bool
}

func newMockOrchestrator() *mockOrchestratorController {
	return &mockOrchestratorController{cancelOK: true}
}

func (m *mockOrchestratorController) CancelAgent(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelCalls = append(m.cancelCalls, id)
	return m.cancelOK
}

func (m *mockOrchestratorController) CreateAgent(_ any) (string, error) {
	return "new-agent", nil
}

// mockPublisher implements Publisher for testing.
type mockPublisher struct {
	mu      sync.Mutex
	results []*ActionResult
}

func (m *mockPublisher) Publish(result *ActionResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = append(m.results, result)
}

// setupEngine creates a DAG engine with nodes for testing.
func setupEngine() *Engine {
	eng := NewEngine()
	_ = eng.AddNode(&DAGNode{
		ID:       "rt-1",
		Name:     "runtime-agent",
		Type:     "agent",
		Status:   StatusRunning,
		Metadata: map[string]any{"source": "runtime"},
	})
	_ = eng.AddNode(&DAGNode{
		ID:       "orch-1",
		Name:     "orch-agent",
		Type:     "agent",
		Status:   StatusRunning,
		Metadata: map[string]any{"source": "orchestrator"},
	})
	_ = eng.AddNode(&DAGNode{
		ID:       "dead-1",
		Name:     "dead-agent",
		Type:     "agent",
		Status:   StatusDead,
		Metadata: map[string]any{"source": "runtime"},
	})
	_ = eng.AddNode(&DAGNode{
		ID:     "unknown-src",
		Name:   "no-source",
		Type:   "agent",
		Status: StatusRunning,
	})
	return eng
}

func TestExecuteAction_KillRuntimeAgent(t *testing.T) {
	eng := setupEngine()
	rt := newMockRuntime()
	orch := newMockOrchestrator()
	pub := &mockPublisher{}

	ie := NewInteractionEngine(eng, rt, orch)
	ie.SetPublisher(pub)

	result, err := ie.ExecuteAction(context.Background(), "rt-1", "kill")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "rt-1", result.NodeID)
	assert.Equal(t, "kill", result.Action)

	// Verify runtime was notified.
	rt.mu.Lock()
	require.Len(t, rt.deadCalls, 1)
	assert.Equal(t, "rt-1", rt.deadCalls[0].agentID)
	rt.mu.Unlock()

	// Verify DAG status updated to dead.
	node, ok := eng.GetNode("rt-1")
	require.True(t, ok)
	assert.Equal(t, StatusDead, node.Status)

	// Verify publisher was called.
	pub.mu.Lock()
	require.Len(t, pub.results, 1)
	assert.Equal(t, result.ActionID, pub.results[0].ActionID)
	pub.mu.Unlock()
}

func TestExecuteAction_KillOrchestratorAgent(t *testing.T) {
	eng := setupEngine()
	rt := newMockRuntime()
	orch := newMockOrchestrator()

	ie := NewInteractionEngine(eng, rt, orch)

	result, err := ie.ExecuteAction(context.Background(), "orch-1", "kill")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)

	// Verify orchestrator was called.
	orch.mu.Lock()
	require.Len(t, orch.cancelCalls, 1)
	assert.Equal(t, "orch-1", orch.cancelCalls[0])
	orch.mu.Unlock()

	// Verify DAG status updated to dead.
	node, ok := eng.GetNode("orch-1")
	require.True(t, ok)
	assert.Equal(t, StatusDead, node.Status)
}

func TestExecuteAction_KillUnknownNode(t *testing.T) {
	eng := NewEngine()
	rt := newMockRuntime()
	orch := newMockOrchestrator()

	ie := NewInteractionEngine(eng, rt, orch)

	_, err := ie.ExecuteAction(context.Background(), "missing", "kill")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

func TestExecuteAction_KillOrchestratorNotFound(t *testing.T) {
	eng := setupEngine()
	rt := newMockRuntime()
	orch := newMockOrchestrator()
	orch.cancelOK = false

	ie := NewInteractionEngine(eng, rt, orch)

	result, err := ie.ExecuteAction(context.Background(), "orch-1", "kill")
	require.NoError(t, err)
	assert.False(t, result.Success)
}

func TestExecuteAction_KillNoRuntimeController(t *testing.T) {
	eng := setupEngine()
	orch := newMockOrchestrator()

	ie := NewInteractionEngine(eng, nil, orch)

	_, err := ie.ExecuteAction(context.Background(), "rt-1", "kill")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime controller not available")
}

func TestExecuteAction_KillNoOrchestratorController(t *testing.T) {
	eng := setupEngine()
	rt := newMockRuntime()

	ie := NewInteractionEngine(eng, rt, nil)

	_, err := ie.ExecuteAction(context.Background(), "orch-1", "kill")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "orchestrator controller not available")
}

func TestExecuteAction_Resume(t *testing.T) {
	eng := setupEngine()
	rt := newMockRuntime()

	ie := NewInteractionEngine(eng, rt, nil)

	result, err := ie.ExecuteAction(context.Background(), "dead-1", "resume")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "resume", result.Action)

	rt.mu.Lock()
	require.Len(t, rt.restartCalls, 1)
	assert.Equal(t, "dead-1", rt.restartCalls[0])
	rt.mu.Unlock()
}

func TestExecuteAction_ResumeFailure(t *testing.T) {
	eng := setupEngine()
	rt := newMockRuntime()
	rt.restartErr = errors.New("restart timeout")

	ie := NewInteractionEngine(eng, rt, nil)

	result, err := ie.ExecuteAction(context.Background(), "dead-1", "resume")
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Message, "restart timeout")
}

func TestExecuteAction_ResumeUnknownNode(t *testing.T) {
	eng := NewEngine()
	rt := newMockRuntime()

	ie := NewInteractionEngine(eng, rt, nil)

	_, err := ie.ExecuteAction(context.Background(), "missing", "resume")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

func TestExecuteAction_Retry(t *testing.T) {
	eng := setupEngine()
	rt := newMockRuntime()

	ie := NewInteractionEngine(eng, rt, nil)

	result, err := ie.ExecuteAction(context.Background(), "dead-1", "retry")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "retry", result.Action)

	rt.mu.Lock()
	require.Len(t, rt.restartCalls, 1)
	rt.mu.Unlock()
}

func TestExecuteAction_RetryFailure(t *testing.T) {
	eng := setupEngine()
	rt := newMockRuntime()
	rt.restartErr = errors.New("agent crash loop")

	ie := NewInteractionEngine(eng, rt, nil)

	result, err := ie.ExecuteAction(context.Background(), "dead-1", "retry")
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Message, "agent crash loop")
}

func TestExecuteAction_Inspect(t *testing.T) {
	eng := setupEngine()
	ie := NewInteractionEngine(eng, nil, nil)

	result, err := ie.ExecuteAction(context.Background(), "rt-1", "inspect")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "inspect", result.Action)
	assert.Contains(t, result.Message, "rt-1")
	assert.Contains(t, result.Message, "running")
}

func TestExecuteAction_InspectUnknownNode(t *testing.T) {
	eng := NewEngine()
	ie := NewInteractionEngine(eng, nil, nil)

	_, err := ie.ExecuteAction(context.Background(), "missing", "inspect")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

func TestExecuteAction_UnknownAction(t *testing.T) {
	eng := setupEngine()
	ie := NewInteractionEngine(eng, nil, nil)

	_, err := ie.ExecuteAction(context.Background(), "rt-1", "deploy")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownAction)
}

func TestExecuteAction_CanceledContext(t *testing.T) {
	eng := setupEngine()
	ie := NewInteractionEngine(eng, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ie.ExecuteAction(ctx, "rt-1", "inspect")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

func TestExecuteAction_KillDeadNodeNoStatusChange(t *testing.T) {
	eng := setupEngine()
	rt := newMockRuntime()
	orch := newMockOrchestrator()

	ie := NewInteractionEngine(eng, rt, orch)

	// Kill a node that is already dead — should succeed without DAG status change.
	result, err := ie.ExecuteAction(context.Background(), "dead-1", "kill")
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestExecuteAction_ConcurrentActions(t *testing.T) {
	eng := NewEngine()
	for i := 0; i < 20; i++ {
		_ = eng.AddNode(&DAGNode{
			ID:       fmt.Sprintf("n%d", i),
			Name:     fmt.Sprintf("node-%d", i),
			Type:     "agent",
			Status:   StatusRunning,
			Metadata: map[string]any{"source": "runtime"},
		})
	}

	rt := newMockRuntime()
	ie := NewInteractionEngine(eng, rt, nil)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, _ = ie.ExecuteAction(context.Background(), fmt.Sprintf("n%d", id), "kill")
		}(i)
	}
	wg.Wait()

	// All nodes should be dead.
	for i := 0; i < 20; i++ {
		node, ok := eng.GetNode(fmt.Sprintf("n%d", i))
		require.True(t, ok)
		assert.Equal(t, StatusDead, node.Status)
	}

	// All kill calls recorded.
	rt.mu.Lock()
	assert.Len(t, rt.deadCalls, 20)
	rt.mu.Unlock()
}

func TestNewInteractionEngine_NilDAG(t *testing.T) {
	ie := NewInteractionEngine(nil, nil, nil)
	assert.Nil(t, ie)
}

func TestExecuteAction_KillUnknownSourceNode(t *testing.T) {
	eng := setupEngine()
	rt := newMockRuntime()
	orch := newMockOrchestrator()

	ie := NewInteractionEngine(eng, rt, orch)

	// Node with no "source" in metadata defaults to "unknown" source.
	result, err := ie.ExecuteAction(context.Background(), "unknown-src", "kill")
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.Message, "unknown")
}

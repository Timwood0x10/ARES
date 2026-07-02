package agents_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	apperrors "github.com/Timwood0x10/ares/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents"
)

// mockAgentRepository implements core.AgentRepository with controllable behavior.
type mockAgentRepository struct {
	mu     sync.Mutex
	agents map[string]*core.Agent

	// Controllable errors — when non-nil, these override default behavior.
	createErr error
	getErr    error
	updateErr error
	deleteErr error
	listErr   error

	// Call counters for verification.
	createCalls int
	getCalls    int
	updateCalls int
	deleteCalls int
	listCalls   int
}

func newMockAgentRepository() *mockAgentRepository {
	return &mockAgentRepository{
		agents: make(map[string]*core.Agent),
	}
}

func (m *mockAgentRepository) Create(_ context.Context, agent *core.Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls++
	if m.createErr != nil {
		return m.createErr
	}
	if agent == nil {
		return errors.New("agent is nil")
	}
	if _, exists := m.agents[agent.ID]; exists {
		return errors.New("agent already exists")
	}
	m.agents[agent.ID] = agent
	return nil
}

func (m *mockAgentRepository) Get(_ context.Context, agentID string) (*core.Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls++
	if m.getErr != nil {
		return nil, m.getErr
	}
	if agentID == "" {
		return nil, errors.New("agent ID is empty")
	}
	agent, exists := m.agents[agentID]
	if !exists {
		return nil, apperrors.ErrNotFound
	}
	cp := *agent
	return &cp, nil
}

func (m *mockAgentRepository) Update(_ context.Context, agent *core.Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCalls++
	if m.updateErr != nil {
		return m.updateErr
	}
	if agent == nil {
		return errors.New("agent is nil")
	}
	if _, exists := m.agents[agent.ID]; !exists {
		return errors.New("agent not found")
	}
	m.agents[agent.ID] = agent
	return nil
}

func (m *mockAgentRepository) Delete(_ context.Context, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls++
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if agentID == "" {
		return errors.New("agent ID is empty")
	}
	if _, exists := m.agents[agentID]; !exists {
		return errors.New("agent not found")
	}
	delete(m.agents, agentID)
	return nil
}

func (m *mockAgentRepository) List(_ context.Context, filter *core.AgentFilter) ([]*core.Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listCalls++
	if m.listErr != nil {
		return nil, m.listErr
	}
	result := make([]*core.Agent, 0, len(m.agents))
	for _, agent := range m.agents {
		if filter != nil {
			if filter.Type != "" && agent.Type != filter.Type {
				continue
			}
			if filter.Status != "" && agent.Status != filter.Status {
				continue
			}
		}
		cp := *agent
		result = append(result, &cp)
	}
	return result, nil
}

// newTestService creates a Service with the given repo and nil memory manager.
func newTestService(repo core.AgentRepository) *agents.Service {
	svc, err := agents.NewService(&agents.Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 5 * time.Second,
			MaxRetries:     1,
			RetryDelay:     100 * time.Millisecond,
		},
		MemoryMgr: nil,
		Repo:      repo,
	})
	if err != nil {
		panic(err)
	}
	return svc
}

// ---------------------------------------------------------------------------
// Error sentinel tests
// ---------------------------------------------------------------------------

func TestErrorMessages(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"ErrInvalidAgentID", agents.ErrInvalidAgentID, "invalid agent ID"},
		{"ErrAgentNotFound", agents.ErrAgentNotFound, "agent not found: not found"},
		{"ErrAgentAlreadyExists", agents.ErrAgentAlreadyExists, "agent already exists"},
		{"ErrInvalidTaskID", agents.ErrInvalidTaskID, "invalid task ID"},
		{"ErrTaskNotFound", agents.ErrTaskNotFound, "task not found: not found"},
		{"ErrInvalidConfig", agents.ErrInvalidConfig, "invalid configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantMsg, tt.err.Error())
		})
	}
}

func TestErrorWrapping(t *testing.T) {
	// ErrAgentNotFound and ErrTaskNotFound should wrap apperrors.ErrNotFound.
	assert.True(t, errors.Is(agents.ErrAgentNotFound, apperrors.ErrNotFound),
		"ErrAgentNotFound should wrap apperrors.ErrNotFound")
	assert.True(t, errors.Is(agents.ErrTaskNotFound, apperrors.ErrNotFound),
		"ErrTaskNotFound should wrap apperrors.ErrNotFound")

	// Other sentinels should NOT wrap apperrors.ErrNotFound.
	assert.False(t, errors.Is(agents.ErrInvalidAgentID, apperrors.ErrNotFound))
	assert.False(t, errors.Is(agents.ErrAgentAlreadyExists, apperrors.ErrNotFound))
	assert.False(t, errors.Is(agents.ErrInvalidTaskID, apperrors.ErrNotFound))
	assert.False(t, errors.Is(agents.ErrInvalidConfig, apperrors.ErrNotFound))
}

// ---------------------------------------------------------------------------
// NewService tests
// ---------------------------------------------------------------------------

func TestNewService_NilConfig(t *testing.T) {
	svc, err := agents.NewService(nil)
	assert.Nil(t, svc)
	assert.ErrorIs(t, err, agents.ErrInvalidConfig)
}

func TestNewService_NilBaseConfig(t *testing.T) {
	svc, err := agents.NewService(&agents.Config{
		Repo: newMockAgentRepository(),
	})
	require.NoError(t, err)
	require.NotNil(t, svc)
	// Should have applied default BaseConfig.
}

func TestNewService_Valid(t *testing.T) {
	repo := newMockAgentRepository()
	svc := newTestService(repo)
	require.NotNil(t, svc)

	// Create an agent to prove the service is wired correctly.
	agent, err := svc.CreateAgent(context.Background(), &core.AgentConfig{ID: "a1", Name: "test"})
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "a1", agent.ID)
}

// ---------------------------------------------------------------------------
// CreateAgent tests
// ---------------------------------------------------------------------------

func TestCreateAgent_NilConfig(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	agent, err := svc.CreateAgent(context.Background(), nil)
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, agents.ErrInvalidConfig)
}

func TestCreateAgent_EmptyID(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	agent, err := svc.CreateAgent(context.Background(), &core.AgentConfig{ID: ""})
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, agents.ErrInvalidAgentID)
}

func TestCreateAgent_AlreadyExists(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	// First create succeeds.
	err := repo.Create(ctx, &core.Agent{ID: "dup"})
	require.NoError(t, err)

	svc := newTestService(repo)
	agent, err := svc.CreateAgent(ctx, &core.AgentConfig{ID: "dup"})
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, agents.ErrAgentAlreadyExists)
}

func TestCreateAgent_GetError(t *testing.T) {
	repo := newMockAgentRepository()
	repo.getErr = errors.New("db unreachable")

	svc := newTestService(repo)
	agent, err := svc.CreateAgent(context.Background(), &core.AgentConfig{ID: "a1"})
	assert.Nil(t, agent)
	assert.ErrorContains(t, err, "check agent existence")
}

func TestCreateAgent_SuccessWithRepo(t *testing.T) {
	repo := newMockAgentRepository()
	svc := newTestService(repo)

	agent, err := svc.CreateAgent(context.Background(), &core.AgentConfig{
		ID:   "a1",
		Name: "alpha",
		Type: "leader",
		Config: map[string]interface{}{
			"model": "gpt-4",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "a1", agent.ID)
	assert.Equal(t, "alpha", agent.Name)
	assert.Equal(t, "leader", agent.Type)
	assert.Equal(t, core.AgentStatusReady, agent.Status)
	assert.Equal(t, "", agent.SessionID) // memoryMgr is nil
	assert.NotZero(t, agent.CreatedAt)
	assert.NotZero(t, agent.UpdatedAt)
	assert.Equal(t, agent.CreatedAt, agent.UpdatedAt)

	// Verify it was persisted.
	got, err := repo.Get(context.Background(), "a1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "a1", got.ID)
}

func TestCreateAgent_WithoutRepo(t *testing.T) {
	svc, err := agents.NewService(&agents.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: time.Second},
		Repo:       nil,
	})
	require.NoError(t, err)

	agent, err := svc.CreateAgent(context.Background(), &core.AgentConfig{
		ID:   "a1",
		Name: "alpha",
	})
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "a1", agent.ID)
	assert.Equal(t, "alpha", agent.Name)
	assert.Equal(t, "", agent.SessionID)
}

func TestCreateAgent_RepoCreateError(t *testing.T) {
	repo := newMockAgentRepository()
	repo.createErr = errors.New("disk full")

	svc := newTestService(repo)
	// The pre-check Get will return ErrNotFound (no duplicate), then Create will fail.
	agent, err := svc.CreateAgent(context.Background(), &core.AgentConfig{ID: "a1"})
	assert.Nil(t, agent)
	assert.ErrorContains(t, err, "create agent")
}

// ---------------------------------------------------------------------------
// GetAgent tests
// ---------------------------------------------------------------------------

func TestGetAgent_EmptyID(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	agent, err := svc.GetAgent(context.Background(), "")
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, agents.ErrInvalidAgentID)
}

func TestGetAgent_NoRepo(t *testing.T) {
	svc, err := agents.NewService(&agents.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: time.Second},
		Repo:       nil,
	})
	require.NoError(t, err)

	agent, err := svc.GetAgent(context.Background(), "a1")
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, agents.ErrAgentNotFound)
}

func TestGetAgent_NotFound(t *testing.T) {
	repo := newMockAgentRepository()
	svc := newTestService(repo)

	agent, err := svc.GetAgent(context.Background(), "nonexistent")
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, agents.ErrAgentNotFound)
}

func TestGetAgent_GetError(t *testing.T) {
	repo := newMockAgentRepository()
	repo.getErr = errors.New("connection reset")

	svc := newTestService(repo)
	agent, err := svc.GetAgent(context.Background(), "a1")
	assert.Nil(t, agent)
	assert.ErrorContains(t, err, "get agent")
}

func TestGetAgent_Success(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)
	agent, err := svc.GetAgent(ctx, "a1")
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "a1", agent.ID)
	assert.Equal(t, "alpha", agent.Name)
}

// ---------------------------------------------------------------------------
// UpdateAgent tests
// ---------------------------------------------------------------------------

func TestUpdateAgent_EmptyID(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	agent, err := svc.UpdateAgent(context.Background(), "", map[string]interface{}{})
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, agents.ErrInvalidAgentID)
}

func TestUpdateAgent_NoRepo(t *testing.T) {
	svc, err := agents.NewService(&agents.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: time.Second},
		Repo:       nil,
	})
	require.NoError(t, err)

	agent, err := svc.UpdateAgent(context.Background(), "a1", map[string]interface{}{})
	assert.Nil(t, agent)
	assert.EqualError(t, err, "agent repository not configured")
}

func TestUpdateAgent_NotFound(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	agent, err := svc.UpdateAgent(context.Background(), "nonexistent", map[string]interface{}{})
	assert.Nil(t, agent)
	assert.ErrorIs(t, err, agents.ErrAgentNotFound)
}

func TestUpdateAgent_GetError(t *testing.T) {
	repo := newMockAgentRepository()
	repo.getErr = errors.New("timeout")
	svc := newTestService(repo)

	agent, err := svc.UpdateAgent(context.Background(), "a1", map[string]interface{}{})
	assert.Nil(t, agent)
	assert.ErrorContains(t, err, "get agent")
}

func TestUpdateAgent_Success(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{
		ID:        "a1",
		Name:      "alpha",
		Type:      "leader",
		Status:    core.AgentStatusReady,
		SessionID: "sess1",
		Config:    map[string]interface{}{"model": "gpt-3"},
	})
	require.NoError(t, err)

	svc := newTestService(repo)
	updated, err := svc.UpdateAgent(ctx, "a1", map[string]interface{}{
		"name":       "Alpha v2",
		"status":     core.AgentStatusRunning,
		"type":       "sub",
		"session_id": "sess2",
		"config":     map[string]interface{}{"model": "gpt-4"},
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "Alpha v2", updated.Name)
	assert.Equal(t, core.AgentStatusRunning, updated.Status)
	assert.Equal(t, "sub", updated.Type)
	assert.Equal(t, "sess2", updated.SessionID)
	assert.Equal(t, map[string]interface{}{"model": "gpt-4"}, updated.Config)
	assert.GreaterOrEqual(t, updated.UpdatedAt, updated.CreatedAt)
}

func TestUpdateAgent_RepoUpdateError(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha"})
	require.NoError(t, err)

	repo.updateErr = errors.New("update failed")
	svc := newTestService(repo)

	updated, err := svc.UpdateAgent(ctx, "a1", map[string]interface{}{
		"name": "Alpha v2",
	})
	assert.Nil(t, updated)
	assert.ErrorContains(t, err, "update agent")
}

// ---------------------------------------------------------------------------
// DeleteAgent tests
// ---------------------------------------------------------------------------

func TestDeleteAgent_EmptyID(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	err := svc.DeleteAgent(context.Background(), "")
	assert.ErrorIs(t, err, agents.ErrInvalidAgentID)
}

func TestDeleteAgent_NoRepo(t *testing.T) {
	svc, err := agents.NewService(&agents.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: time.Second},
		Repo:       nil,
	})
	require.NoError(t, err)

	err = svc.DeleteAgent(context.Background(), "a1")
	assert.EqualError(t, err, "agent repository not configured")
}

func TestDeleteAgent_NotFound(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	err := svc.DeleteAgent(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, agents.ErrAgentNotFound)
}

func TestDeleteAgent_GetError(t *testing.T) {
	repo := newMockAgentRepository()
	repo.getErr = errors.New("timeout")
	svc := newTestService(repo)

	err := svc.DeleteAgent(context.Background(), "a1")
	assert.ErrorContains(t, err, "get agent")
}

func TestDeleteAgent_Success(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha"})
	require.NoError(t, err)

	svc := newTestService(repo)
	err = svc.DeleteAgent(ctx, "a1")
	assert.NoError(t, err)

	// Verify it is gone.
	_, err = repo.Get(ctx, "a1")
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}

func TestDeleteAgent_RepoDeleteError(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1"})
	require.NoError(t, err)

	repo.deleteErr = errors.New("delete failed")
	svc := newTestService(repo)

	err = svc.DeleteAgent(ctx, "a1")
	assert.ErrorContains(t, err, "delete agent")
}

// ---------------------------------------------------------------------------
// ListAgents tests
// ---------------------------------------------------------------------------

func TestListAgents_NoRepo(t *testing.T) {
	svc, err := agents.NewService(&agents.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: time.Second},
		Repo:       nil,
	})
	require.NoError(t, err)

	agentsList, pagination, err := svc.ListAgents(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, agentsList)
	assert.Equal(t, int64(0), pagination.Total)
	assert.Equal(t, 1, pagination.Page)
	assert.Equal(t, false, pagination.HasMore)
}

func TestListAgents_NoFilter(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		id := string(rune('a' + i))
		_ = repo.Create(ctx, &core.Agent{ID: id, Name: "agent-" + id, Type: "leader", Status: core.AgentStatusReady})
	}

	svc := newTestService(repo)
	agentsList, pagination, err := svc.ListAgents(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, agentsList, 3)
	assert.Equal(t, int64(3), pagination.Total)
}

func TestListAgents_WithFilter(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	_ = repo.Create(ctx, &core.Agent{ID: "l1", Type: "leader", Status: core.AgentStatusReady})
	_ = repo.Create(ctx, &core.Agent{ID: "l2", Type: "leader", Status: core.AgentStatusRunning})
	_ = repo.Create(ctx, &core.Agent{ID: "s1", Type: "sub", Status: core.AgentStatusReady})

	svc := newTestService(repo)

	// Filter by type.
	filter := &core.AgentFilter{Type: "leader"}
	agentsList, pagination, err := svc.ListAgents(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, agentsList, 2)
	assert.Equal(t, int64(2), pagination.Total)

	// Filter by status.
	filter2 := &core.AgentFilter{Status: core.AgentStatusReady}
	agentsList2, _, err := svc.ListAgents(ctx, filter2)
	require.NoError(t, err)
	assert.Len(t, agentsList2, 2)

	// Filter by type + status.
	filter3 := &core.AgentFilter{Type: "leader", Status: core.AgentStatusRunning}
	agentsList3, _, err := svc.ListAgents(ctx, filter3)
	require.NoError(t, err)
	assert.Len(t, agentsList3, 1)
	assert.Equal(t, "l2", agentsList3[0].ID)
}

func TestListAgents_RepoError(t *testing.T) {
	repo := newMockAgentRepository()
	repo.listErr = errors.New("db error")
	svc := newTestService(repo)

	agentsList, pagination, err := svc.ListAgents(context.Background(), &core.AgentFilter{})
	assert.ErrorContains(t, err, "list agents")
	assert.Nil(t, agentsList)
	assert.Nil(t, pagination)
}

func TestListAgents_Pagination(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		_ = repo.Create(ctx, &core.Agent{ID: string(rune('a' + i))})
	}

	svc := newTestService(repo)
	filter := &core.AgentFilter{
		Pagination: &core.PaginationRequest{
			Page:     1,
			PageSize: 5,
		},
	}
	agentsList, pagination, err := svc.ListAgents(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, agentsList, 10) // Service returns all agents; pagination is metadata-only
	assert.Equal(t, int64(10), pagination.Total)
	assert.Equal(t, 1, pagination.Page)
	assert.Equal(t, 5, pagination.PageSize)
	assert.Equal(t, 2, pagination.TotalPages)
	assert.True(t, pagination.HasMore)
}

// ---------------------------------------------------------------------------
// ExecuteTask tests
// ---------------------------------------------------------------------------

func TestExecuteTask_NilTask(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	result, err := svc.ExecuteTask(context.Background(), nil)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, agents.ErrInvalidConfig)
}

func TestExecuteTask_EmptyAgentID(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	result, err := svc.ExecuteTask(context.Background(), &core.Task{AgentID: ""})
	assert.Nil(t, result)
	assert.ErrorIs(t, err, agents.ErrInvalidAgentID)
}

func TestExecuteTask_NoRepo(t *testing.T) {
	svc, err := agents.NewService(&agents.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: time.Second},
		Repo:       nil,
	})
	require.NoError(t, err)

	result, err := svc.ExecuteTask(context.Background(), &core.Task{AgentID: "a1", ID: "t1"})
	assert.Nil(t, result)
	assert.EqualError(t, err, "agent repository not configured")
}

func TestExecuteTask_AgentNotFound(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	result, err := svc.ExecuteTask(context.Background(), &core.Task{AgentID: "nonexistent", ID: "t1"})
	assert.Nil(t, result)
	assert.ErrorIs(t, err, agents.ErrAgentNotFound)
}

func TestExecuteTask_GetError(t *testing.T) {
	repo := newMockAgentRepository()
	repo.getErr = errors.New("db timeout")
	svc := newTestService(repo)

	result, err := svc.ExecuteTask(context.Background(), &core.Task{AgentID: "a1", ID: "t1"})
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "get agent")
}

func TestExecuteTask_FirstUpdateFails(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	repo.updateErr = errors.New("update failed")
	svc := newTestService(repo)

	result, err := svc.ExecuteTask(ctx, &core.Task{AgentID: "a1", ID: "t1", Type: "simple"})
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "update agent status")
}

func TestExecuteTask_Simple(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)
	result, err := svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "simple",
		Payload: map[string]interface{}{"input": "hello world"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "t1", result.TaskID)
	assert.Equal(t, "a1", result.AgentID)
	assert.True(t, result.Success)
	assert.Equal(t, "hello world", result.Data["output"])
	assert.Equal(t, "simple", result.Data["task_type"])
	assert.Equal(t, "hello world", result.Data["input"])
	assert.NotZero(t, result.CompletedAt)

	// Verify agent status was reset to ready.
	agent, err := repo.Get(ctx, "a1")
	require.NoError(t, err)
	assert.Equal(t, core.AgentStatusReady, agent.Status)
}

func TestExecuteTask_DefaultType(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)
	result, err := svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "", // default should map to "simple"
		Payload: map[string]interface{}{"input": "hello"},
	})
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Data["output"])
	assert.Equal(t, "", result.Data["task_type"])
}

func TestExecuteTask_Retrieve(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)
	result, err := svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "retrieve",
		Payload: map[string]interface{}{"input": "query data"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Retrieved information for: query data", result.Data["output"])
	assert.Equal(t, "retrieve", result.Data["task_type"])
}

func TestExecuteTask_Generate(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)
	result, err := svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "generate",
		Payload: map[string]interface{}{"input": "write poem"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Generated content for: write poem", result.Data["output"])
	assert.Equal(t, "generate", result.Data["task_type"])
}

func TestExecuteTask_UnknownType(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)
	result, err := svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "custom",
		Payload: map[string]interface{}{"input": "do stuff"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Processed task of type 'custom'", result.Data["output"])
	assert.Equal(t, "custom", result.Data["task_type"])
}

func TestExecuteTask_UsesContentField(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)
	result, err := svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "simple",
		Payload: map[string]interface{}{"content": "from content field"},
	})
	require.NoError(t, err)
	assert.Equal(t, "from content field", result.Data["output"])
	assert.Equal(t, "from content field", result.Data["input"])
}

func TestExecuteTask_NoPayload(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)
	result, err := svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "simple",
	})
	require.NoError(t, err)
	assert.Equal(t, "", result.Data["output"])
}

// failAfterNUpdates is a helper that returns an error on the Nth call and nil otherwise.
func failAfterNUpdates(n int) func() error {
	var mu sync.Mutex
	count := 0
	return func() error {
		mu.Lock()
		count++
		mu.Unlock()
		if count >= n {
			return errors.New("update failed")
		}
		return nil
	}
}

type conditionalMockRepo struct {
	*mockAgentRepository
	updateErrFn func() error
}

func (c *conditionalMockRepo) Update(ctx context.Context, agent *core.Agent) error {
	c.mu.Lock()
	c.updateCalls++
	c.mu.Unlock()
	if c.updateErrFn != nil {
		return c.updateErrFn()
	}
	return c.mockAgentRepository.Update(ctx, agent)
}

func TestExecuteTask_SecondUpdateFails_StillReturnsResult(t *testing.T) {
	repo := &conditionalMockRepo{
		mockAgentRepository: newMockAgentRepository(),
		updateErrFn:         failAfterNUpdates(2),
	}
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)
	result, err := svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "simple",
		Payload: map[string]interface{}{"input": "hello"},
	})
	// The task should still succeed because the second update failure is logged, not returned.
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "t1", result.TaskID)
	assert.Equal(t, "hello", result.Data["output"])
}

// ---------------------------------------------------------------------------
// GetTaskResult tests
// ---------------------------------------------------------------------------

func TestGetTaskResult_EmptyID(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	result, err := svc.GetTaskResult(context.Background(), "")
	assert.Nil(t, result)
	assert.ErrorIs(t, err, agents.ErrInvalidTaskID)
}

func TestGetTaskResult_NotFound(t *testing.T) {
	svc := newTestService(newMockAgentRepository())
	result, err := svc.GetTaskResult(context.Background(), "nonexistent")
	assert.Nil(t, result)
	assert.ErrorIs(t, err, agents.ErrTaskNotFound)
}

func TestGetTaskResult_Success(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)

	// Execute a task to populate a result.
	_, err = svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "simple",
		Payload: map[string]interface{}{"input": "data"},
	})
	require.NoError(t, err)

	result, err := svc.GetTaskResult(ctx, "t1")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "t1", result.TaskID)
	assert.Equal(t, "a1", result.AgentID)
	assert.True(t, result.Success)
	assert.NotNil(t, result.Data["output"])
}

func TestGetTaskResult_ReturnsCopy(t *testing.T) {
	repo := newMockAgentRepository()
	ctx := context.Background()

	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	svc := newTestService(repo)

	_, err = svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "simple",
		Payload: map[string]interface{}{"input": "data"},
	})
	require.NoError(t, err)

	result1, err := svc.GetTaskResult(ctx, "t1")
	require.NoError(t, err)

	// Mutate the returned copy.
	result1.Data["output"] = "mutated"

	// The second retrieval should still have the original value.
	result2, err := svc.GetTaskResult(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, "data", result2.Data["output"])
}

func TestGetTaskResult_ConcurrentSafe(t *testing.T) {
	ctx := context.Background()

	// Store a result directly via ExecuteTask is not possible without a repo,
	// so we test concurrent reads of a known key. We'll create an agent+task.
	repo := newMockAgentRepository()
	svc := newTestService(repo)
	err := repo.Create(ctx, &core.Agent{ID: "a1", Name: "alpha", Status: core.AgentStatusReady})
	require.NoError(t, err)

	_, err = svc.ExecuteTask(ctx, &core.Task{
		ID:      "t1",
		AgentID: "a1",
		Type:    "simple",
		Payload: map[string]interface{}{"input": "data"},
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.GetTaskResult(ctx, "t1")
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
}

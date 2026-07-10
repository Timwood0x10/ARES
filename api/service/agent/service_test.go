// Package agent tests.
package agent

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)
	require.NotNil(t, s)
}

func TestCreateAgent(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	agent, err := s.CreateAgent(context.Background(), &core.AgentConfig{ID: "test-1"})
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "test-1", agent.ID)
	assert.NotEmpty(t, agent.SessionID)
	assert.Equal(t, core.AgentStatusReady, agent.Status)
	assert.Greater(t, agent.CreatedAt, int64(0))
}

func TestCreateAgent_EmptyID(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	agent, err := s.CreateAgent(context.Background(), &core.AgentConfig{ID: ""})
	require.Error(t, err)
	assert.Nil(t, agent)
}

func TestCreateAgent_NilConfig(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	agent, err := s.CreateAgent(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, agent)
}

func TestGetAgent(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	created, err := s.CreateAgent(context.Background(), &core.AgentConfig{ID: "test-2"})
	require.NoError(t, err)

	agent, err := s.GetAgent(context.Background(), "test-2")
	require.NoError(t, err)
	assert.Equal(t, created.ID, agent.ID)
	assert.Equal(t, created.SessionID, agent.SessionID)
}

func TestGetAgent_NotFound(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	agent, err := s.GetAgent(context.Background(), "non-existent")
	require.Error(t, err)
	assert.Nil(t, agent)
}

func TestDeleteAgent(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	_, err = s.CreateAgent(context.Background(), &core.AgentConfig{ID: "test-3"})
	require.NoError(t, err)

	err = s.DeleteAgent(context.Background(), "test-3")
	require.NoError(t, err)

	// Verify agent is gone
	agent, err := s.GetAgent(context.Background(), "test-3")
	require.Error(t, err)
	assert.Nil(t, agent)
}

func TestDeleteAgent_NotFound(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	err = s.DeleteAgent(context.Background(), "non-existent")
	require.Error(t, err)
}

func TestUpdateAgent(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	_, err = s.CreateAgent(context.Background(), &core.AgentConfig{ID: "test-4"})
	require.NoError(t, err)

	agent, err := s.UpdateAgent(context.Background(), "test-4", map[string]interface{}{"name": "updated"})
	require.NoError(t, err)
	assert.Equal(t, "test-4", agent.ID)
}

func TestExecuteTask_NotImplemented(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	result, err := s.ExecuteTask(context.Background(), &core.Task{ID: "task-1", AgentID: "test-agent"})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "agent not found")
}

func TestGetTaskResult_NotFound(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	result, err := s.GetTaskResult(context.Background(), "task-1")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not found")
}

func TestExecuteAndGetTaskResult(t *testing.T) {
	s, err := New(nil)
	require.NoError(t, err)

	// Create an agent first.
	agent, err := s.CreateAgent(context.Background(), &core.AgentConfig{ID: "task-agent"})
	require.NoError(t, err)

	// Execute a task.
	taskResult, err := s.ExecuteTask(context.Background(), &core.Task{
		ID:      "my-task",
		AgentID: agent.ID,
		Payload: map[string]interface{}{"query": "test"},
	})
	require.NoError(t, err)
	require.NotNil(t, taskResult)
	assert.True(t, taskResult.Success)
	// The inner service creates a new task ID via the memory manager,
	// so the result's TaskID is the memory-manager-assigned ID, not the input ID.
	assert.NotEmpty(t, taskResult.TaskID)
	assert.NotEqual(t, "my-task", taskResult.TaskID, "should use memory-manager-assigned task ID")

	// Retrieve the result by the original task ID (used as cache key).
	retrieved, err := s.GetTaskResult(context.Background(), "my-task")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, taskResult.TaskID, retrieved.TaskID)
	assert.True(t, retrieved.Success)
}

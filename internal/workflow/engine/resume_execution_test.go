package engine

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/runtime"
)

// memoryCheckpointStore is a minimal in-memory CheckpointStore for resume tests.
type memoryCheckpointStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemCheckpointStore() *memoryCheckpointStore {
	return &memoryCheckpointStore{data: make(map[string][]byte)}
}

func (s *memoryCheckpointStore) Save(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = data
	return nil
}

func (s *memoryCheckpointStore) Load(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[key], nil
}

// TestDynamicExecutor_ExecuteDynamicFromCheckpoint verifies that a
// checkpointed workflow can be resumed and previously completed steps are
// not re-executed.
func TestDynamicExecutor_ExecuteDynamicFromCheckpoint(t *testing.T) {
	registry := NewAgentRegistry()
	require.NoError(t, registry.Register("echo", testAgentFactory(
		func(ctx context.Context, input any) (any, error) {
			return &models.RecommendResult{
				Items: []*models.RecommendItem{{Description: "echo"}},
			}, nil
		},
	)))

	workflow := &Workflow{
		ID:   "resume-test",
		Name: "Resume Test",
		Steps: []*Step{
			{ID: "s1", Name: "Step1", AgentType: "echo", Input: "hello"},
			{ID: "s2", Name: "Step2", AgentType: "echo", Input: "world", DependsOn: []string{"s1"}},
		},
	}

	dag, err := NewMutableDAG(workflow.Steps)
	require.NoError(t, err)

	ckptStore := newMemCheckpointStore()
	ckpt := runtime.ExperienceCheckpoint{
		SchemaVersion: 1,
		ExecutionID:   "resume-exec-1",
		WorkflowID:    "resume-test",
		Status:        "running",
		StepStates: []runtime.StepStateSnapshot{
			{StepID: "s1", Status: runtime.StepStatusCompleted, Output: "hello"},
		},
		StateVersion: 1,
	}
	ckptData, err := json.Marshal(ckpt)
	require.NoError(t, err)
	require.NoError(t, ckptStore.Save(context.Background(), "checkpoint/resume-exec-1", ckptData))

	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithCheckpointStore(ckptStore)

	result, err := executor.ExecuteDynamicFromCheckpoint(
		context.Background(), workflow, "input", dag, "resume-exec-1",
	)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "resume-exec-1", result.ExecutionID)
	require.Len(t, result.Steps, 2)
	assert.Equal(t, "s1", result.Steps[0].StepID)
	assert.Equal(t, "s2", result.Steps[1].StepID)
}

func TestDynamicExecutor_ResumeCheckpointNotFound(t *testing.T) {
	registry := NewAgentRegistry()
	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint).
		WithCheckpointStore(newMemCheckpointStore())

	_, err := executor.ExecuteDynamicFromCheckpoint(
		context.Background(),
		&Workflow{ID: "test", Steps: []*Step{{ID: "s1", AgentType: "echo"}}},
		"input",
		&MutableDAG{},
		"nonexistent",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint not found")
}

func TestDynamicExecutor_ResumeNoStore(t *testing.T) {
	registry := NewAgentRegistry()
	executor := NewDynamicExecutor(registry, ApplyAtCheckpoint)

	_, err := executor.ExecuteDynamicFromCheckpoint(
		context.Background(),
		&Workflow{ID: "test", Steps: []*Step{{ID: "s1", AgentType: "echo"}}},
		"input",
		&MutableDAG{},
		"exec-1",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint store not configured")
}

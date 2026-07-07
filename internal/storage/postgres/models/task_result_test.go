// Package models provides comprehensive tests for task result model.
package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestTaskResult_TableName tests table name returns correct value.
func TestTaskResult_TableName(t *testing.T) {
	result := &TaskResult{}
	assert.Equal(t, "task_results_1024", result.TableName())
}

// TestTaskResult_ValidFields tests valid field assignment.
func TestTaskResult_ValidFields(t *testing.T) {
	result := &TaskResult{
		ID:               "result-id",
		TenantID:         "tenant-1",
		SessionID:        "session-1",
		TaskType:         "test-task",
		AgentID:          "agent-1",
		Input:            map[string]interface{}{"prompt": "test input"},
		Output:           map[string]interface{}{"result": "test output"},
		Embedding:        []float64{0.1, 0.2, 0.3},
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		Status:           TaskStatusCompleted,
		LatencyMs:        100,
		Metadata:         map[string]interface{}{"key": "value"},
		CreatedAt:        time.Now(),
	}

	assert.Equal(t, "result-id", result.ID)
	assert.Equal(t, "tenant-1", result.TenantID)
	assert.Equal(t, "session-1", result.SessionID)
	assert.Equal(t, "test-task", result.TaskType)
	assert.Equal(t, "agent-1", result.AgentID)
	assert.NotNil(t, result.Input)
	assert.NotNil(t, result.Output)
	assert.Len(t, result.Embedding, 3)
	assert.Equal(t, "e5-large", result.EmbeddingModel)
	assert.Equal(t, 1, result.EmbeddingVersion)
	assert.Equal(t, TaskStatusCompleted, result.Status)
	assert.Equal(t, 100, result.LatencyMs)
	assert.True(t, result.IsSuccessful())
	assert.False(t, result.IsFailed())
}

// TestTaskResult_EmptyFields tests handling of empty fields.
func TestTaskResult_EmptyFields(t *testing.T) {
	result := &TaskResult{}

	assert.Empty(t, result.ID)
	assert.Empty(t, result.TenantID)
	assert.Empty(t, result.SessionID)
	assert.Empty(t, result.TaskType)
	assert.Empty(t, result.AgentID)
	assert.Nil(t, result.Input)
	assert.Nil(t, result.Output)
	assert.Nil(t, result.Embedding)
	assert.Empty(t, result.EmbeddingModel)
	assert.Equal(t, 0, result.EmbeddingVersion)
	assert.Empty(t, result.Status)
	assert.Empty(t, result.Error)
	assert.Equal(t, 0, result.LatencyMs)
	assert.Nil(t, result.Metadata)
	assert.True(t, result.CreatedAt.IsZero())
}

// TestTaskResult_EmptyEmbedding tests handling of empty embedding.
func TestTaskResult_EmptyEmbedding(t *testing.T) {
	result := &TaskResult{
		ID:        "result-id",
		SessionID: "session-1",
		TaskType:  "test-task",
		Embedding: []float64{},
		Status:    TaskStatusCompleted,
		CreatedAt: time.Now(),
	}

	assert.Empty(t, result.Embedding)
	assert.True(t, result.IsSuccessful())
}

// TestTaskResult_NilEmbedding tests handling of nil embedding.
func TestTaskResult_NilEmbedding(t *testing.T) {
	result := &TaskResult{
		ID:        "result-id",
		SessionID: "session-1",
		TaskType:  "test-task",
		Embedding: nil,
		Status:    TaskStatusCompleted,
		CreatedAt: time.Now(),
	}

	assert.Nil(t, result.Embedding)
	assert.True(t, result.IsSuccessful())
}

// TestTaskResult_StatusValues tests different status values.
func TestTaskResult_StatusValues(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{"completed status", TaskStatusCompleted},
		{"failed status", TaskStatusFailed},
		{"pending status", TaskStatusPending},
		{"running status", TaskStatusRunning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &TaskResult{
				ID:        "result-id",
				SessionID: "session-1",
				Status:    tt.status,
				CreatedAt: time.Now(),
			}

			assert.Equal(t, tt.status, result.Status)
		})
	}
}

// TestTaskResult_IsSuccessful tests success status check.
func TestTaskResult_IsSuccessful(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected bool
	}{
		{"completed is successful", TaskStatusCompleted, true},
		{"failed is not successful", TaskStatusFailed, false},
		{"pending is not successful", TaskStatusPending, false},
		{"running is not successful", TaskStatusRunning, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &TaskResult{
				ID:        "result-id",
				SessionID: "session-1",
				Status:    tt.status,
				CreatedAt: time.Now(),
			}

			assert.Equal(t, tt.expected, result.IsSuccessful())
		})
	}
}

// TestTaskResult_IsFailed tests failure status check.
func TestTaskResult_IsFailed(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected bool
	}{
		{"failed is failed", TaskStatusFailed, true},
		{"completed is not failed", TaskStatusCompleted, false},
		{"pending is not failed", TaskStatusPending, false},
		{"running is not failed", TaskStatusRunning, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &TaskResult{
				ID:        "result-id",
				SessionID: "session-1",
				Status:    tt.status,
				CreatedAt: time.Now(),
			}

			assert.Equal(t, tt.expected, result.IsFailed())
		})
	}
}

// TestTaskResult_LatencyMs tests latency handling.
func TestTaskResult_LatencyMs(t *testing.T) {
	tests := []struct {
		name    string
		latency int
	}{
		{"zero latency", 0},
		{"fast execution", 50},
		{"normal execution", 100},
		{"slow execution", 1000},
		{"very slow execution", 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &TaskResult{
				ID:        "result-id",
				SessionID: "session-1",
				LatencyMs: tt.latency,
				CreatedAt: time.Now(),
			}

			assert.Equal(t, tt.latency, result.LatencyMs)
		})
	}
}

// TestTaskResult_InputOutputComplex tests handling of complex input and output.
func TestTaskResult_InputOutputComplex(t *testing.T) {
	result := &TaskResult{
		ID: "result-id",
		Input: map[string]interface{}{
			"string": "value",
			"number": 123,
			"bool":   true,
			"array":  []string{"a", "b", "c"},
			"object": map[string]interface{}{"nested": "value"},
		},
		Output: map[string]interface{}{
			"result": "success",
			"count":  42,
			"items":  []string{"item1", "item2"},
		},
		CreatedAt: time.Now(),
	}

	assert.NotNil(t, result.Input)
	assert.Equal(t, "value", result.Input["string"])
	assert.Equal(t, 123, result.Input["number"])
	assert.True(t, result.Input["bool"].(bool))
	assert.Len(t, result.Input["array"].([]string), 3)
	assert.NotNil(t, result.Output)
	assert.Equal(t, "success", result.Output["result"])
	assert.Equal(t, 42, result.Output["count"])
	assert.Len(t, result.Output["items"].([]string), 2)
}

// TestTaskResult_NilInputOutput tests handling of nil input and output.
func TestTaskResult_NilInputOutput(t *testing.T) {
	result := &TaskResult{
		ID:        "result-id",
		SessionID: "session-1",
		Input:     nil,
		Output:    nil,
		CreatedAt: time.Now(),
	}

	assert.Nil(t, result.Input)
	assert.Nil(t, result.Output)
}

// TestTaskResult_EmptyInputOutput tests handling of empty input and output.
func TestTaskResult_EmptyInputOutput(t *testing.T) {
	result := &TaskResult{
		ID:        "result-id",
		SessionID: "session-1",
		Input:     map[string]interface{}{},
		Output:    map[string]interface{}{},
		CreatedAt: time.Now(),
	}

	assert.Empty(t, result.Input)
	assert.Empty(t, result.Output)
}

// TestTaskResult_ErrorField tests handling of error field.
func TestTaskResult_ErrorField(t *testing.T) {
	tests := []struct {
		name  string
		error string
	}{
		{"no error", ""},
		{"simple error", "task failed"},
		{"detailed error", "timeout: operation took too long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &TaskResult{
				ID:        "result-id",
				SessionID: "session-1",
				Error:     tt.error,
				CreatedAt: time.Now(),
			}

			assert.Equal(t, tt.error, result.Error)
		})
	}
}

// TestTaskResult_MetadataComplex tests handling of complex metadata.
func TestTaskResult_MetadataComplex(t *testing.T) {
	result := &TaskResult{
		ID: "result-id",
		Metadata: map[string]interface{}{
			"string": "value",
			"number": 123,
			"bool":   true,
			"array":  []string{"a", "b", "c"},
			"object": map[string]interface{}{"nested": "value"},
		},
		CreatedAt: time.Now(),
	}

	assert.NotNil(t, result.Metadata)
	assert.Equal(t, "value", result.Metadata["string"])
	assert.Equal(t, 123, result.Metadata["number"])
	assert.True(t, result.Metadata["bool"].(bool))
	assert.Len(t, result.Metadata["array"].([]string), 3)
	assert.NotNil(t, result.Metadata["object"])
}

// TestTaskResult_NilMetadata tests handling of nil metadata.
func TestTaskResult_NilMetadata(t *testing.T) {
	result := &TaskResult{
		ID:        "result-id",
		Metadata:  nil,
		CreatedAt: time.Now(),
	}

	assert.Nil(t, result.Metadata)
}

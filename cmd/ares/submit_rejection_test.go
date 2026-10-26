package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestWriteSubmitRejection_QueueFullIs429 pins the classification S03 exists for: a queue at
// capacity is a "retry shortly", and answering 500 made it indistinguishable from a bug.
//
// The error fed in is produced by a REAL capped fabric (not a literal), and wrapped the way
// agentruntime wraps it, so the assertion covers the chain rather than a hand-made value.
func TestWriteSubmitRejection_QueueFullIs429(t *testing.T) {
	fabric := taskfabric.NewFabric().WithMaxReadyTasks(1)
	require.NoError(t, fabric.Create(&taskfabric.Task{ID: "t-1"}))
	rawErr := fabric.Create(&taskfabric.Task{ID: "t-2"})
	require.Error(t, rawErr)

	rr := httptest.NewRecorder()
	writeSubmitRejection(rr, fmt.Errorf("agentruntime: create task: %w", rawErr))

	require.Equal(t, http.StatusTooManyRequests, rr.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, "queue_full", body["code"], "the code is the machine-readable part")
	assert.Equal(t, "error", body["status"])
	assert.Equal(t, float64(1), body["depth"])
	assert.Equal(t, float64(1), body["limit"])
	assert.NotContains(t, body["error"], "depth 1", "the numbers travel as fields, not inside the message")
}

// TestWriteSubmitRejection_OtherFailuresStay500: only the queue case changed class. A storage fault
// or a programming error is still a server error, and pretending otherwise would invite retries
// that cannot help.
func TestWriteSubmitRejection_OtherFailuresStay500(t *testing.T) {
	rr := httptest.NewRecorder()
	writeSubmitRejection(rr, fmt.Errorf("agentruntime: create task: %w", assert.AnError))

	require.Equal(t, http.StatusInternalServerError, rr.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Nil(t, body["code"], "an unclassified failure must not carry a retryable code")
	assert.Contains(t, body["error"], "assert.AnError")
}

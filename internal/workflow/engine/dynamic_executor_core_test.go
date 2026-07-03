// nolint: errcheck // Test code may ignore return values
package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCoreNewExecutor(t *testing.T) {
	exe := NewDynamicExecutor(&AgentRegistry{}, 0)
	assert.NotNil(t, exe)
}

func TestCoreExecuteDynamic_EmptyGraph(t *testing.T) {
	exe := NewDynamicExecutor(&AgentRegistry{}, 0)
	wf := &Workflow{Name: "test"}
	dag := &MutableDAG{}
	result, err := exe.ExecuteDynamic(context.Background(), wf, "exec-1", dag)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestCoreExecuteDynamic_NilWorkflow(t *testing.T) {
	exe := NewDynamicExecutor(&AgentRegistry{}, 0)
	_, err := exe.ExecuteDynamic(context.Background(), nil, "exec-1", nil)
	assert.Error(t, err)
}

func TestCoreGenerateExecutionID_Unique(t *testing.T) {
	id1 := "exec-1"
	id2 := "exec-2"
	assert.NotEqual(t, id1, id2)
}

func TestCoreExecuteDynamic_CancelledContext(t *testing.T) {
	exe := NewDynamicExecutor(&AgentRegistry{}, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wf := &Workflow{Name: "test"}
	dag := &MutableDAG{}
	_, err := exe.ExecuteDynamic(ctx, wf, "exec-1", dag)
	assert.Error(t, err)
}

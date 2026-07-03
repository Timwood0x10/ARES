// nolint: errcheck // Test code may ignore return values
package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCoreNewExecutor(t *testing.T) {
	exe := NewDynamicExecutor(NewAgentRegistry(), 0)
	assert.NotNil(t, exe)
}

func TestCoreExecuteDynamic_EmptyGraph(t *testing.T) {
	exe := NewDynamicExecutor(NewAgentRegistry(), 0)
	wf := &Workflow{Name: "test"}
	dag, err := NewMutableDAG(nil)
	if err != nil {
		t.Fatalf("NewMutableDAG: %v", err)
	}
	result, err := exe.ExecuteDynamic(context.Background(), wf, "exec-1", dag)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestCoreExecuteDynamic_NilWorkflow(t *testing.T) {
	exe := NewDynamicExecutor(NewAgentRegistry(), 0)
	_, err := exe.ExecuteDynamic(context.Background(), nil, "exec-1", nil)
	assert.Error(t, err)
}

func TestCoreExecuteDynamic_CancelledContext(t *testing.T) {
	exe := NewDynamicExecutor(NewAgentRegistry(), 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wf := &Workflow{Name: "test"}
	dag, err := NewMutableDAG(nil)
	if err != nil {
		t.Fatalf("NewMutableDAG: %v", err)
	}
	_, err = exe.ExecuteDynamic(ctx, wf, "exec-1", dag)
	assert.Error(t, err)
}

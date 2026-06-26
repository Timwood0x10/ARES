package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolPlugin_RecordToolCall(t *testing.T) {
	collector := NewExecutionCollector("exec-1")
	toolPlugin := NewToolPlugin("tools")
	toolPlugin.WithCollector(collector)

	err := toolPlugin.AfterStep(context.Background(), "exec-1", &StepResult{
		StepID:   "s1",
		Status:   StepStatusCompleted,
		Duration: 100 * time.Millisecond,
		Metadata: map[string]string{
			PayloadKeyToolName: "search",
		},
	})
	require.NoError(t, err)

	tools := collector.ToolHistory()
	assert.Len(t, tools, 1)
	assert.Equal(t, "search", tools[0].ToolName)
	assert.Equal(t, "s1", tools[0].StepID)
	assert.True(t, tools[0].Success)
	assert.Equal(t, 100*time.Millisecond, tools[0].Duration)
}

func TestToolPlugin_NoMetadata(t *testing.T) {
	collector := NewExecutionCollector("exec-1")
	toolPlugin := NewToolPlugin("tools")
	toolPlugin.WithCollector(collector)

	err := toolPlugin.AfterStep(context.Background(), "exec-1", &StepResult{
		StepID: "s1",
		Status: StepStatusCompleted,
	})
	require.NoError(t, err)

	assert.Len(t, collector.ToolHistory(), 0)
}

func TestToolPlugin_Registry(t *testing.T) {
	p := NewToolPlugin("tools")
	assert.False(t, p.IsRegistered("search"))
	p.RegisterTool("search")
	assert.True(t, p.IsRegistered("search"))
	assert.False(t, p.IsRegistered("unknown"))
}

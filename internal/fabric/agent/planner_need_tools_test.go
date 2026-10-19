package agentfabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/llmcore"
)

// TestApplyNeedTools_UnionsIntoTheTask is the escape hatch itself: a task that was narrowed to
// the wrong set can be widened by asking, and the widened set is what the NEXT quantum reads.
func TestApplyNeedTools_UnionsIntoTheTask(t *testing.T) {
	task := models.NewTask("t1", "coder", nil)
	task.Payload[TaskToolCapabilityKey] = []string{"network"}

	call := llmcore.ToolCall{}
	call.Function.Name = NeedToolsToolName
	call.Function.Arguments = `{"extra_caps":["git"]}`

	(&plannerCognition{}).applyNeedTools(context.Background(), task, call)

	assert.Equal(t, []string{"git", "network"}, task.Payload[TaskToolCapabilityKey],
		"the request must be added to what the task already had, sorted for a stable prompt")

	// Asking again for the same capability must not duplicate it.
	(&plannerCognition{}).applyNeedTools(context.Background(), task, call)
	assert.Equal(t, []string{"git", "network"}, task.Payload[TaskToolCapabilityKey])
}

// TestApplyNeedTools_MalformedArgumentIsIgnored: the hatch is a hint. An unparseable hint must
// not end the quantum or corrupt the capability set.
func TestApplyNeedTools_MalformedArgumentIsIgnored(t *testing.T) {
	task := models.NewTask("t2", "coder", nil)
	task.Payload[TaskToolCapabilityKey] = []string{"network"}

	call := llmcore.ToolCall{}
	call.Function.Arguments = "{not json"

	(&plannerCognition{}).applyNeedTools(context.Background(), task, call)

	assert.Equal(t, []string{"network"}, task.Payload[TaskToolCapabilityKey])
}

// TestNeedToolsToolSchema pins the name the model has to be able to call.
func TestNeedToolsToolSchema(t *testing.T) {
	schema := needToolsToolSchema()
	require.Equal(t, NeedToolsToolName, schema.Function.Name)
	require.NotEmpty(t, schema.Function.Description)
	props, ok := schema.Function.Parameters["properties"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, props, needToolsArgKey)
}

// TestApplyNeedTools_KeepsCapsWrittenInAnotherShape is the asymmetry this used to have: the
// router accepted []string, []any and a comma-separated string, while the hatch asserted
// []string only. A task submitted by the SDK with []any therefore had its existing capabilities
// silently replaced by whatever need_tools asked for.
func TestApplyNeedTools_KeepsCapsWrittenInAnotherShape(t *testing.T) {
	for name, existing := range map[string]any{
		"[]any":        []any{"file"},
		"comma string": "file",
	} {
		t.Run(name, func(t *testing.T) {
			task := models.NewTask("t3", "coder", nil)
			task.Payload[TaskToolCapabilityKey] = existing

			call := llmcore.ToolCall{}
			call.Function.Arguments = `{"extra_caps":["git"]}`
			(&plannerCognition{}).applyNeedTools(context.Background(), task, call)

			assert.Equal(t, []string{"file", "git"}, task.Payload[TaskToolCapabilityKey],
				"the capability already on the task must survive whichever shape it was written in")
		})
	}
}

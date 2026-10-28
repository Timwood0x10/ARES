package agentfabric

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// failingBinder answers every tool call with the same error, so a test can ask what the tool node
// did with it.
type failingBinder struct {
	err        error
	idempotent bool
}

func (b failingBinder) CallTool(context.Context, string, map[string]any) (any, error) {
	return nil, b.err
}
func (b failingBinder) ListTools() []string                    { return []string{"write"} }
func (b failingBinder) IsToolIdempotent(string) bool           { return b.idempotent }
func (b failingBinder) GetToolSchemas() []resources.ToolSchema { return nil }

// TestToolCognition_ClassifiesToolFailures is the wiring half of the classification: the table in
// taskfabric is only worth having if the tool node actually consults it with the tool's declared
// retry safety.
//
// The same timeout from the same node reaches two different levels, which is the whole point: the
// decision depends on the tool, not on the error.
func TestToolCognition_ClassifiesToolFailures(t *testing.T) {
	timeout := context.DeadlineExceeded

	t.Run("unmarked write is killed", func(t *testing.T) {
		cog := &toolCognition{tool: "write", binder: failingBinder{err: timeout, idempotent: false}}

		_, err := cog.ExecuteStep(context.Background(), &models.Task{TaskID: "t", Payload: map[string]any{}})

		require.Error(t, err)
		assert.Equal(t, taskfabric.LevelFatal, taskfabric.LevelOf(err),
			"a timeout on a tool that never declared idempotency may have applied the write; "+
				"retrying would duplicate it")
		assert.True(t, errors.Is(err, timeout), "the original cause must stay reachable")
	})

	t.Run("idempotent tool is retried", func(t *testing.T) {
		cog := &toolCognition{tool: "write", binder: failingBinder{err: timeout, idempotent: true}}

		_, err := cog.ExecuteStep(context.Background(), &models.Task{TaskID: "t", Payload: map[string]any{}})

		require.Error(t, err)
		assert.Equal(t, taskfabric.LevelTransient, taskfabric.LevelOf(err),
			"a declared-idempotent tool can absorb a second call")
	})
}

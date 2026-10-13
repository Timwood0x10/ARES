package agentfabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestAnswerCognitionCarriesContinuationFlags locks the A6 terminal-metadata
// contract: executor_revived and requeued ride the terminal result metadata
// so a caller can tell "this session was continued by a revived agent" from
// "this is a first run" — both keys are absent when neither signal is stamped,
// so the metadata never fabricates a continuation that did not happen.
func TestAnswerCognitionCarriesContinuationFlags(t *testing.T) {
	t.Run("both flags present in metadata", func(t *testing.T) {
		task := &models.Task{
			TaskID:    "task-a6-1",
			AgentType: models.AgentType("ares/plan"),
			Payload: map[string]any{
				executorRevivedKey:            true,
				taskfabric.PayloadKeyRequeued: true,
			},
		}
		out, err := (&answerCognition{}).ExecuteStep(context.Background(), task)
		require.NoError(t, err)
		require.True(t, out.Done)

		assert.Equal(t, true, out.Result.Metadata[executorRevivedKey],
			"executor_revived must surface in terminal metadata")
		assert.Equal(t, true, out.Result.Metadata[taskfabric.PayloadKeyRequeued],
			"requeued must surface in terminal metadata")
	})

	t.Run("neither flag present → neither key in metadata", func(t *testing.T) {
		task := &models.Task{
			TaskID:    "task-a6-2",
			AgentType: models.AgentType("ares/plan"),
			Payload:   map[string]any{},
		}
		out, err := (&answerCognition{}).ExecuteStep(context.Background(), task)
		require.NoError(t, err)
		require.True(t, out.Done)

		_, hasRevived := out.Result.Metadata[executorRevivedKey]
		assert.False(t, hasRevived, "executor_revived must be absent when not stamped")

		_, hasRequeued := out.Result.Metadata[taskfabric.PayloadKeyRequeued]
		assert.False(t, hasRequeued, "requeued must be absent when not stamped")
	})

	t.Run("revived false is present and false", func(t *testing.T) {
		// "present and false" ≠ "absent": a quantum that ran through the
		// wrapper but was NOT revived must distinguish itself from one that
		// never ran through it.
		task := &models.Task{
			TaskID:    "task-a6-3",
			AgentType: models.AgentType("ares/plan"),
			Payload: map[string]any{
				executorRevivedKey: false,
			},
		}
		out, err := (&answerCognition{}).ExecuteStep(context.Background(), task)
		require.NoError(t, err)
		require.True(t, out.Done)

		got, ok := out.Result.Metadata[executorRevivedKey].(bool)
		require.True(t, ok, "executor_revived must be present (false)")
		assert.False(t, got, "executor_revived=false must surface as false")
	})

	t.Run("requeued false is absent (only true surfaces)", func(t *testing.T) {
		// requeued is only written when true; absent means "first run".
		task := &models.Task{
			TaskID:    "task-a6-4",
			AgentType: models.AgentType("ares/plan"),
			Payload: map[string]any{
				taskfabric.PayloadKeyRequeued: false,
			},
		}
		out, err := (&answerCognition{}).ExecuteStep(context.Background(), task)
		require.NoError(t, err)
		require.True(t, out.Done)

		_, hasRequeued := out.Result.Metadata[taskfabric.PayloadKeyRequeued]
		assert.False(t, hasRequeued, "requeued=false must NOT surface (absent = first run)")
	})
}

package agentfabric

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// TestAnswerCognitionTruncationMetadata pins the E4 contract: a terminal
// answer node stamped with the depth-guard truncation marker carries
// truncated=true in its result metadata; an unstamped node must not
// sprout the key.
func TestAnswerCognitionTruncationMetadata(t *testing.T) {
	t.Parallel()

	c := &answerCognition{}

	t.Run("stamped node carries truncated=true", func(t *testing.T) {
		t.Parallel()
		task := &models.Task{
			TaskID:    "task-stamped",
			AgentType: models.AgentType("ares/plan"),
			Payload: map[string]any{
				argMetadataPrefix + answerContentKey:   "partial answer",
				argMetadataPrefix + answerTruncatedKey: true,
			},
		}
		out, err := c.ExecuteStep(context.Background(), task)
		if err != nil {
			t.Fatalf("ExecuteStep: %v", err)
		}
		if !out.Done {
			t.Fatal("answer node must terminate the session (Done=true)")
		}
		if out.Result.Metadata[answerTruncatedKey] != true {
			t.Fatalf("truncated marker must ride the result metadata, got %v", out.Result.Metadata)
		}
	})

	t.Run("unstamped node has no truncated key", func(t *testing.T) {
		t.Parallel()
		task := &models.Task{
			TaskID:    "task-plain",
			AgentType: models.AgentType("ares/plan"),
			Payload: map[string]any{
				argMetadataPrefix + answerContentKey: "final answer",
			},
		}
		out, err := c.ExecuteStep(context.Background(), task)
		if err != nil {
			t.Fatalf("ExecuteStep: %v", err)
		}
		if _, exists := out.Result.Metadata[answerTruncatedKey]; exists {
			t.Fatal("a normally-completed session must NOT carry the truncated marker")
		}
	})

	t.Run("false marker is not copied", func(t *testing.T) {
		t.Parallel()
		task := &models.Task{
			TaskID:    "task-false",
			AgentType: models.AgentType("ares/plan"),
			Payload: map[string]any{
				argMetadataPrefix + answerContentKey:   "final answer",
				argMetadataPrefix + answerTruncatedKey: false,
			},
		}
		out, err := c.ExecuteStep(context.Background(), task)
		if err != nil {
			t.Fatalf("ExecuteStep: %v", err)
		}
		if _, exists := out.Result.Metadata[answerTruncatedKey]; exists {
			t.Fatal("explicit false must not surface as a truncation marker")
		}
	})
}

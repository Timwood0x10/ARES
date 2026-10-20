package agentfabric

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// wirePayload builds an answer node's payload the way the GRAPH builds one:
// through argsMetadata, which the L2 graph uses to store node args and which
// stringifies every value (the engine's node metadata is a map[string]string).
//
// Building the payload by hand is what let the depth-capped tier be stamped by
// every session and read by none: the reader asserted .(bool) against a wire that
// only ever carries the JSON text "true". A test that hand-writes a raw bool
// therefore proves nothing about production, so every case below goes through the
// real encoder — and one case goes through it explicitly as the counter-example.
func wirePayload(args map[string]any) map[string]any {
	md := argsMetadata(args)
	out := make(map[string]any, len(md))
	for k, v := range md {
		out[k] = v
	}
	return out
}

// TestAnswerCognitionTruncationMetadata pins the E4 contract: a terminal answer
// node stamped with the depth-guard truncation marker carries truncated=true in
// its result metadata; an unstamped node must not sprout the key.
func TestAnswerCognitionTruncationMetadata(t *testing.T) {
	t.Parallel()

	c := &answerCognition{}

	t.Run("stamped node carries truncated=true", func(t *testing.T) {
		t.Parallel()
		task := &models.Task{
			TaskID:    "task-stamped",
			AgentType: models.AgentType("ares/plan"),
			Payload: wirePayload(map[string]any{
				answerContentKey:   "partial answer",
				answerTruncatedKey: true,
			}),
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
			Payload: wirePayload(map[string]any{
				answerContentKey: "final answer",
			}),
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
			Payload: wirePayload(map[string]any{
				answerContentKey:   "final answer",
				answerTruncatedKey: false,
			}),
		}
		out, err := c.ExecuteStep(context.Background(), task)
		if err != nil {
			t.Fatalf("ExecuteStep: %v", err)
		}
		if _, exists := out.Result.Metadata[answerTruncatedKey]; exists {
			t.Fatal("explicit false must not surface as a truncation marker")
		}
	})

	t.Run("in-memory bool is read too", func(t *testing.T) {
		t.Parallel()
		// The other writer shape: a caller that builds a node's args in memory
		// (the graph is not the only producer the cognition can be handed). The
		// fact is the same, so both encodings must read the same way.
		task := &models.Task{
			TaskID:    "task-bool",
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
		if out.Result.Metadata[answerTruncatedKey] != true {
			t.Fatalf("a bool-valued marker must be read as truncated, got %v", out.Result.Metadata)
		}
	})

	t.Run("wire TRUE text is not the only spelling accepted", func(t *testing.T) {
		t.Parallel()
		// Guard against a tolerant parse growing into a second vocabulary: only
		// the exact rendering stringify produces counts as true.
		task := &models.Task{
			TaskID:    "task-text",
			AgentType: models.AgentType("ares/plan"),
			Payload: map[string]any{
				argMetadataPrefix + answerContentKey:   "final answer",
				argMetadataPrefix + answerTruncatedKey: "TRUE",
			},
		}
		out, err := c.ExecuteStep(context.Background(), task)
		if err != nil {
			t.Fatalf("ExecuteStep: %v", err)
		}
		if _, exists := out.Result.Metadata[answerTruncatedKey]; exists {
			t.Fatal("a non-canonical spelling must not be promoted to a truncation marker")
		}
	})
}

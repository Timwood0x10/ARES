package postgres

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/errors"
)

// TestEmbeddingQueueRejectsEmptyTaskID verifies the REVIEW #13 contract:
// Enqueue/EnqueueTx reject tasks whose TaskID is not the source row id.
// The validation runs before any database access, so a nil pool is safe here.
func TestEmbeddingQueueRejectsEmptyTaskID(t *testing.T) {
	q := NewEmbeddingQueue(nil, nil)
	ctx := context.Background()

	err := q.Enqueue(ctx, &EmbeddingTask{TaskID: "", Table: "knowledge_chunks_1024", Content: "x", TenantID: "t"})
	if err == nil {
		t.Fatal("Enqueue with empty TaskID must fail")
	}
	if !stderrors.Is(err, errors.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}

	err = q.Enqueue(ctx, nil)
	if err == nil {
		t.Fatal("Enqueue with nil task must fail")
	}
}

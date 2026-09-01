package postgres

import (
	"context"
	stderrors "errors"
	"strings"
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

// TestDedupeKey_OneRowPerSourceRow locks the invariant every Mark* statement
// depends on: dedupe_key is the only UNIQUE constraint on embedding_queue, while
// MarkProcessing/MarkCompleted/MarkFailed address rows by `WHERE task_id = $1`.
// If anything that varies per attempt (content, model, version, spec hash) fed
// the key, one task_id could own several queue rows and a single Mark* call
// would rewrite all of them.
func TestDedupeKey_OneRowPerSourceRow(t *testing.T) {
	q := NewEmbeddingQueue(nil, nil)

	base := &EmbeddingTask{
		TaskID:   "11111111-1111-1111-1111-111111111111",
		Table:    "experiences_1024",
		Content:  "original content",
		TenantID: "tenant-a",
		Model:    "qwen3-embedding:0.6b",
		Version:  1,
		SpecHash: "spec-hash-a",
	}
	want := q.generateDedupeKey(base)

	variants := map[string]*EmbeddingTask{
		"content edited":  {TaskID: base.TaskID, Table: base.Table, Content: "rewritten", TenantID: base.TenantID, Model: base.Model, Version: base.Version, SpecHash: base.SpecHash},
		"model swapped":   {TaskID: base.TaskID, Table: base.Table, Content: base.Content, TenantID: base.TenantID, Model: "other-model", Version: base.Version, SpecHash: base.SpecHash},
		"version bumped":  {TaskID: base.TaskID, Table: base.Table, Content: base.Content, TenantID: base.TenantID, Model: base.Model, Version: 2, SpecHash: base.SpecHash},
		"spec hash moved": {TaskID: base.TaskID, Table: base.Table, Content: base.Content, TenantID: base.TenantID, Model: base.Model, Version: base.Version, SpecHash: "spec-hash-b"},
		"spec hash empty": {TaskID: base.TaskID, Table: base.Table, Content: base.Content, TenantID: base.TenantID, Model: base.Model, Version: base.Version},
	}
	for name, v := range variants {
		if got := q.generateDedupeKey(v); got != want {
			t.Errorf("%s changed the dedupe key: got %s, want %s", name, got, want)
		}
	}
}

// TestDedupeKey_SeparatesDistinctRows is the other half of the invariant: rows
// that must each get their own vector must not collide. Keying only on content
// once made "this content is never embedded twice, ever" the semantics, which
// left later rows with the same content permanently unembedded.
func TestDedupeKey_SeparatesDistinctRows(t *testing.T) {
	q := NewEmbeddingQueue(nil, nil)

	same := "identical content in two places"
	rowA := &EmbeddingTask{TaskID: "row-a", Table: "experiences_1024", Content: same, TenantID: "tenant-a"}
	cases := map[string]*EmbeddingTask{
		"different row id": {TaskID: "row-b", Table: "experiences_1024", Content: same, TenantID: "tenant-a"},
		"different table":  {TaskID: "row-a", Table: "knowledge_chunks_1024", Content: same, TenantID: "tenant-a"},
		"different tenant": {TaskID: "row-a", Table: "experiences_1024", Content: same, TenantID: "tenant-b"},
	}

	keyA := q.generateDedupeKey(rowA)
	for name, other := range cases {
		if q.generateDedupeKey(other) == keyA {
			t.Errorf("%s collided with the base row's dedupe key", name)
		}
	}
}

// TestEnqueueSQL_RevivesOnlyCompletedEntries pins the conflict handling.
// Reviving a stale 'completed' entry is what lets an edited row be re-embedded
// under the one-row-per-source-row key; touching a 'pending'/'processing' entry
// would yank a task out from under the worker holding it.
func TestEnqueueSQL_RevivesOnlyCompletedEntries(t *testing.T) {
	if !strings.Contains(enqueueSQL, "ON CONFLICT (dedupe_key) DO UPDATE") {
		t.Fatal("enqueue must revive a stale entry rather than DO NOTHING")
	}
	if !strings.Contains(enqueueSQL, "WHERE embedding_queue.status = 'completed'") {
		t.Error("revive must be restricted to completed entries")
	}
	for _, reset := range []string{"status = 'pending'", "retry_count = 0", "error_message = NULL"} {
		if !strings.Contains(enqueueSQL, reset) {
			t.Errorf("revive must reset %q", reset)
		}
	}
}

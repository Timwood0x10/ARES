package postgres

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/errors"
)

// TestMarkStatementsScopeBySourceRowIdentity locks the S-9 contract: every
// Mark* statement addresses a queue row by its source-row identity
// (table_name, task_id) — the two fixed components of dedupe_key (see
// generateDedupeKey). A task_id-only WHERE would rewrite every table's entry
// for that id. SQL-text assertion per DEVELOPMENT_PLAN §5; MarkFailed's
// transactional statements are covered behaviorally by the PG integration
// suite (tests/integration/storage_test.go) plus the guard test below.
func TestMarkStatementsScopeBySourceRowIdentity(t *testing.T) {
	for name, q := range map[string]string{
		"markProcessingSQL": markProcessingSQL,
		"markCompletedSQL":  markCompletedSQL,
	} {
		if !strings.Contains(q, "table_name = $") {
			t.Fatalf("%s must scope by table_name, got:\n%s", name, q)
		}
		if !strings.Contains(q, "task_id = $") {
			t.Fatalf("%s must address the row by task_id, got:\n%s", name, q)
		}
	}
}

// TestMarkRejectsEmptyScope verifies the validation guards fire before any
// database access, so a nil pool is safe here (same pattern as
// TestEmbeddingQueueRejectsEmptyTaskID).
func TestMarkRejectsEmptyScope(t *testing.T) {
	q := NewEmbeddingQueue(nil, nil)
	ctx := context.Background()

	cases := []struct {
		name string
		run  func() error
	}{
		{"processing_empty_table", func() error {
			return q.MarkProcessing(ctx, "", "task-1")
		}},
		{"processing_empty_task", func() error {
			return q.MarkProcessing(ctx, "knowledge_chunks_1024", "")
		}},
		{"completed_empty_table", func() error {
			return q.MarkCompleted(ctx, "", "task-1")
		}},
		{"completed_empty_task", func() error {
			return q.MarkCompleted(ctx, "knowledge_chunks_1024", "")
		}},
		{"failed_empty_table", func() error {
			return q.MarkFailed(ctx, "", "task-1", "boom")
		}},
		{"failed_empty_task", func() error {
			return q.MarkFailed(ctx, "knowledge_chunks_1024", "", "boom")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("Mark* with an empty scope must fail, got nil error")
			}
			if !stderrors.Is(err, errors.ErrInvalidArgument) {
				t.Fatalf("Mark* empty scope error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

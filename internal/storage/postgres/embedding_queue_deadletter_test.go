package postgres

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/errors"
)

// TestReplayDeadLettersRejectsNonPositiveLimit pins the guard: a non-positive
// batch would either do nothing (silently disabling the recovery path) or, if it
// reached the database as LIMIT 0/NULL, take an unbounded meaning. Validation
// runs before any database access, so a nil pool is safe here.
func TestReplayDeadLettersRejectsNonPositiveLimit(t *testing.T) {
	q := NewEmbeddingQueue(nil, nil)
	ctx := context.Background()

	for _, limit := range []int{0, -1} {
		_, err := q.ReplayDeadLetters(ctx, limit)
		if !stderrors.Is(err, errors.ErrInvalidArgument) {
			t.Fatalf("limit %d: err = %v, want ErrInvalidArgument", limit, err)
		}
	}
}

// TestReplayDeadLettersSQLIsSingleShot locks the D1/P1-4 contract inside the
// statement, because these are the properties that keep the automatic replay
// from becoming a retry loop or from double-queueing content:
//
//   - only never-replayed rows may be picked, and the stamp must be written back
//     in the same statement (the marker is what bounds the replay to one attempt
//     per piece of content, ever);
//   - content already sitting in the queue must be skipped, or a Reconcile pass
//     plus a replay would put the same row in flight twice;
//   - the batch must be bounded.
func TestReplayDeadLettersSQLIsSingleShot(t *testing.T) {
	required := []struct {
		why  string
		want string
	}{
		{"only never-replayed rows are picked", "d.auto_replayed = FALSE"},
		{"the stamp is written back in the same statement", "SET auto_replayed = TRUE"},
		{"content already queued is skipped", "NOT EXISTS"},
		{"the batch is bounded", "LIMIT $1"},
		{"the re-queued row carries the stamp forward", "'pending', NOW(), TRUE"},
	}
	for _, r := range required {
		if !strings.Contains(replayDeadLettersSQL, r.want) {
			t.Errorf("replayDeadLettersSQL must %s (%q missing):\n%s", r.why, r.want, replayDeadLettersSQL)
		}
	}
}

// TestDeadLetterInsertCarriesTheReplayStamp locks the loop-breaker: when a
// replayed task fails again, its new dead-letter row must inherit auto_replayed
// from the queue row. If it were reset to FALSE, the reconciler would replay the
// same unembeddable content on every tick, forever.
func TestDeadLetterInsertCarriesTheReplayStamp(t *testing.T) {
	if !strings.Contains(deadLetterInsertSQL, "auto_replayed)") {
		t.Fatalf("dead-letter insert must carry auto_replayed into the dead-letter row:\n%s", deadLetterInsertSQL)
	}
	// Copied from the queue row, never a literal: a constant would either lose
	// the stamp or invent one.
	if !strings.Contains(deadLetterInsertSQL, "queued_at, auto_replayed") {
		t.Errorf("dead-letter insert must SELECT auto_replayed from the queue row:\n%s", deadLetterInsertSQL)
	}
}

// TestDeadLetterBacklogTotal pins the number the alarm reports: both halves are
// content without an embedding, so the total is what retrieval cannot see.
func TestDeadLetterBacklogTotal(t *testing.T) {
	backlog := DeadLetterBacklog{Pending: 3, GivenUp: 2}
	if got := backlog.Total(); got != 5 {
		t.Errorf("Total() = %d, want 5", got)
	}
	if got := (DeadLetterBacklog{}).Total(); got != 0 {
		t.Errorf("empty Total() = %d, want 0", got)
	}
}

// TestDeadLetterBacklogSQLSplitsByActionability keeps the alarm actionable: rows
// already given their automatic retry must not be counted as pending, or the
// number stops telling an operator whether waiting will help.
func TestDeadLetterBacklogSQLSplitsByActionability(t *testing.T) {
	if !strings.Contains(deadLetterBacklogSQL, "auto_replayed = FALSE") ||
		!strings.Contains(deadLetterBacklogSQL, "auto_replayed = TRUE") {
		t.Fatalf("backlog query must split pending from given-up:\n%s", deadLetterBacklogSQL)
	}
}

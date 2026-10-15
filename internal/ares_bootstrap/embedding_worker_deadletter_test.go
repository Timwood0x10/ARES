package ares_bootstrap

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// captureLogger returns a logger writing into buf, so the dead-letter alarm can
// be asserted as behaviour rather than assumed.
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

// TestReplayEmbeddingDeadLetters_UsesTheConfiguredBatch locks the bound: one tick
// may only replay a batch, because an outage dead-letters everything at once and
// stamping the whole backlog in one tick would spend the embedding quota in a
// burst while the provider may still be down.
func TestReplayEmbeddingDeadLetters_UsesTheConfiguredBatch(t *testing.T) {
	queue := &fakeQueue{replayedRows: 3}
	replayEmbeddingDeadLetters(context.Background(), queue, 25, captureLogger(&bytes.Buffer{}))

	if len(queue.replayCalls) != 1 {
		t.Fatalf("replay calls = %d, want 1", len(queue.replayCalls))
	}
	if queue.replayCalls[0] != 25 {
		t.Errorf("replay limit = %d, want the configured batch 25", queue.replayCalls[0])
	}
}

// TestReplayEmbeddingDeadLetters_FailureDoesNotStopTheTick pins the failure
// handling: a replay that cannot reach the database must not skip the report, or
// the tick would go silent about the very backlog it failed to drain.
func TestReplayEmbeddingDeadLetters_FailureDoesNotStopTheTick(t *testing.T) {
	var buf bytes.Buffer
	logger := captureLogger(&buf)
	queue := &fakeQueue{replayErr: errors.New("db down"), backlog: postgres.DeadLetterBacklog{Pending: 2}}

	replayEmbeddingDeadLetters(context.Background(), queue, 10, logger)
	var reporter deadLetterReporter
	reporter.report(context.Background(), queue, 30*24*time.Hour, logger)

	out := buf.String()
	if !strings.Contains(out, "dead-letter replay failed") {
		t.Errorf("a failed replay must be reported, got: %s", out)
	}
	if !strings.Contains(out, "invisible to retrieval") {
		t.Errorf("the backlog report must still run after a replay failure, got: %s", out)
	}
}

// TestDeadLetterReporter_ReportsInvisibleContent is the alarm the defect was
// missing: dead-lettered content has a NULL embedding, so retrieval skips it
// without an error. A non-zero backlog has to say so, and has to split "waiting
// for its automatic retry" from "needs a human".
func TestDeadLetterReporter_ReportsInvisibleContent(t *testing.T) {
	var buf bytes.Buffer
	queue := &fakeQueue{backlog: postgres.DeadLetterBacklog{Pending: 3, GivenUp: 2}}

	var reporter deadLetterReporter
	reporter.report(context.Background(), queue, 30*24*time.Hour, captureLogger(&buf))

	out := buf.String()
	for _, want := range []string{
		"invisible to retrieval",
		"total=5",
		"pending_replay=3",
		"given_up=2",
		"level=WARN",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("backlog report missing %q, got: %s", want, out)
		}
	}
}

// TestDeadLetterReporter_QuietWhenEmpty keeps the alarm worth reading: an empty
// backlog must produce no line at all, or operators learn to ignore the one that
// matters.
func TestDeadLetterReporter_QuietWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	var reporter deadLetterReporter
	reporter.report(context.Background(), &fakeQueue{}, time.Hour, captureLogger(&buf))

	if buf.Len() != 0 {
		t.Errorf("an empty backlog must be silent, got: %s", buf.String())
	}
}

// TestDeadLetterReporter_ReportsOncePerState is the anti-noise contract: a backlog
// that does not move must be said once, not once per tick (the default interval
// would make a permanently unembeddable row emit ~144 identical warnings a day),
// while any change — growth, shrinkage, or the count starting to fail — speaks up
// again.
func TestDeadLetterReporter_ReportsOncePerState(t *testing.T) {
	queue := &fakeQueue{backlog: postgres.DeadLetterBacklog{Pending: 2}}
	var reporter deadLetterReporter

	var first bytes.Buffer
	reporter.report(context.Background(), queue, time.Hour, captureLogger(&first))
	if !strings.Contains(first.String(), "invisible to retrieval") {
		t.Fatalf("the first sighting must be reported, got: %s", first.String())
	}

	var unchanged bytes.Buffer
	reporter.report(context.Background(), queue, time.Hour, captureLogger(&unchanged))
	if unchanged.Len() != 0 {
		t.Errorf("an unchanged backlog must not be re-reported, got: %s", unchanged.String())
	}

	// The backlog grew: that is news.
	queue.backlog = postgres.DeadLetterBacklog{Pending: 3}
	var grown bytes.Buffer
	reporter.report(context.Background(), queue, time.Hour, captureLogger(&grown))
	if !strings.Contains(grown.String(), "pending_replay=3") {
		t.Errorf("a changed backlog must be reported again, got: %s", grown.String())
	}

	// The count starts failing: also news, and not silence.
	queue.backlogErr = errors.New("db down")
	var failing bytes.Buffer
	reporter.report(context.Background(), queue, time.Hour, captureLogger(&failing))
	if !strings.Contains(failing.String(), "dead-letter count failed") {
		t.Errorf("a failing count must be reported, got: %s", failing.String())
	}
}

// TestDeadLetterReporter_CountFailureIsReported pins that a failed count is not
// mistaken for an empty backlog: "we could not look" and "there is nothing there"
// are different facts, and only one of them is safe to ignore.
func TestDeadLetterReporter_CountFailureIsReported(t *testing.T) {
	var buf bytes.Buffer
	queue := &fakeQueue{backlogErr: errors.New("db down")}

	var reporter deadLetterReporter
	reporter.report(context.Background(), queue, time.Hour, captureLogger(&buf))

	if !strings.Contains(buf.String(), "dead-letter count failed") {
		t.Errorf("a failed count must be reported, got: %s", buf.String())
	}
}

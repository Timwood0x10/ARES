package taskfabric

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// TestClassifyFailure locks the retry decision that Fail applies and stamps onto
// the task.failed event.
//
// It pins BOTH halves of T-F1 and that is the point: the rows that must become
// terminal (a known permanent cause nobody remembered to mark) and the rows that
// must NOT move (anything a retry could plausibly outlast). A table that only
// grew the first half would silently trade "wasted retries" for "tasks killed
// while they could still have succeeded".
func TestClassifyFailure(t *testing.T) {
	domainErr := errors.New("tool exploded")

	tests := []struct {
		name  string
		cause error
		want  bool
	}{
		{name: "nil cause keeps the legacy retry", cause: nil, want: true},
		{name: "unclassified error keeps the legacy retry", cause: domainErr, want: true},
		{name: "context deadline is a slow call, not a verdict", cause: context.DeadlineExceeded, want: true},
		{name: "wrapped context deadline stays retryable", cause: fmt.Errorf("llm step: %w", context.DeadlineExceeded), want: true},
		{name: "task deadline sentinel is terminal", cause: ErrTaskDeadlineExceeded, want: false},
		{name: "wrapped task deadline sentinel is terminal", cause: fmt.Errorf("sweep: %w", ErrTaskDeadlineExceeded), want: false},
		{name: "permanent marker overrides the default", cause: MarkPermanent(domainErr), want: false},
		{name: "permanent marker overrides the deadline sentinel", cause: MarkPermanent(ErrTaskDeadlineExceeded), want: false},
		{name: "retryable marker overrides the deadline sentinel", cause: MarkRetryable(ErrTaskDeadlineExceeded), want: true},
		{name: "retryable marker survives wrapping", cause: fmt.Errorf("bridge: %w", MarkRetryable(errors.New("upstream 503"))), want: true},

		// --- T-F1: known permanent causes are terminal even unmarked ---------
		{name: "not-found family is terminal", cause: fmt.Errorf("run step: %w", apperrors.ErrNotFound), want: false},
		{name: "tool-not-found is terminal", cause: apperrors.ErrToolNotFound, want: false},
		{name: "record-not-found is terminal", cause: fmt.Errorf("load: %w", apperrors.ErrRecordNotFound), want: false},
		{name: "workflow-not-found is terminal", cause: apperrors.ErrWorkflowNotFound, want: false},
		{name: "invalid argument is terminal", cause: apperrors.ErrInvalidArgument, want: false},
		{name: "invalid input is terminal", cause: apperrors.ErrInvalidInput, want: false},
		{name: "invalid config is terminal", cause: apperrors.ErrInvalidConfig, want: false},
		{name: "access denied is terminal", cause: apperrors.ErrAccessDenied, want: false},
		{name: "already exists is terminal", cause: apperrors.ErrAlreadyExists, want: false},
		{name: "dependency cycle is terminal", cause: apperrors.ErrDependencyCycle, want: false},
		{name: "max retries exceeded is terminal", cause: apperrors.ErrMaxRetriesExceeded, want: false},
		{name: "a retryable marker still beats a permanent sentinel", cause: MarkRetryable(apperrors.ErrNotFound), want: true},

		// --- and the half that must NOT move ---------------------------------
		{name: "timeout stays retryable", cause: apperrors.ErrTimeout, want: true},
		{name: "llm timeout stays retryable", cause: apperrors.ErrLLMTimeout, want: true},
		{name: "service unavailable stays retryable", cause: apperrors.ErrServiceUnavailable, want: true},
		{name: "rate limit stays retryable", cause: apperrors.ErrRateLimitExceeded, want: true},
		{name: "llm quota stays retryable", cause: apperrors.ErrLLMQuotaExceeded, want: true},
		{name: "open circuit breaker stays retryable", cause: apperrors.ErrCircuitBreakerOpen, want: true},
		{name: "db connection failure stays retryable", cause: apperrors.ErrDBConnectionFailed, want: true},
		{name: "queue full stays retryable", cause: apperrors.ErrQueueFull, want: true},
		{name: "backpressure stays retryable", cause: apperrors.ErrBackpressureTriggered, want: true},
		{name: "invalid state stays retryable (it may change)", cause: apperrors.ErrInvalidState, want: true},
		{name: "expired secret stays retryable (it may be rotated)", cause: apperrors.ErrSecretExpired, want: true},
		{name: "llm validation failure stays retryable (output may differ)", cause: apperrors.ErrLLMValidationFailed, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyFailure(tc.cause))
		})
	}
}

// TestFailureMarkersPreserveCause pins the wrapping contract: a marker must not
// hide the original error from errors.Is/As, nor rewrite the operator-facing
// message (the checkpoint's LastError is read from it).
func TestFailureMarkersPreserveCause(t *testing.T) {
	sentinel := errors.New("boom")

	markers := []struct {
		name string
		fn   func(error) error
	}{
		{name: "MarkPermanent", fn: MarkPermanent},
		{name: "MarkRetryable", fn: MarkRetryable},
	}

	for _, m := range markers {
		t.Run(m.name, func(t *testing.T) {
			wrapped := m.fn(fmt.Errorf("tool x: %w", sentinel))
			require.ErrorIs(t, wrapped, sentinel, "markers must keep the cause reachable")
			assert.Equal(t, "tool x: boom", wrapped.Error(), "markers must not rewrite the message")
		})
	}
}

// TestFailureMarkersIgnoreNil documents the unconditional-wrap convenience: a
// call site may wrap without checking, and nil stays nil.
func TestFailureMarkersIgnoreNil(t *testing.T) {
	assert.NoError(t, MarkPermanent(nil))
	assert.NoError(t, MarkRetryable(nil))
}

// TestFailureVisibilityReason pins WHEN the fabric speaks up about a failure
// that is spending retry budget, which is the other half of T-F1: recognising a
// permanent cause saves the budget, and saying so when the budget is being spent
// is what keeps the remaining silent case findable.
func TestFailureVisibilityReason(t *testing.T) {
	tests := []struct {
		name       string
		attempts   int
		maxRetries int
		retryable  bool
		terminal   bool
		replyTask  bool
		wantEmpty  bool
	}{
		{name: "first attempt of a retryable cause stays quiet", attempts: 1, maxRetries: 3, retryable: true, wantEmpty: true},
		{name: "second attempt of an unclassified cause speaks up", attempts: 2, maxRetries: 3, retryable: true},
		{name: "exhausted budget on an unclassified cause speaks up", attempts: 3, maxRetries: 3, retryable: true},
		{name: "a classified terminal cause stays quiet — no budget was spent", attempts: 2, maxRetries: 3, retryable: false, wantEmpty: true},

		// A lost collaboration answer must be said even on a path that would
		// otherwise stay quiet: the asker is waiting for something nothing else in
		// the system knows is gone (defect I1's second half).
		{name: "a reply task failing terminally speaks even with no budget spent", attempts: 1, maxRetries: 1, retryable: false, terminal: true, replyTask: true},
		{name: "a reply task about to be retried stays quiet — the answer is still coming", attempts: 1, maxRetries: 3, retryable: true, terminal: false, replyTask: true, wantEmpty: true},
		{name: "an ordinary terminal failure stays quiet", attempts: 1, maxRetries: 1, retryable: false, terminal: true, wantEmpty: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason := failureVisibilityReason(failureContext{
				Attempts:   tc.attempts,
				MaxRetries: tc.maxRetries,
				Retryable:  tc.retryable,
				Terminal:   tc.terminal,
				ReplyTask:  tc.replyTask,
			})
			if tc.wantEmpty {
				assert.Empty(t, reason)
				return
			}
			assert.NotEmpty(t, reason)
		})
	}
}

// TestCarriesCollaborationReply pins the recognition the fabric needs to say the line
// above: the marker is read from the task's checkpoint payload, and a task with no
// checkpoint (or no payload) is simply not a reply task.
func TestCarriesCollaborationReply(t *testing.T) {
	reply := &Task{ID: "ask-reply-1", Checkpoint: NewCheckpointEnvelope(map[string]any{
		PayloadKeyCollaborationReply: map[string]any{"from": "agent-b"},
	})}
	assert.True(t, carriesCollaborationReply(reply))

	assert.False(t, carriesCollaborationReply(&Task{ID: "t", Checkpoint: NewCheckpointEnvelope(map[string]any{"input": "x"})}))
	assert.False(t, carriesCollaborationReply(&Task{ID: "t"}))
	assert.False(t, carriesCollaborationReply(nil))
}

// TestFailKnownPermanentCauseSkipsTheRetryBudget is the T-F1 contract end to end:
// a cause whose CLASS cannot be fixed by waiting must fail the task terminally
// even though the producing layer never called MarkPermanent, while an
// unclassified cause of the same shape must still requeue. Both halves in one
// test on purpose — the failure mode of this fix is over-classification, and a
// test that only proved the first half would not notice it.
func TestFailKnownPermanentCauseSkipsTheRetryBudget(t *testing.T) {
	f := NewFabric()
	tk := &Task{ID: "t-perm", Capability: "rust", RetryPolicy: RetryPolicy{MaxRetries: 3}}
	require.NoError(t, f.Create(tk))
	epoch, err := f.Acquire("t-perm", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("t-perm", "agent-a", epoch))

	require.NoError(t, f.Fail("t-perm", "agent-a", epoch,
		fmt.Errorf("dispatch tool: %w", apperrors.ErrToolNotFound)))

	got, err := f.Task("t-perm")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, got.State, "a known permanent cause must go terminal with budget left")
	assert.Equal(t, 1, got.RetryPolicy.Attempts, "exactly one attempt may be spent")
	assert.False(t, got.Requeued, "a terminal failure is never requeued")

	// Control: the same shape of failure, unclassified, keeps the legacy retry.
	control := NewFabric()
	ctk := &Task{ID: "t-unclassified", Capability: "rust", RetryPolicy: RetryPolicy{MaxRetries: 3}}
	require.NoError(t, control.Create(ctk))
	cep, err := control.Acquire("t-unclassified", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, control.Start("t-unclassified", "agent-a", cep))
	require.NoError(t, control.Fail("t-unclassified", "agent-a", cep, errors.New("flaky upstream")))

	cgot, err := control.Task("t-unclassified")
	require.NoError(t, err)
	assert.Equal(t, StateReady, cgot.State, "an unclassified cause must keep the legacy retry")
	assert.True(t, cgot.Requeued)
}

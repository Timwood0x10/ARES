package taskfabric

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassifyFailure locks the retry decision that Fail applies and stamps onto
// the task.failed event. The compatibility-critical rows are the first four:
// anything unclassified — including a bare context deadline from one slow call —
// must keep the legacy retry behaviour, so an upgrade can never turn a cause
// that used to be retried into a terminal one.
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

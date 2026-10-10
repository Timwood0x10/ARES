package taskfabric

import "errors"

// payloadKeyRetryable is the task.failed payload key carrying the retry
// classification that produced the transition. It is an event-only annotation:
// the durable event is the only place it needs to live, so it deliberately has
// no restore key (nothing folds it back into a task).
const payloadKeyRetryable = "retryable"

// ErrTaskDeadlineExceeded is the sentinel for a task that passed its absolute
// Deadline (see ExpireDeadlines). It is the only deadline-shaped error the
// fabric treats as terminal: a bare context.DeadlineExceeded from a single LLM
// or tool call is usually a transient timeout, so it keeps the legacy retry
// behaviour — classifying it as permanent would silently retire retry budget
// that used to be spent on recoverable slowness.
var ErrTaskDeadlineExceeded = errors.New("task deadline exceeded")

// permanentError marks a failure that must not be retried even when retry
// budget remains. It is created by MarkPermanent and never escapes the fabric:
// callers reach it through errors.As inside classifyFailure.
type permanentError struct{ err error }

// Error implements error.
func (e *permanentError) Error() string { return e.err.Error() }

// Unwrap exposes the wrapped cause so errors.Is/errors.As keep working through
// the marker.
func (e *permanentError) Unwrap() error { return e.err }

// retryableError marks a failure as worth retrying even when the error type
// alone would not say so (e.g. an upstream 503 wrapped into a domain error). It
// is created by MarkRetryable.
type retryableError struct{ err error }

// Error implements error.
func (e *retryableError) Error() string { return e.err.Error() }

// Unwrap exposes the wrapped cause so errors.Is/errors.As keep working through
// the marker.
func (e *retryableError) Unwrap() error { return e.err }

// MarkPermanent wraps err so the fabric fails the task terminally on this cause,
// whether or not retry budget remains. Use it when the producing layer knows
// retrying cannot help (invalid input, unsupported tool, missing permission).
// A nil err returns nil so call sites can wrap unconditionally.
func MarkPermanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// MarkRetryable wraps err so the fabric retries this cause even if a broader
// classification would call it terminal. Use it when the producing layer knows
// the failure is transient (a retryable upstream status). A nil err returns nil
// so call sites can wrap unconditionally.
func MarkRetryable(err error) error {
	if err == nil {
		return nil
	}
	return &retryableError{err: err}
}

// classifyFailure reports whether a failed execution is worth retrying. It is
// the single decision point behind Fail's requeue and behind the retryable bit
// on the task.failed event.
//
// Precedence and why:
//  1. An explicit marker wins — the layer that produced the error knows more
//     than any table here (MarkPermanent / MarkRetryable).
//  2. ErrTaskDeadlineExceeded is terminal — a task past its absolute deadline
//     must not burn the remaining retry budget.
//  3. Everything else is retryable, which is exactly 0.3.2's behaviour: no
//     cause that used to be retried becomes terminal merely because it is
//     unclassified, and a bare context.DeadlineExceeded stays retryable.
func classifyFailure(cause error) bool {
	if cause == nil {
		// Operator kills and recovery sweeps pass no cause; those paths have
		// always retried within budget.
		return true
	}
	var perm *permanentError
	if errors.As(cause, &perm) {
		return false
	}
	var retry *retryableError
	if errors.As(cause, &retry) {
		return true
	}
	if errors.Is(cause, ErrTaskDeadlineExceeded) {
		return false
	}
	return true
}

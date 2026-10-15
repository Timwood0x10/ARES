package taskfabric

import (
	"errors"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

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

// permanentCauses are the sentinel errors that provably cannot be fixed by
// running the same step again: the input or the referenced entity is wrong, and
// waiting cannot make it right.
//
// They are the SECOND net behind MarkPermanent (defect T-F1). The marker
// contract puts the whole burden on the producing layer, so one forgotten wrap
// used to mean a permanent error burning the entire retry budget, backoff
// included, before failing the way it would have failed immediately.
//
// The list is deliberately NARROW. Everything a retry could plausibly outlast —
// timeouts, unavailability, rate limits, quota, an open circuit breaker,
// connection failures, queue pressure, an invalid STATE (which the next attempt
// may find changed) and an expired secret (which may be rotated) — stays
// retryable, and TestClassifyFailure pins that half as hard as this one.
// Over-classifying here would trade a wasted retry for a task killed while it
// could still have succeeded, which is the more expensive mistake.
//
// Matching goes through errors.Is against the shared sentinels, never string
// matching: ErrToolNotFound, ErrRecordNotFound, ErrAgentNotFound and
// ErrWorkflowNotFound all wrap ErrNotFound, so one entry covers the whole
// "referenced something that does not exist" family the defect names.
var permanentCauses = []error{
	apperrors.ErrNotFound,           // a tool, entity or workflow the task references does not exist
	apperrors.ErrInvalidArgument,    // the request itself is malformed (an over-long LLM prompt included)
	apperrors.ErrInvalidInput,       // validation rejected the input
	apperrors.ErrInvalidConfig,      // the configuration is broken, not slow
	apperrors.ErrAccessDenied,       // waiting does not grant permission
	apperrors.ErrAlreadyExists,      // a create that already exists fails identically next time
	apperrors.ErrDependencyCycle,    // a cycle is a property of the graph, not a race to retry
	apperrors.ErrWorkflowCyclicDAG,  // same, for the workflow compiler
	apperrors.ErrMaxRetriesExceeded, // retrying is precisely what this sentinel forbids
}

// isKnownPermanent reports whether cause is one of the provably permanent causes.
func isKnownPermanent(cause error) bool {
	for _, sentinel := range permanentCauses {
		if errors.Is(cause, sentinel) {
			return true
		}
	}
	return false
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
//  3. A KNOWN permanent cause is terminal (T-F1): an error whose class cannot be
//     fixed by waiting must not spend the budget just because its producer
//     forgot to mark it.
//  4. Everything else is retryable, which is exactly 0.3.2's behaviour: no
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
	if isKnownPermanent(cause) {
		return false
	}
	return true
}

// retryEscalationAttempts is the attempt count at which an unclassified cause
// stops being "one of those things" and earns a log line (see
// failureVisibilityReason). Two, because it is the first count that proves the
// retry did not help: attempt 1 is the only attempt that has not yet been shown
// to be useless.
const retryEscalationAttempts = 2

// failureVisibilityReason turns "how this attempt ended" into the sentence an
// operator needs, or "" when nothing needs saying.
//
// It exists because T-F1 was SILENT: a permanent error nobody marked simply
// burned the budget, and the only trace was that the task eventually failed in
// the way it could have failed at once. Saying it while the budget is being
// spent is the difference between a defect and a mystery. retryable=false is
// intentionally quiet — the fabric already knows the cause is terminal, no
// budget was spent, and there is nothing to escalate.
func failureVisibilityReason(attempts, maxRetries int, retryable bool) string {
	switch {
	case !retryable:
		return ""
	case attempts >= maxRetries:
		return "retry budget exhausted on an unclassified cause: if this cause can never succeed, the producing layer must wrap it in MarkPermanent"
	case attempts >= retryEscalationAttempts:
		return "unclassified cause still failing after repeated attempts: check whether retrying this cause can ever help"
	default:
		return ""
	}
}

// failureCauseText renders a failure cause for a log line, naming the absent
// case rather than leaving an empty field that reads like a lost error.
func failureCauseText(cause error) string {
	if cause == nil {
		return "none"
	}
	return cause.Error()
}

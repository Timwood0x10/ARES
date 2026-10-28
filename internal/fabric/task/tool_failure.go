package taskfabric

import (
	"context"
	"errors"
	"io/fs"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// ClassifyToolFailure attaches a failure level to an error raised by a tool call.
//
// The tool's retry safety decides what an AMBIGUOUS failure means. A timeout or a dropped
// connection says nothing about whether the write landed: for an idempotent tool the retry is
// harmless, for a non-idempotent one it duplicates the side effect. So the same error is L1 for one
// tool and L0 for the other, and the difference is data the tool author declared — nothing
// downstream can infer it from the error text.
//
// A tool that never declared itself idempotent is treated as NOT safe to retry after an ambiguous
// failure. That is deliberately the strict reading: the alternative retries a write that may have
// already been applied, and a duplicated transfer or file overwrite is not recoverable by trying
// again. Declaring idempotency (BindIdempotentTool) is what buys automatic retries.
//
// Failures that are certainly effect-free keep their natural level regardless of idempotency:
// a rejected request (4xx), a rate limit, or a missing entity never applied anything, so retrying
// cannot duplicate it.
func ClassifyToolFailure(err error, idempotent bool) error {
	if err == nil {
		return nil
	}
	// A cancelled call is the scheduler shutting down, not the tool failing. Attaching a level here
	// would make an orderly shutdown look like a task failure.
	if errors.Is(err, context.Canceled) {
		return err
	}
	// An explicit decision by the producing layer outranks this table.
	if LevelOf(err) != LevelUnclassified {
		return err
	}
	switch {
	case errors.Is(err, apperrors.ErrRateLimitExceeded):
		// The request was refused, so nothing was applied: safe to retry later even for a write.
		return WithLevel(err, LevelThrottled)
	case errors.Is(err, apperrors.ErrNotFound),
		errors.Is(err, apperrors.ErrInvalidArgument),
		errors.Is(err, apperrors.ErrInvalidInput),
		errors.Is(err, apperrors.ErrAccessDenied),
		errors.Is(err, apperrors.ErrAlreadyExists),
		errors.Is(err, fs.ErrNotExist),
		errors.Is(err, fs.ErrPermission):
		// Deterministic for this input: retrying reproduces it, and no effect was applied.
		return WithLevel(err, LevelFatal)
	case isAmbiguousOutcome(err):
		if idempotent {
			return WithLevel(err, LevelTransient)
		}
		// The side effect may already have happened. Stopping is the only safe answer; a human (or
		// the tool's own reconciliation) has to decide, and the recorded class says why.
		return WithLevel(err, LevelFatal)
	}
	// Everything else stays unclassified: retryable, with the task's own policy. Killing here would
	// mean that a tool which merely forgot to declare idempotency stops retrying altogether — and
	// since no builtin declares it today, that would silently remove retries from every read tool
	// on its first transient hiccup. The strict treatment is reserved for the failures that
	// actually leave the outcome undecided (isAmbiguousOutcome), which is where a duplicate is
	// possible at all.
	return err
}

// isAmbiguousOutcome reports whether the failure leaves "did it happen?" undecided.
//
// These are the classes where the request provably left the process — a timeout, a dropped
// connection, an unavailable upstream — and the response never came back. A validation error or a
// rate limit is not in this set because the peer told us it did nothing.
func isAmbiguousOutcome(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, apperrors.ErrServiceUnavailable) ||
		errors.Is(err, apperrors.ErrTimeout) ||
		errors.Is(err, fs.ErrClosed)
}

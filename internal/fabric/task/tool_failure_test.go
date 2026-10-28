package taskfabric

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// TestClassifyToolFailure is the table that decides what a failed tool call means. The two columns
// that matter are the level and the idempotency flag: the same error is retryable for a read and
// fatal for an unmarked write, because only the tool author knows whether a second call is safe.
func TestClassifyToolFailure(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		idempotent bool
		want       Level
	}{
		{"nil stays nil", nil, false, LevelUnclassified},
		{"cancel is not a failure", context.Canceled, false, LevelUnclassified},
		{"missing entity is fatal for a read", apperrors.ErrNotFound, true, LevelFatal},
		{"invalid argument is fatal for a write", apperrors.ErrInvalidArgument, false, LevelFatal},
		{"permission is fatal", fs.ErrPermission, true, LevelFatal},
		{"rate limit is retryable even for a write", apperrors.ErrRateLimitExceeded, false, LevelThrottled},
		{"timeout from an idempotent tool is transient", context.DeadlineExceeded, true, LevelTransient},
		{"timeout from an unmarked write is fatal", context.DeadlineExceeded, false, LevelFatal},
		{"unavailable from an idempotent tool is transient", apperrors.ErrServiceUnavailable, true, LevelTransient},
		{"unavailable from an unmarked write is fatal", apperrors.ErrServiceUnavailable, false, LevelFatal},
		{"unknown cause from an idempotent tool keeps legacy retry", assert.AnError, true, LevelUnclassified},
		{"unknown cause keeps legacy retry even for an unmarked write", assert.AnError, false, LevelUnclassified},
		{"explicit level outranks the table", WithLevel(apperrors.ErrNotFound, LevelThrottled), false, LevelThrottled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyToolFailure(tc.err, tc.idempotent)
			if tc.err == nil {
				assert.NoError(t, got)
				return
			}
			assert.Equal(t, tc.want, LevelOf(got))
		})
	}
}

// TestClassifyToolFailure_KeepsTheCause keeps the message useful: the class is metadata, and the
// operator still needs to see what the tool actually said.
func TestClassifyToolFailure_KeepsTheCause(t *testing.T) {
	cause := errors.New("connection reset by peer")

	got := ClassifyToolFailure(cause, false)

	assert.Contains(t, got.Error(), "connection reset by peer")
	assert.True(t, errors.Is(got, cause), "errors.Is must still reach the original cause")
}

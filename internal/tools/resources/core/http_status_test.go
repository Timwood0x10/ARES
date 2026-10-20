package core

import (
	"errors"
	"net/http"
	"testing"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// TestHTTPStatusError_MapsPermanentClasses pins the half of the mapping that must
// become TERMINAL: retrying these cannot change the outcome, so their sentinels are
// the ones the task fabric's permanent table already contains (pinned separately by
// taskfabric's TestClassifyFailure — the two halves compose into "a 404 does not burn
// the retry budget").
func TestHTTPStatusError_MapsPermanentClasses(t *testing.T) {
	tests := []struct {
		code int
		want error
	}{
		{http.StatusNotFound, apperrors.ErrNotFound},
		{http.StatusBadRequest, apperrors.ErrInvalidArgument},
		{http.StatusUnsupportedMediaType, apperrors.ErrInvalidArgument},
		{http.StatusUnprocessableEntity, apperrors.ErrInvalidArgument},
		{http.StatusUnauthorized, apperrors.ErrAccessDenied},
		{http.StatusForbidden, apperrors.ErrAccessDenied},
		{http.StatusConflict, apperrors.ErrAlreadyExists},
	}
	for _, tc := range tests {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			err := HTTPStatusError(tc.code)
			if err == nil {
				t.Fatalf("status %d must carry a class", tc.code)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("status %d mapped to %v, want it to be %v", tc.code, err, tc.want)
			}
		})
	}
}

// TestHTTPStatusError_MapsRetryableClasses pins the other half: these sentinels are
// deliberately NOT in the permanent table, so the same mapping says "waiting may fix
// this" — a 5xx or a rate limit must keep its retries.
func TestHTTPStatusError_MapsRetryableClasses(t *testing.T) {
	tests := []struct {
		code int
		want error
	}{
		{http.StatusTooManyRequests, apperrors.ErrRateLimitExceeded},
		{http.StatusInternalServerError, apperrors.ErrServiceUnavailable},
		{http.StatusBadGateway, apperrors.ErrServiceUnavailable},
		{http.StatusServiceUnavailable, apperrors.ErrServiceUnavailable},
		{599, apperrors.ErrServiceUnavailable},
	}
	for _, tc := range tests {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			if err := HTTPStatusError(tc.code); !errors.Is(err, tc.want) {
				t.Errorf("status %d mapped to %v, want it to be %v", tc.code, err, tc.want)
			}
		})
	}
}

// TestHTTPStatusError_LeavesUnknownStatusesUnclassified is the conservative arm, and
// the important one: a status nobody thought about (405, 410, 418, a redirect) must
// NOT be guessed into a permanent class. Unclassified means "retried", and the
// existing T-F1 escalation warning reports it once the retry has failed twice —
// over-classifying would trade a wasted retry for a call killed while it could still
// have succeeded, which is the more expensive mistake.
func TestHTTPStatusError_LeavesUnknownStatusesUnclassified(t *testing.T) {
	for _, code := range []int{200, 201, 301, 405, 410, 418, 451} {
		if err := HTTPStatusError(code); err != nil {
			t.Errorf("status %d must stay unclassified, got %v", code, err)
		}
	}
}

// TestNewErrorResultWithCause_KeepsMessageAndClass: the tool-result shape carries both,
// so a model reading the result still sees the message while the scheduler sees the
// class.
func TestNewErrorResultWithCause_KeepsMessageAndClass(t *testing.T) {
	result := NewErrorResultWithCause("SearXNG returned status 404: not found", HTTPStatusError(http.StatusNotFound))
	if result.Success {
		t.Error("a failure result must not report success")
	}
	if result.Error == "" {
		t.Error("the human-readable message must survive")
	}
	if !errors.Is(result.Err, apperrors.ErrNotFound) {
		t.Errorf("the class must ride along, got %v", result.Err)
	}

	// A nil cause degrades to the plain error result rather than an empty Err value.
	plain := NewErrorResultWithCause("boom", nil)
	if plain.Err != nil || plain.Error != "boom" {
		t.Errorf("nil cause must behave like NewErrorResult, got %+v", plain)
	}
}

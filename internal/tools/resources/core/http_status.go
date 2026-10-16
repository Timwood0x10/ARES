package core

import (
	"fmt"
	"net/http"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// HTTPStatusError maps an HTTP status code to the shared sentinel that says whether
// running the same request again can help, or returns nil when no confident mapping
// exists.
//
// It exists because a status code is DATA, not a cause: before this, every non-200
// collapsed into a message string ("SearXNG returned status 404: ...") and the
// failure classification downstream could only see text, so a permanent error (a tool
// or resource that does not exist) was indistinguishable from a transient one and
// burned the retry budget exactly as defect T-F1 describes. Returning the sentinels
// the task fabric already knows means `errors.Is` works on the way up and
// `classifyFailure` needs no change at all.
//
// The mapping is deliberately CONSERVATIVE, and the unmapped arm is the important
// one:
//
//   - 404 not found                 -> permanent: the referenced thing does not exist
//   - 400/415/422 malformed request -> permanent: the request itself is wrong
//   - 401/403 denied                -> permanent: waiting does not grant permission
//   - 409 conflict                  -> permanent: an idempotent create fails the same way
//   - 429 rate limited              -> RETRYABLE: the limit resets
//   - 5xx server error              -> RETRYABLE: the upstream is having a moment
//   - anything else (405, 410, ...) -> nil, i.e. UNCLASSIFIED and therefore retried,
//     with the escalation warning T-F1 already emits once a retry has failed twice
//
// The unmapped arm returning nil rather than a guess is the point: over-classifying
// trades a wasted retry for a call killed while it could still have succeeded, and the
// second mistake is the more expensive one.
func HTTPStatusError(code int) error {
	switch code {
	case http.StatusNotFound:
		return fmt.Errorf("http status %d: %w", code, apperrors.ErrNotFound)
	case http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return fmt.Errorf("http status %d: %w", code, apperrors.ErrInvalidArgument)
	case http.StatusUnauthorized, http.StatusForbidden:
		// A rotated token is re-authenticated by whatever owns the credential, not by
		// retrying this request, so the conservative reading is "permanent". A
		// deployment that refreshes credentials in-band should treat the warning this
		// produces as the signal that its refresh path is missing.
		return fmt.Errorf("http status %d: %w", code, apperrors.ErrAccessDenied)
	case http.StatusConflict:
		return fmt.Errorf("http status %d: %w", code, apperrors.ErrAlreadyExists)
	case http.StatusTooManyRequests:
		return fmt.Errorf("http status %d: %w", code, apperrors.ErrRateLimitExceeded)
	}
	if code >= 500 && code <= 599 {
		return fmt.Errorf("http status %d: %w", code, apperrors.ErrServiceUnavailable)
	}
	return nil
}

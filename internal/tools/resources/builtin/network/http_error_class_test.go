package builtin

import (
	"errors"
	"net/http"
	"testing"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// TestHTTPError_UnwrapExposesTheClass pins the wiring that makes the status mapping
// reach a caller: an HTTPError must answer errors.Is directly, without the caller
// parsing its message. It lives in this package because HTTPError does — the mapping
// itself (core.HTTPStatusError) is tested where it is defined.
func TestHTTPError_UnwrapExposesTheClass(t *testing.T) {
	notFound := &HTTPError{StatusCode: http.StatusNotFound, Message: "http status 404: not found"}
	if !errors.Is(notFound, apperrors.ErrNotFound) {
		t.Errorf("a 404 HTTPError must satisfy errors.Is(ErrNotFound), got %v", notFound)
	}

	// A transient status must keep its retries: the same Unwrap says "wait".
	unavailable := &HTTPError{StatusCode: http.StatusServiceUnavailable, Message: "http status 503"}
	if !errors.Is(unavailable, apperrors.ErrServiceUnavailable) {
		t.Errorf("a 503 HTTPError must satisfy errors.Is(ErrServiceUnavailable), got %v", unavailable)
	}
	if errors.Is(unavailable, apperrors.ErrNotFound) {
		t.Error("a 503 must not be reported as a not-found")
	}

	// An unclassified status must not accidentally match a permanent sentinel.
	teapot := &HTTPError{StatusCode: http.StatusTeapot, Message: "http status 418"}
	if errors.Is(teapot, apperrors.ErrNotFound) || errors.Is(teapot, apperrors.ErrInvalidArgument) {
		t.Error("an unclassified status must not match a permanent sentinel")
	}
	if teapot.Error() == "" {
		t.Error("Unwrap must not replace the message the caller prints")
	}
}

package planner

import (
	"errors"
	"strings"
	"testing"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestResultFailureCause_PrefersTheClass is the last leg of the 4xx fix: the class a
// tool attached to its failure must survive into the error the scheduler sees, or the
// mapping upstream changes nothing (the previous code wrapped only the message).
func TestResultFailureCause_PrefersTheClass(t *testing.T) {
	notFound := core.NewErrorResultWithCause("SearXNG returned status 404: nope", core.HTTPStatusError(404))

	cause := resultFailureCause("web_search", notFound)
	if !errors.Is(cause, apperrors.ErrNotFound) {
		t.Errorf("the class must reach the scheduler, got %v", cause)
	}
	if !strings.Contains(cause.Error(), "web_search") {
		t.Errorf("the error must name the tool that failed, got %v", cause)
	}

	// A transient class must NOT look permanent: both halves of the mapping have to
	// survive the same path.
	unavailable := core.NewErrorResultWithCause("status 503", core.HTTPStatusError(503))
	if errors.Is(resultFailureCause("web_search", unavailable), apperrors.ErrNotFound) {
		t.Error("a 503 must not be reported as a not-found")
	}
}

// TestResultFailureCause_FallsBackToTheMessage keeps the pre-existing behaviour for
// every tool that has no class to offer (most of them): the message is still the
// cause, so nothing that worked before regresses.
func TestResultFailureCause_FallsBackToTheMessage(t *testing.T) {
	cause := resultFailureCause("stringutils", core.NewErrorResult("operation is required"))
	if cause == nil || cause.Error() != "operation is required" {
		t.Errorf("cause = %v, want the message verbatim", cause)
	}
}

// TestResultFailureCause_AlwaysReturnsSomethingPrintable fixes a latent artifact of the
// old condition (`!result.Success && result.Error != ""`): a failure with no message
// left the last error nil, and the caller's `%w` rendered the literal "%!w(<nil>)".
func TestResultFailureCause_AlwaysReturnsSomethingPrintable(t *testing.T) {
	cause := resultFailureCause("mystery_tool", core.Result{Success: false})
	if cause == nil {
		t.Fatal("a failed result must always produce an error")
	}
	if strings.Contains(cause.Error(), "%!") {
		t.Errorf("cause must be printable, got %q", cause.Error())
	}
	if !strings.Contains(cause.Error(), "mystery_tool") {
		t.Errorf("cause must name the tool, got %q", cause.Error())
	}
}

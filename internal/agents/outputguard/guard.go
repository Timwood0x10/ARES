// Package outputguard validates agent outputs before they are consumed
// downstream (原语6: 框架级输出校验). A guard checks structural and
// consistency invariants of a TaskResult so malformed output — a result that
// claims success while carrying an error, or a failure with no explanation —
// is rejected at the boundary instead of propagating into aggregation,
// memory distillation, or event sinks.
package outputguard

import (
	"errors"
	"fmt"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// Sentinel errors for output guard violations. Callers use errors.Is to
// classify a rejected output.
var (
	// ErrSuccessWithError indicates a result marked Success but carrying a
	// non-empty Error (contradictory state).
	ErrSuccessWithError = errors.New("outputguard: success result carries an error")
	// ErrFailureWithoutDetail indicates a result marked failed with neither an
	// Error message nor a Reason explaining the failure.
	ErrFailureWithoutDetail = errors.New("outputguard: failed result lacks error/reason detail")
	// ErrNilResult indicates the executor returned a nil result.
	ErrNilResult = errors.New("outputguard: nil result")
)

// Guard validates agent outputs. A Guard is stateless and safe for concurrent
// use; create one per package or reuse a shared instance.
type Guard struct{}

// NewGuard creates an output Guard.
func NewGuard() *Guard {
	return &Guard{}
}

// ValidateResult checks a TaskResult for structural consistency. It returns
// the first violation found, or nil when the result is well-formed:
//
//   - nil result is rejected (ErrNilResult);
//   - Success == true with non-empty Error is rejected (ErrSuccessWithError);
//   - Success == false with empty Error AND empty Reason is rejected
//     (ErrFailureWithoutDetail) — a failure must be explainable.
func (g *Guard) ValidateResult(result *models.TaskResult) error {
	if result == nil {
		return fmt.Errorf("%w", ErrNilResult)
	}
	if result.Success && result.Error != "" {
		return fmt.Errorf("%w: task %s", ErrSuccessWithError, result.TaskID)
	}
	if !result.Success && result.Error == "" && result.Reason == "" {
		return fmt.Errorf("%w: task %s", ErrFailureWithoutDetail, result.TaskID)
	}
	return nil
}

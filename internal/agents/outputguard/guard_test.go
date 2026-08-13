package outputguard

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuard_ValidateResult_ValidSuccess(t *testing.T) {
	g := NewGuard()
	err := g.ValidateResult(&models.TaskResult{
		TaskID:  "t1",
		Success: true,
		Items:   []*models.RecommendItem{{ItemID: "i1"}},
	})
	require.NoError(t, err)
}

func TestGuard_ValidateResult_ValidFailureWithError(t *testing.T) {
	g := NewGuard()
	err := g.ValidateResult(&models.TaskResult{
		TaskID:  "t1",
		Success: false,
		Error:   "tool failed",
	})
	require.NoError(t, err)
}

func TestGuard_ValidateResult_ValidFailureWithReason(t *testing.T) {
	g := NewGuard()
	err := g.ValidateResult(&models.TaskResult{
		TaskID:  "t1",
		Success: false,
		Reason:  "rejected by human",
	})
	require.NoError(t, err)
}

func TestGuard_ValidateResult_SuccessWithErrorRejected(t *testing.T) {
	g := NewGuard()
	err := g.ValidateResult(&models.TaskResult{
		TaskID:  "t1",
		Success: true,
		Error:   "contradictory",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSuccessWithError)
}

func TestGuard_ValidateResult_FailureWithoutDetailRejected(t *testing.T) {
	g := NewGuard()
	err := g.ValidateResult(&models.TaskResult{
		TaskID:  "t1",
		Success: false, // no Error, no Reason
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFailureWithoutDetail)
}

func TestGuard_ValidateResult_NilRejected(t *testing.T) {
	g := NewGuard()
	err := g.ValidateResult(nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilResult)
}

package eval

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// reportingExecutor is an executor built on a runtime: it can say where the
// answer came from.
type reportingExecutor struct {
	bodySource string
	truncated  bool
}

func (e *reportingExecutor) Execute(context.Context, string) (string, []string, int, error) {
	return "some answer", nil, 0, nil
}

func (e *reportingExecutor) AnswerProvenance() (string, bool) {
	return e.bodySource, e.truncated
}

// plainExecutor is the LLM-only shape: no runtime, so no provenance to report.
type plainExecutor struct{}

func (e *plainExecutor) Execute(context.Context, string) (string, []string, int, error) {
	return "some answer", nil, 0, nil
}

// TestRunSingle_RecordsExecutorProvenance pins the OPTIONAL half of the executor
// contract: a runtime-backed executor's provenance reaches the TestResult through
// a type assertion, so the judge (or the deterministic floor) has a
// runtime-derived fact instead of guessing from text.
func TestRunSingle_RecordsExecutorProvenance(t *testing.T) {
	runner, err := NewAgentTestRunner(&reportingExecutor{
		bodySource: ares_events.AnswerBodySourceGap, truncated: true,
	})
	require.NoError(t, err)

	result, err := runner.RunSingle(context.Background(), TestCase{ID: "c", Input: "q"})
	require.NoError(t, err)
	assert.Equal(t, ares_events.AnswerBodySourceGap, result.AnswerBodySource)
	assert.True(t, result.Truncated)
}

// TestRunSingle_ExecutorWithoutProvenanceLeavesItAbsent keeps the other side
// honest: an executor that cannot report provenance leaves the fields empty, and
// that must read as "no signal" (no penalty) rather than as a healthy answer —
// the distinction the shared penalty rule is built on.
func TestRunSingle_ExecutorWithoutProvenanceLeavesItAbsent(t *testing.T) {
	runner, err := NewAgentTestRunner(&plainExecutor{})
	require.NoError(t, err)

	result, err := runner.RunSingle(context.Background(), TestCase{ID: "c", Input: "q"})
	require.NoError(t, err)
	assert.Empty(t, result.AnswerBodySource)
	assert.False(t, result.Truncated)

	factor, degraded := ares_events.AnswerQualityPenalty(result.AnswerBodySource, result.Truncated)
	assert.False(t, degraded, "no signal is not a degraded answer")
	assert.InDelta(t, 1.0, factor, 1e-9)
}

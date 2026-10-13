package evolution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// The degraded-answer penalty contract (P0-1): a completed task whose answer
// was a gap body (no real answer produced) or truncated (depth-guard forced)
// is NOT scored as success. The GA must not reward "did less and lied about
// finishing". Signals ride the task.completed event payload.

// completedWithAnswerQuality builds a task.completed event carrying the
// answer-quality signals in the payload.
func completedWithAnswerQuality(bodySource string, truncated bool) *ares_events.Event {
	p := map[string]any{"task_id": "t-quality"}
	if bodySource != "" {
		p[ares_events.EventKeyAnswerBodySource] = bodySource
	}
	if truncated {
		p[ares_events.EventKeyAnswerTruncated] = true
	}
	return &ares_events.Event{
		Type:      ares_events.EventTaskCompleted,
		Timestamp: time.Now(),
		Payload:   p,
	}
}

func TestDegradedAnswerPenaltyGap(t *testing.T) {
	obs := &RuntimeObserver{}
	factor, degraded := obs.degradedAnswerPenalty(completedWithAnswerQuality(ares_events.AnswerBodySourceGap, false))
	assert.True(t, degraded, "gap answer must be degraded")
	assert.Equal(t, 0.0, factor, "gap answer factor must be 0")
}

func TestDegradedAnswerPenaltyTruncated(t *testing.T) {
	obs := &RuntimeObserver{}
	factor, degraded := obs.degradedAnswerPenalty(completedWithAnswerQuality("", true))
	assert.True(t, degraded, "truncated answer must be degraded")
	assert.Equal(t, 0.5, factor, "truncated answer factor must be 0.5")
}

func TestDegradedAnswerPenaltySupplied(t *testing.T) {
	obs := &RuntimeObserver{}
	factor, degraded := obs.degradedAnswerPenalty(completedWithAnswerQuality(ares_events.AnswerBodySourceSupplied, false))
	assert.False(t, degraded, "supplied answer must NOT be degraded")
	assert.Equal(t, 1.0, factor, "supplied answer factor must be 1")
}

func TestDegradedAnswerPenaltyAbsent(t *testing.T) {
	obs := &RuntimeObserver{}
	factor, degraded := obs.degradedAnswerPenalty(&ares_events.Event{
		Type:      ares_events.EventTaskCompleted,
		Timestamp: time.Now(),
		Payload:   map[string]any{"task_id": "t"},
	})
	assert.False(t, degraded, "absent signals must NOT be degraded")
	assert.Equal(t, 1.0, factor, "absent signals factor must be 1")
}

func TestEventToSampleGapAnswerIsNotSuccess(t *testing.T) {
	obs := &RuntimeObserver{}
	s, ok := obs.eventToSample(completedWithAnswerQuality(ares_events.AnswerBodySourceGap, false))
	require.True(t, ok)
	assert.False(t, s.Success, "a gap answer must not be counted as success")
	assert.Equal(t, 0.0, s.Score, "a gap answer must score 0")
}

func TestEventToSampleTruncatedAnswerIsDegraded(t *testing.T) {
	obs := &RuntimeObserver{}
	s, ok := obs.eventToSample(completedWithAnswerQuality("", true))
	require.True(t, ok)
	assert.False(t, s.Success, "a truncated answer must not be counted as success")
	assert.Equal(t, 0.5, s.Score, "a truncated answer must score 0.5")
}

func TestEventToSampleNormalAnswerStaysSuccess(t *testing.T) {
	obs := &RuntimeObserver{}
	s, ok := obs.eventToSample(completedWithAnswerQuality(ares_events.AnswerBodySourceSupplied, false))
	require.True(t, ok)
	assert.True(t, s.Success, "a supplied answer stays success")
	assert.Equal(t, 1.0, s.Score, "a supplied answer keeps score 1")
}

func TestDegradedAnswerStillOutscoresFailure(t *testing.T) {
	// A truncated answer (0.5) must still outscore a failure (0.0) —
	// it produced something under duress, which is better than nothing.
	obs := &RuntimeObserver{}
	truncated, _ := obs.eventToSample(completedWithAnswerQuality("", true))
	failure, _ := obs.eventToSample(&ares_events.Event{
		Type:      ares_events.EventTaskFailed,
		Timestamp: time.Now(),
		Payload:   map[string]any{"task_id": "t"},
	})
	assert.Greater(t, truncated.Score, failure.Score,
		"truncated answer must outscore failure")
}

// TestGapAnswerEqualsFailureScore locks the harshest case: a gap answer
// (nothing produced) is scored identically to a failure. This is the P0-1
// fix's core assertion — "did less and lied about finishing" must not beat a
// genuine failure.
func TestGapAnswerEqualsFailureScore(t *testing.T) {
	obs := &RuntimeObserver{}
	gap, _ := obs.eventToSample(completedWithAnswerQuality(ares_events.AnswerBodySourceGap, false))
	failure, _ := obs.eventToSample(&ares_events.Event{
		Type:      ares_events.EventTaskFailed,
		Timestamp: time.Now(),
		Payload:   map[string]any{"task_id": "t"},
	})
	assert.Equal(t, failure.Score, gap.Score,
		"gap answer must score identically to failure")
	assert.False(t, gap.Success, "gap must not be success")
	assert.False(t, failure.Success, "failure must not be success")
}

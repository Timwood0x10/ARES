// rollback_short_window_test.go locks REVIEW 3.4#3: with windowSize 3 or 4
// the recent-half decline check had fewer than two comparisons, so the
// "at least 2 declines" gate could never fire — gradual-decline detection
// silently vanished for every window below 5 entries. Short windows now
// inspect the whole window instead.
package evolution

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRollbackPolicy_GradualDeclineShortWindow(t *testing.T) {
	t.Run("window size 3", func(t *testing.T) {
		p := NewRollbackPolicy(
			WithDegradationThreshold(0.15),
			WithRollbackWindowSize(3),
			WithMinRollbackSamples(3),
		)
		// Steady decline: 100 → 98 → 96. Degradation 4 < threshold 15, so
		// only gradual detection can fire — pre-fix it never did for
		// windows below 5.
		p.RecordScore(1, 100.0)
		p.RecordScore(2, 98.0)
		p.RecordScore(3, 96.0)

		decision := p.Evaluate()
		assert.True(t, decision.ShouldRollback, "consistent decline in a 3-entry window must trigger rollback")
		assert.Contains(t, decision.Reason, "gradual degradation")
	})

	t.Run("window size 4", func(t *testing.T) {
		p := NewRollbackPolicy(
			WithDegradationThreshold(0.15),
			WithRollbackWindowSize(4),
			WithMinRollbackSamples(3),
		)
		p.RecordScore(1, 100.0)
		p.RecordScore(2, 99.0)
		p.RecordScore(3, 98.0)
		p.RecordScore(4, 97.0)

		decision := p.Evaluate()
		assert.True(t, decision.ShouldRollback, "consistent decline in a 4-entry window must trigger rollback")
		assert.Contains(t, decision.Reason, "gradual degradation")
	})

	t.Run("window size 3 with a bump does not trigger", func(t *testing.T) {
		p := NewRollbackPolicy(
			WithDegradationThreshold(10),
			WithRollbackWindowSize(3),
			WithMinRollbackSamples(3),
		)
		// Not a consistent decline: 100 → 90 → 95. Sudden-drop degradation
		// is 5 < 10, and the rise at the end must veto gradual detection.
		p.RecordScore(1, 100.0)
		p.RecordScore(2, 90.0)
		p.RecordScore(3, 95.0)

		decision := p.Evaluate()
		assert.False(t, decision.ShouldRollback, "a non-monotonic short window must not trigger gradual rollback")
	})
}

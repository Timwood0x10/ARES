package evolution

import "testing"

// TestGradualDeclineGateUsesThreshold pins G-5.
//
// Regression (docs/reviews/0.3.1-final-deep-review.md finding G-5):
// isGradualDeclineLocked only asked "did the recent half decline monotonically,
// at least twice?" — it never consulted degradationThreshold. Two 0.001 wobbles
// therefore satisfied it and triggered a rollback, so the configured threshold
// (0.15 by default) applied to the sudden-drop path only and production saw
// meaningless rollbacks on noise.
func TestGradualDeclineGateUsesThreshold(t *testing.T) {
	gradual := func(scores ...float64) bool {
		p := NewRollbackPolicy()
		for i, s := range scores {
			p.RecordScore(i+1, s)
		}
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.isGradualDeclineLocked()
	}

	t.Run("sub_threshold_wobble_is_not_a_decline", func(t *testing.T) {
		// Monotonic, but the net movement inside the checked range is 0.002 —
		// far below the 0.15 default. Pre-fix this returned true.
		if gradual(0.800, 0.799, 0.798, 0.797, 0.796) {
			t.Fatal("a sub-threshold monotonic wobble must not trigger a gradual-decline rollback")
		}
	})

	t.Run("threshold_sized_decline_is_detected", func(t *testing.T) {
		// Net decline inside the checked range is 0.20 >= 0.15: this must still
		// fire, so the gate cannot degrade into "never rollback".
		if !gradual(0.90, 0.80, 0.70, 0.60, 0.50) {
			t.Fatal("a threshold-sized monotonic decline must still be detected")
		}
	})
}

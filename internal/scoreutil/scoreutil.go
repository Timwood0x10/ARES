// Package scoreutil provides helpers for normalizing relevance and confidence
// scores into a canonical range so downstream sorting, filtering, and display
// operate on a well-defined domain regardless of which backend produced them.
package scoreutil

// ClampUnit clamps v into the closed interval [0, 1].
//
// Backends are contractually expected to return confidence/relevance scores in
// this range, but a defensive clamp keeps downstream consumers (snippet merge,
// dedup, prompt rendering) safe against negative or >1 values that would
// otherwise break sort ordering or filtering thresholds.
//
// Args:
//
//	v - raw score from a retriever/provider. Any sign or magnitude is accepted.
//
// Returns:
//
//	float64 - v bounded to [0, 1]. NaN is returned as 0 (treated as "no signal").
func ClampUnit(v float64) float64 {
	if v < 0 || v != v { // v != v guards against NaN
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

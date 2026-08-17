package aresrecovery

import (
	"math"
	"testing"
)

// approxEqual reports whether two floats are equal within a small epsilon
// (attribution arithmetic uses float division, so exact equality is brittle).
func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// TestChangeAttributorEqualShare verifies the delta is distributed equally
// across changes without explicit impacts.
func TestChangeAttributorEqualShare(t *testing.T) {
	before := &GenerationSnapshot{Generation: 1, BestScore: 0.5}
	after := &GenerationSnapshot{
		Generation: 2,
		BestScore:  0.8,
		Changes: []GenerationChange{
			{StrategyID: "s1", Description: "mutate temp"},
			{StrategyID: "s2", Description: "swap tools"},
		},
	}
	got := ChangeAttributor{}.Attribute(before, after)
	if len(got) != 2 {
		t.Fatalf("want 2 attributions, got %d", len(got))
	}
	// delta 0.3 / 2 = 0.15 each.
	for _, id := range []string{"s1", "s2"} {
		if !approxEqual(got[id], 0.15) {
			t.Fatalf("impact of %s = %v, want 0.15", id, got[id])
		}
	}
}

// TestChangeAttributorExplicitImpactWins verifies an explicit Impact value is
// kept instead of the equal share.
func TestChangeAttributorExplicitImpactWins(t *testing.T) {
	before := &GenerationSnapshot{Generation: 1, BestScore: 0.5}
	after := &GenerationSnapshot{
		Generation: 2,
		BestScore:  0.8,
		Changes: []GenerationChange{
			{StrategyID: "s1", Description: "counterfactual", Impact: 0.25},
			{StrategyID: "s2", Description: "plain"},
		},
	}
	got := ChangeAttributor{}.Attribute(before, after)
	// s1 keeps 0.25; s2 gets the remaining delta 0.05 (0.3 - 0.25).
	if !approxEqual(got["s1"], 0.25) {
		t.Fatalf("s1 impact = %v, want 0.25", got["s1"])
	}
	if !approxEqual(got["s2"], 0.05) {
		t.Fatalf("s2 impact = %v, want 0.05", got["s2"])
	}
}

// TestChangeAttributorNilBeforeMeasuresFromZero verifies a nil before treats
// the delta as measured from 0.
func TestChangeAttributorNilBeforeMeasuresFromZero(t *testing.T) {
	after := &GenerationSnapshot{
		Generation: 1,
		BestScore:  0.6,
		Changes:    []GenerationChange{{StrategyID: "s1"}},
	}
	got := ChangeAttributor{}.Attribute(nil, after)
	if got["s1"] != 0.6 {
		t.Fatalf("s1 impact = %v, want 0.6 (delta from 0)", got["s1"])
	}
}

// TestChangeAttributorNoChangesOrZeroDelta verifies empty changes and zero
// deltas yield no attribution.
func TestChangeAttributorNoChangesOrZeroDelta(t *testing.T) {
	// Empty changes → nil.
	if got := (ChangeAttributor{}).Attribute(&GenerationSnapshot{BestScore: 0.5}, &GenerationSnapshot{BestScore: 0.8}); got != nil {
		t.Fatalf("no changes must yield nil, got %v", got)
	}
	// Nil after → nil.
	if got := (ChangeAttributor{}).Attribute(&GenerationSnapshot{BestScore: 0.5}, nil); got != nil {
		t.Fatalf("nil after must yield nil, got %v", got)
	}
	// Zero delta → all shared changes get 0.
	after := &GenerationSnapshot{
		BestScore: 0.5,
		Changes:   []GenerationChange{{StrategyID: "s1"}, {StrategyID: "s2"}},
	}
	got := (ChangeAttributor{}).Attribute(&GenerationSnapshot{BestScore: 0.5}, after)
	if got["s1"] != 0 || got["s2"] != 0 {
		t.Fatalf("zero delta must yield zero impacts, got %v", got)
	}
}

// TestChangeAttributorTrajectory verifies AttributeTrajectory returns one
// attribution map per adjacent pair.
func TestChangeAttributorTrajectory(t *testing.T) {
	snaps := []GenerationSnapshot{
		{Generation: 1, BestScore: 0.5},
		{Generation: 2, BestScore: 0.8, Changes: []GenerationChange{{StrategyID: "s1"}}},
		{Generation: 3, BestScore: 0.9, Changes: []GenerationChange{{StrategyID: "s2"}}},
	}
	got := (ChangeAttributor{}).AttributeTrajectory(snaps)
	if len(got) != 2 {
		t.Fatalf("want 2 attribution maps, got %d", len(got))
	}
	if !approxEqual(got[0]["s1"], 0.3) {
		t.Fatalf("pair 1 impact = %v, want 0.3", got[0]["s1"])
	}
	if !approxEqual(got[1]["s2"], 0.1) {
		t.Fatalf("pair 2 impact = %v, want 0.1", got[1]["s2"])
	}
	if (ChangeAttributor{}).AttributeTrajectory(snaps[:1]) != nil {
		t.Fatal("fewer than 2 snapshots must yield nil")
	}
}

package genome

import (
	"math/rand"
	"testing"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// TestSpatialIndexCellKeyNormalizedSpace locks the dimensional fix for the
// spatial index (REVIEW 2.4#21): cell coordinates must be computed in the
// SAME range-normalized space paramDistance/nicheRadius live in. Quantizing
// RAW parameter values with the normalized-space cellSize put a wide-range
// dim (scale 0..4096) hundreds of cells away from a normalized-close
// neighbor, so the adjacent-cell scan silently missed it and fitness sharing
// never penalized the crowding.
func TestSpatialIndexCellKeyNormalizedSpace(t *testing.T) {
	scored := []*mutation.Strategy{
		{ID: "a1", Params: map[string]any{"scale": 0.0, "temp": 0.5}},
		{ID: "a2", Params: map[string]any{"scale": 15.0, "temp": 0.5}},
		{ID: "a3", Params: map[string]any{"scale": 4096.0, "temp": 0.5}},
	}
	ranges := map[string]float64{"scale": 4096, "temp": 1}
	idx := newSpatialIndex([]int{0, 1, 2}, scored, []string{"scale", "temp"}, ranges, 0.15)
	if idx == nil {
		t.Fatal("expected a spatial index for 2 float dims")
	}

	// a1 and a2 are normalized-close: |0-15|/4096 = 0.0037 << nicheRadius,
	// so they must share a grid cell. Pre-fix, raw 15/0.15 = 100 cells away.
	if idx.cellKey(0) != idx.cellKey(1) {
		t.Errorf("normalized-close agents a1/a2 landed in different cells: %q vs %q",
			idx.cellKey(0), idx.cellKey(1))
	}

	// a3 is normalized-FAR (|0-4096|/4096 = 1.0 >> nicheRadius): it may share
	// the clamped boundary cell with a1 only via the 255 clamp, but the exact
	// distance re-check in applyFitnessSharingSpatial filters it; what must
	// NOT happen is a1/a2 being separated.
	neighbors := idx.neighborsWithin(0)
	found := false
	for _, n := range neighbors {
		if n == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("neighborsWithin(0) must include the normalized-close a2, got %v", neighbors)
	}
}

// TestApplyFitnessSharingSpatialWideRangeDim verifies the end-to-end effect
// of the normalized grid: two agents that are close ONLY in normalized space
// (one dim has a large raw range) crowd each other and get their selection
// score penalized via the spatial path.
func TestApplyFitnessSharingSpatialWideRangeDim(t *testing.T) {
	pop := &Population{
		Agents: []*mutation.Strategy{
			{ID: "n1", Score: 100, Params: map[string]any{"scale": 0.0, "temp": 0.5}},
			{ID: "n2", Score: 90, Params: map[string]any{"scale": 15.0, "temp": 0.5}},
			// Sets the "scale" range to 4096 — the raw values above are far
			// apart, but their normalized distance is 15/4096 ≈ 0.004,
			// well within FitnessNicheRadius (0.15).
			{ID: "anchor", Score: 80, Params: map[string]any{"scale": 4096.0, "temp": 0.5}},
		},
		cfg: PopulationConfig{
			EliteCount:                0,
			FitnessSharingSampleLimit: 100,
			SpatialIndexThreshold:     2, // m=3 > 2 → spatial mode
		},
		rng: rand.New(rand.NewSource(42)),
	}

	pop.applyFitnessSharing(0)

	// n1 and n2 crowd each other → penalized selection scores.
	for _, id := range []int{0, 1} {
		a := pop.Agents[id]
		if a.SelectionScore <= 0 || a.SelectionScore >= a.Score {
			t.Errorf("agent %s: expected penalized SelectionScore in (0, %.2f), got %.2f",
				a.ID, a.Score, a.SelectionScore)
		}
	}
	// The anchor is normalized-far from both (distance ≈ 0.5 > 0.15): no
	// crowd, no penalty — SelectionScore stays unset (0).
	if anchor := pop.Agents[2]; anchor.SelectionScore != 0 {
		t.Errorf("normalized-far anchor should not be penalized, got SelectionScore %.2f",
			anchor.SelectionScore)
	}
}

// TestSelectTopVarDimsNormalizedVariance locks the variance normalization in
// the grid dimension projection: raw variance is dimensionally inconsistent
// (a dim spanning 0..4096 always out-varies one spanning 0..0.001 regardless
// of actual normalized spread), so the projection picked dims by unit scale,
// not by information content.
func TestSelectTopVarDimsNormalizedVariance(t *testing.T) {
	keys := []string{"bigscale", "tiny", "mid1", "mid2", "mid3", "mid4", "mid5"}
	// 10 agents. bigscale: 9 identical values plus one outlier at 4096 —
	// huge RAW variance (~1.5M) but a clustered normalized spread (var 0.09,
	// the LOWEST normalized variance: 90% of the mass sits at one end).
	// tiny: two values at the range extremes — the HIGHEST normalized
	// variance (0.25) despite a raw variance of 2.5e-7.
	scored := make([]*mutation.Strategy, 10)
	for i := range scored {
		bigscale, tiny, mid := 0.0, 0.0, 0.2
		if i == 9 {
			bigscale = 4096.0
		}
		if i%2 == 1 {
			tiny = 0.001
		}
		switch i % 3 {
		case 1:
			mid = 0.6
		case 2:
			mid = 1.0
		}
		scored[i] = &mutation.Strategy{Params: map[string]any{
			"bigscale": bigscale, "tiny": tiny,
			"mid1": mid, "mid2": mid, "mid3": mid, "mid4": mid, "mid5": mid,
		}}
	}
	ranges := map[string]float64{
		"bigscale": 4096,
		"tiny":     0.001,
		"mid1":     0.8, "mid2": 0.8, "mid3": 0.8, "mid4": 0.8, "mid5": 0.8,
	}

	selected := selectTopVarDims(scored, keys, ranges, 6)
	in := map[string]bool{}
	for _, k := range selected {
		in[k] = true
	}
	if in["bigscale"] {
		t.Errorf("bigscale (highest RAW variance, lowest normalized variance) must not be selected: %v", selected)
	}
	if !in["tiny"] {
		t.Errorf("tiny (lowest RAW variance, highest normalized variance) must be selected: %v", selected)
	}
	if len(selected) != 6 {
		t.Errorf("expected 6 selected dims, got %d: %v", len(selected), selected)
	}
}

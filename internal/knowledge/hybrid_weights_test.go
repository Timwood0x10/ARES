package knowledge

import (
	"math"
	"testing"
	"time"
)

// recencyObject builds a candidate whose relevance is identical to every other
// candidate in these tests, so the only thing that can move its rank is the recency
// term.
func recencyObject(id string, observed time.Time) *KnowledgeObject {
	return &KnowledgeObject{
		ID:        id,
		Summary:   "deployment pipeline retry policy",
		UpdatedAt: observed,
		CreatedAt: observed,
	}
}

// unitVec returns a 2-dim vector, since CosineSimilarity needs matching dimensions.
func unitVec() []float32 { return []float32{1, 0} }

// TestFreshnessScore_Tiers pins the shared freshness rule (used by the write-time
// quality score and the query-time recency term).
func TestFreshnessScore_Tiers(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		at   time.Time
		want float64
	}{
		{"unknown timestamp is neutral, not ancient", time.Time{}, 0.5},
		{"hours old is fully fresh", now.Add(-time.Hour), 1.0},
		{"three days old", now.Add(-3 * 24 * time.Hour), 0.8},
		{"ten days old", now.Add(-10 * 24 * time.Hour), 0.5},
		{"two months old is the oldest tier", now.Add(-60 * 24 * time.Hour), 0.3},
		// A future timestamp is clock skew, not a fresher-than-fresh object; it must
		// not fall through to the oldest tier.
		{"future timestamp is treated as fresh", now.Add(48 * time.Hour), 1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FreshnessScore(tc.at); got != tc.want {
				t.Errorf("FreshnessScore = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDefaultHybridWeights_PreserveLegacyScoring is the compatibility contract: the
// shipped weights must reproduce the pre-existing formula exactly, or every
// production retrieval silently re-ranks on upgrade.
func TestDefaultHybridWeights_PreserveLegacyScoring(t *testing.T) {
	obj := recencyObject("obj-1", time.Now().Add(-time.Hour))
	objs := []*KnowledgeObject{obj}
	query := "deployment pipeline"
	reps := map[string]*Representation{"obj-1": {ID: "rep-1", Model: "m", Vector: unitVec()}}

	scored := ScoreHybrid(objs, reps, unitVec(), query)
	if len(scored) != 1 {
		t.Fatalf("expected one result, got %d", len(scored))
	}
	want := 0.7*scored[0].VectorScore + 0.3*scored[0].LexicalScore
	if math.Abs(scored[0].FinalScore-want) > 1e-9 {
		t.Errorf("FinalScore = %v, want the legacy 0.7*vector + 0.3*lexical = %v", scored[0].FinalScore, want)
	}

	// Without a representation the legacy behaviour is lexical alone — an object the
	// embedding worker has not reached yet must stay rankable.
	noRep := ScoreHybrid(objs, map[string]*Representation{}, unitVec(), query)
	if math.Abs(noRep[0].FinalScore-noRep[0].LexicalScore) > 1e-9 {
		t.Errorf("no-vector FinalScore = %v, want the lexical score %v", noRep[0].FinalScore, noRep[0].LexicalScore)
	}
}

// TestScoreHybrid_RecencyBreaksTiesOnlyWhenWeighted is the P1-2/R2 fix at the point
// where it matters: two equally relevant objects, one fresh and one stale. With the
// shipped weights they tie (behaviour unchanged); with a recency weight the fresh one
// wins.
func TestScoreHybrid_RecencyBreaksTiesOnlyWhenWeighted(t *testing.T) {
	fresh := recencyObject("fresh", time.Now().Add(-time.Hour))
	stale := recencyObject("stale", time.Now().Add(-60*24*time.Hour))
	objs := []*KnowledgeObject{fresh, stale}
	reps := map[string]*Representation{
		"fresh": {ID: "r1", Model: "m", Vector: unitVec()},
		"stale": {ID: "r2", Model: "m", Vector: unitVec()},
	}

	byID := func(scored []ScoredObject) map[string]ScoredObject {
		out := make(map[string]ScoredObject, len(scored))
		for _, s := range scored {
			out[s.Object.ID] = s
		}
		return out
	}

	defaults := byID(ScoreHybrid(objs, reps, unitVec(), "deployment pipeline"))
	if math.Abs(defaults["fresh"].FinalScore-defaults["stale"].FinalScore) > 1e-9 {
		t.Errorf("with the default weights a recency term moved the score: fresh=%v stale=%v",
			defaults["fresh"].FinalScore, defaults["stale"].FinalScore)
	}

	weighted := byID(ScoreHybridWithWeights(objs, reps, unitVec(), "deployment pipeline",
		HybridWeights{Vector: 0.6, Lexical: 0.2, Recency: 0.2}))
	if weighted["fresh"].FinalScore <= weighted["stale"].FinalScore {
		t.Errorf("with a recency weight the fresh object must outrank the stale one: fresh=%v stale=%v",
			weighted["fresh"].FinalScore, weighted["stale"].FinalScore)
	}
	// The components stay reported even when the term is unweighted, so a reader can
	// see what a different configuration would have done.
	if defaults["fresh"].RecencyScore != 1.0 || defaults["stale"].RecencyScore != 0.3 {
		t.Errorf("recency components = (%v, %v), want (1.0, 0.3)",
			defaults["fresh"].RecencyScore, defaults["stale"].RecencyScore)
	}
}

// TestWithRecency_KeepsTheBaseRatios: enabling recency must not rebalance vector
// against lexical, or a deployment that only meant to add freshness would silently
// change how much relevance counts.
func TestWithRecency_KeepsTheBaseRatios(t *testing.T) {
	base := DefaultHybridWeights()
	got := base.WithRecency(0.2)
	if got.Vector != base.Vector || got.Lexical != base.Lexical {
		t.Errorf("WithRecency changed the relevance weights: %+v -> %+v", base, got)
	}
	if got.Recency != 0.2 {
		t.Errorf("recency = %v, want 0.2", got.Recency)
	}
}

// TestScoreHybrid_WeightsAreRelative: the weights are a ratio, so scaling them all
// must not change any score — a caller switching from 0.7/0.3 to 70/30 must not see
// a different ranking.
func TestScoreHybrid_WeightsAreRelative(t *testing.T) {
	objs := []*KnowledgeObject{recencyObject("obj-1", time.Now().Add(-time.Hour))}
	reps := map[string]*Representation{"obj-1": {ID: "r1", Model: "m", Vector: unitVec()}}

	small := ScoreHybridWithWeights(objs, reps, unitVec(), "deployment", HybridWeights{Vector: 0.7, Lexical: 0.3})
	large := ScoreHybridWithWeights(objs, reps, unitVec(), "deployment", HybridWeights{Vector: 70, Lexical: 30})
	if math.Abs(small[0].FinalScore-large[0].FinalScore) > 1e-9 {
		t.Errorf("scaling the weights changed the score: %v vs %v", small[0].FinalScore, large[0].FinalScore)
	}
}

// TestScoreHybrid_AllZeroWeightsFallBackToDefaults: an all-zero configuration would
// give every candidate FinalScore 0, which is indistinguishable from "nothing
// matched". The defaults are applied instead, loudly.
func TestScoreHybrid_AllZeroWeightsFallBackToDefaults(t *testing.T) {
	logs := captureDefaultLog(t)

	objs := []*KnowledgeObject{recencyObject("obj-1", time.Now().Add(-time.Hour))}
	reps := map[string]*Representation{"obj-1": {ID: "r1", Model: "m", Vector: unitVec()}}

	zeroed := ScoreHybridWithWeights(objs, reps, unitVec(), "deployment", HybridWeights{})
	if zeroed[0].FinalScore <= 0 {
		t.Errorf("all-zero weights zeroed the retrieval: FinalScore = %v", zeroed[0].FinalScore)
	}
	withDefaults := ScoreHybrid(objs, reps, unitVec(), "deployment")
	if math.Abs(zeroed[0].FinalScore-withDefaults[0].FinalScore) > 1e-9 {
		t.Errorf("all-zero weights did not fall back to the defaults: %v vs %v",
			zeroed[0].FinalScore, withDefaults[0].FinalScore)
	}
	if _, ok := logs.findWarn("all zero"); !ok {
		t.Error("an all-zero weight configuration must be reported, not silently corrected")
	}
}

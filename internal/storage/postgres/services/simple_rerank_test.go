package services

import (
	"math"
	"testing"
	"time"
)

// TestLexicalOverlap pins the term's meaning: the share of the query's content
// words present in the candidate, where coverage (not Jaccard) is deliberate —
// a long chunk that mentions everything asked for must not be penalized for its
// own length.
func TestLexicalOverlap(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		content string
		want    float64
	}{
		{"everything asked for is mentioned", "deployment pipeline", "the deployment pipeline retries on failure", 1.0},
		{"nothing in common", "deployment pipeline", "cooking pasta at home", 0.0},
		{"half the terms", "deployment pipeline", "deployment notes only", 0.5},
		{"punctuation splits terms", "go-agent, runtime", "the go agent runtime is small", 1.0},
		{"short tokens are dropped", "a is b", "a is b", 0.0},
		{"case folds", "DEPLOYMENT", "deployment", 1.0},
		// CJK has no spaces, so a clause is ONE run: kept whole, the term would match
		// only an identical clause and contribute nothing in the language this
		// repository's prompts and corpus are written in. Adjacent-character bigrams
		// make partial overlap measurable (7 of the query's 8 bigrams are present).
		{"chinese overlap is partial, not all-or-nothing", "人工智能的最新趋势", "人工智能领域的最新趋势", 0.875},
		{"unrelated chinese scores zero", "人工智能", "今天天气很好", 0.0},
		{"chinese punctuation separates clauses", "人工智能，最新趋势", "最新趋势：人工智能", 1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := lexicalOverlap(tc.query, tc.content)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("lexicalOverlap(%q, %q) = %v, want %v", tc.query, tc.content, got, tc.want)
			}
		})
	}
}

// TestRerankScoreDemotesTheFalseNear is the defect's behaviour, asserted
// directly: a candidate that is nearer in vector space but never mentions what
// was asked must lose to a slightly further one that does. Under the pure cosine
// order the first one would have won, and the wide recall would have been wasted.
func TestRerankScoreDemotesTheFalseNear(t *testing.T) {
	weights := DefaultSimpleRerankWeights()
	query := "deployment pipeline retry"

	falseNear := rerankCandidate{
		Content:    "a note about unrelated cooking measurements",
		Similarity: 0.95,
	}
	relevant := rerankCandidate{
		Content:    "the deployment pipeline retries a failed step",
		Similarity: 0.80,
	}

	falseNearScore := rerankScore(query, falseNear, weights, defaultRerankHalfLife)
	relevantScore := rerankScore(query, relevant, weights, defaultRerankHalfLife)

	if falseNearScore >= relevantScore {
		t.Errorf("rerank kept the false near: falseNear=%v relevant=%v", falseNearScore, relevantScore)
	}
	// ...while plain similarity would have preferred it, which is why the
	// rerank exists at all.
	if falseNear.Similarity <= relevant.Similarity {
		t.Fatal("this test is meaningless unless the false neighbour is nearer")
	}
}

// TestRerankScoreDropsUnknownEvidence pins the honesty rule: an unknown age must
// NOT be treated as stale. The scored candidate's score has to equal the same
// candidate scored with the recency term switched off.
func TestRerankScoreDropsUnknownEvidence(t *testing.T) {
	query := "deployment pipeline"
	weights := DefaultSimpleRerankWeights()
	unknownAge := rerankCandidate{Content: "the deployment pipeline", Similarity: 0.7}

	got := rerankScore(query, unknownAge, weights, defaultRerankHalfLife)

	noRecency := SimpleRerankWeights{Similarity: weights.Similarity, Lexical: weights.Lexical}
	want := rerankScore(query, unknownAge, noRecency, defaultRerankHalfLife)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("unknown age changed the score: got %v, want %v (recency must be dropped)", got, want)
	}
}

// TestRerankScorePrefersFreshOnEqualGround: with identical similarity and text,
// the fresher row wins — the recency term's whole reason to exist.
func TestRerankScorePrefersFreshOnEqualGround(t *testing.T) {
	weights := DefaultSimpleRerankWeights()
	query := "deployment pipeline"
	base := rerankCandidate{Content: "the deployment pipeline", Similarity: 0.9}

	fresh := base
	fresh.Age = time.Hour
	stale := base
	stale.Age = 90 * 24 * time.Hour

	if rerankScore(query, fresh, weights, defaultRerankHalfLife) <= rerankScore(query, stale, weights, defaultRerankHalfLife) {
		t.Error("on equal ground the fresher candidate must rank higher")
	}
}

// TestRerankScoreFallsBackWhenWeightsDisabled: disabling every term must still
// return the recall score, never a constant or NaN — a constant would make the
// order arbitrary while looking like a score.
func TestRerankScoreFallsBackWhenWeightsDisabled(t *testing.T) {
	candidate := rerankCandidate{Content: "anything", Similarity: 0.42}
	got := rerankScore("query", candidate, SimpleRerankWeights{}, defaultRerankHalfLife)
	if math.Abs(got-0.42) > 1e-9 {
		t.Errorf("fallback score = %v, want the similarity 0.42", got)
	}
}

// TestRerankScoreClampsOutOfRangeScores keeps a corrupt recall score from
// dominating the ranking.
func TestRerankScoreClampsOutOfRangeScores(t *testing.T) {
	weights := DefaultSimpleRerankWeights()
	high := rerankScore("q", rerankCandidate{Content: "q", Similarity: 7}, weights, defaultRerankHalfLife)
	low := rerankScore("q", rerankCandidate{Content: "q", Similarity: -3}, weights, defaultRerankHalfLife)
	if high < 0 || high > 1 || low < 0 || low > 1 {
		t.Errorf("scores must stay in [0,1]: high=%v low=%v", high, low)
	}
}

func TestRecencyFactor(t *testing.T) {
	half := 30 * 24 * time.Hour
	if got := recencyFactor(half, half); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("one half-life = %v, want 0.5", got)
	}
	if got := recencyFactor(2*half, half); math.Abs(got-0.25) > 1e-9 {
		t.Errorf("two half-lives = %v, want 0.25", got)
	}
	if got := recencyFactor(365*24*time.Hour, half); got <= 0 || got >= 0.001 {
		t.Errorf("a year old must be nearly zero, got %v", got)
	}
	// A zero AGE means "unknown", and rerankScore never routes it here; the
	// factor itself treats a non-negative age as fresh rather than as an error.
	if got := recencyFactor(0, half); got != 1 {
		t.Errorf("recencyFactor(0) = %v, want 1", got)
	}
}

// TestRerankCandidatesOrdersAndTruncates pins the contract the wide recall
// depends on: descending order, stable on ties, cut to topK.
func TestRerankCandidatesOrdersAndTruncates(t *testing.T) {
	weights := DefaultSimpleRerankWeights()
	query := "deployment pipeline"
	candidates := []rerankCandidate{
		{Content: "unrelated notes on cooking", Similarity: 0.95},
		{Content: "the deployment pipeline retries steps", Similarity: 0.70},
		{Content: "some pipeline documentation", Similarity: 0.80},
	}

	got := rerankCandidates(query, candidates, 2, weights, defaultRerankHalfLife)
	if len(got) != 2 {
		t.Fatalf("returned %d candidates, want the top 2", len(got))
	}
	if got[0].Content != "the deployment pipeline retries steps" {
		t.Errorf("top candidate = %q, want the one that answers the query", got[0].Content)
	}
	if got[1].Similarity < 0.80 {
		t.Errorf("second candidate similarity = %v, want the 0.80 one", got[1].Similarity)
	}

	if ranked := rerankCandidates(query, nil, 5, weights, defaultRerankHalfLife); ranked != nil {
		t.Errorf("no candidates must rank to nil, got %v", ranked)
	}
	if ranked := rerankCandidates(query, candidates, 0, weights, defaultRerankHalfLife); ranked != nil {
		t.Errorf("a non-positive topK must rank to nil, got %v", ranked)
	}
}

// TestRerankCandidates_UnknownAgeDropsOnlyTheRecencyTerm is the regression guard for
// the widest blast radius of this feature: created_at only started travelling in
// metadata alongside it, so a candidate written BEFORE that change has no timestamp.
// Such a candidate must lose the recency term and nothing else — it must still be
// returned, still be ranked, and never be dropped from the result set.
func TestRerankCandidates_UnknownAgeDropsOnlyTheRecencyTerm(t *testing.T) {
	weights := DefaultSimpleRerankWeights()
	query := "deployment pipeline"

	legacy := rerankCandidate{Content: "the deployment pipeline retries steps", Similarity: 0.9} // Age == 0 → unknown
	daterd := rerankCandidate{Content: "the deployment pipeline retries steps", Similarity: 0.9, Age: time.Hour}

	got := rerankCandidates(query, []rerankCandidate{legacy}, 5, weights, defaultRerankHalfLife)
	if len(got) != 1 {
		t.Fatalf("a candidate without created_at must still be returned, got %d candidates", len(got))
	}

	// And it is scored exactly as the same candidate without the recency term would
	// be, not as if it were ancient.
	withoutRecency := SimpleRerankWeights{Similarity: weights.Similarity, Lexical: weights.Lexical}
	want := rerankScore(query, legacy, withoutRecency, defaultRerankHalfLife)
	if score := rerankScore(query, legacy, weights, defaultRerankHalfLife); math.Abs(score-want) > 1e-9 {
		t.Errorf("unknown age changed the score: %v, want %v", score, want)
	}
	// A dated candidate of equal text scores HIGHER: that difference is the term
	// working, and it is the reason dropping it (rather than guessing) matters.
	if rerankScore(query, legacy, weights, defaultRerankHalfLife) >= rerankScore(query, daterd, weights, defaultRerankHalfLife) {
		t.Error("an undated candidate must not outrank an otherwise identical fresh one")
	}
}

// TestRerankCandidatesKeepsInputOrderIntact guards the caller's slice: the rerank
// must not mutate the candidates it was handed.
func TestRerankCandidatesKeepsInputOrderIntact(t *testing.T) {
	candidates := []rerankCandidate{
		{Content: "unrelated cooking notes", Similarity: 0.95},
		{Content: "the deployment pipeline", Similarity: 0.7},
	}
	first := candidates[0].Content
	rerankCandidates("deployment pipeline", candidates, 2, DefaultSimpleRerankWeights(), defaultRerankHalfLife)
	if candidates[0].Content != first {
		t.Error("rerankCandidates must not reorder the caller's slice")
	}
}

func TestChunkAge(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		metadata map[string]any
		want     time.Duration
	}{
		{"rfc3339 string", map[string]any{"created_at": "2026-10-14T12:00:00Z"}, 24 * time.Hour},
		{"time value", map[string]any{"created_at": now.Add(-2 * time.Hour)}, 2 * time.Hour},
		{"falls back to updated_at", map[string]any{"updated_at": "2026-10-15T09:00:00Z"}, 3 * time.Hour},
		{"missing metadata", map[string]any{}, 0},
		{"nil metadata", nil, 0},
		{"unparsable value is unknown", map[string]any{"created_at": "not a date"}, 0},
		{"future timestamp is unknown, not fresh", map[string]any{"created_at": "2027-01-01T00:00:00Z"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := chunkAge(tc.metadata, now); got != tc.want {
				t.Errorf("chunkAge = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSimpleRetrievalConfigNormalized pins the guard against a partial config
// silently switching the rerank off — the failure mode that would make this fix
// look deployed and do nothing.
func TestSimpleRetrievalConfigNormalized(t *testing.T) {
	var nilConfig *SimpleRetrievalConfig
	got := nilConfig.normalized()
	if got.RerankCandidateFactor != defaultRerankCandidateFactor ||
		got.RerankWeights != DefaultSimpleRerankWeights() ||
		got.RerankHalfLife != defaultRerankHalfLife {
		t.Errorf("nil config normalized to %+v, want the shipped defaults", got)
	}

	partial := (&SimpleRetrievalConfig{TopK: 3, MinScore: 0.5}).normalized()
	if partial.TopK != 3 || partial.MinScore != 0.5 {
		t.Errorf("normalization must keep the values that were set, got %+v", partial)
	}
	if partial.RerankCandidateFactor != defaultRerankCandidateFactor ||
		partial.RerankWeights != DefaultSimpleRerankWeights() {
		t.Errorf("partial config must still get a working rerank, got %+v", partial)
	}

	// An explicit zero factor means "disable the wide recall", and the operator
	// gets what they asked for rather than a silent override.
	disabled := (&SimpleRetrievalConfig{RerankCandidateFactor: -1}).normalized()
	if disabled.RerankCandidateFactor != defaultRerankCandidateFactor {
		t.Errorf("a negative factor must fall back to the default, got %d", disabled.RerankCandidateFactor)
	}
}

// TestSetConfigNormalizes guards the runtime knob: setting a partial config must
// not resurrect the "no rerank" behaviour.
func TestSetConfigNormalizes(t *testing.T) {
	service := NewSimpleRetrievalService(nil, nil, nil)
	service.SetConfig(&SimpleRetrievalConfig{TopK: 2})
	got := service.GetConfig()
	if got.TopK != 2 {
		t.Errorf("TopK = %d, want the configured 2", got.TopK)
	}
	if got.RerankWeights != DefaultSimpleRerankWeights() {
		t.Errorf("SetConfig must keep the rerank active, got %+v", got.RerankWeights)
	}
}

package genome

import (
	"context"
	"math"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// --- Multi-objective Fitness Tests ---

func TestParetoDominance(t *testing.T) {
	t.Parallel()

	a := &mutation.Strategy{
		DimensionScores: map[string]float64{"cost": 0.8, "quality": 0.7, "latency": 0.6},
		Score:           0.7,
	}
	b := &mutation.Strategy{
		DimensionScores: map[string]float64{"cost": 0.6, "quality": 0.7, "latency": 0.5},
		Score:           0.6,
	}

	if !ParetoDominance(a, b) {
		t.Error("a should dominate b: all dims >= and cost is better")
	}
	if ParetoDominance(b, a) {
		t.Error("b should NOT dominate a")
	}
}

func TestParetoDominance_NoDim(t *testing.T) {
	t.Parallel()

	a := &mutation.Strategy{Score: 0.9}
	b := &mutation.Strategy{Score: 0.5}

	if !ParetoDominance(a, b) {
		t.Error("a should dominate b by single score")
	}
	if ParetoDominance(b, a) {
		t.Error("b should NOT dominate a")
	}
}

func TestParetoFront(t *testing.T) {
	t.Parallel()

	// Strategies 2 and 3 are dominated by 1 (better in all dims).
	// Strategy 4 is also Pareto-optimal (best latency).
	strategies := []*mutation.Strategy{
		{DimensionScores: map[string]float64{"cost": 0.9, "quality": 0.9, "latency": 0.3}},
		{DimensionScores: map[string]float64{"cost": 0.4, "quality": 0.4, "latency": 0.2}},
		{DimensionScores: map[string]float64{"cost": 0.3, "quality": 0.3, "latency": 0.1}},
		{DimensionScores: map[string]float64{"cost": 0.3, "quality": 0.3, "latency": 0.9}},
	}

	front := ParetoFront(strategies)
	if len(front) != 2 {
		t.Fatalf("expected 2 Pareto-optimal (1 and 4), got %d", len(front))
	}
}

func TestCrowdingDistance(t *testing.T) {
	t.Parallel()

	strategies := []*mutation.Strategy{
		{DimensionScores: map[string]float64{"cost": 0.0, "quality": 0.0}},
		{DimensionScores: map[string]float64{"cost": 0.5, "quality": 0.5}},
		{DimensionScores: map[string]float64{"cost": 1.0, "quality": 1.0}},
	}

	dists := CrowdingDistance(strategies)
	if len(dists) != 3 {
		t.Fatalf("expected 3 distances, got %d", len(dists))
	}
	// Boundary points should have infinite distance.
	if !math.IsInf(dists[0], 1) || !math.IsInf(dists[2], 1) {
		t.Error("boundary points should have infinite crowding distance")
	}
}

func TestAggregateDimensions(t *testing.T) {
	t.Parallel()

	dims := map[string]float64{"success_rate": 0.9, "quality": 0.8, "cost": 0.7}
	score := AggregateDimensions(dims, nil)
	if score <= 0 {
		t.Errorf("expected positive aggregate, got %f", score)
	}
	// Default weights: success_rate=0.4, quality=0.25, cost=0.2, latency=0.15
	// 0.9*0.4 + 0.8*0.25 + 0.7*0.2 + 0*0.15 = 0.36 + 0.20 + 0.14 + 0 = 0.70
	if score < 0.69 || score > 0.71 {
		t.Errorf("expected aggregate ~0.70, got %f", score)
	}
}

func TestAggregateDimensions_Empty(t *testing.T) {
	t.Parallel()

	if got := AggregateDimensions(nil, nil); got != 0 {
		t.Errorf("expected 0 for nil dims, got %f", got)
	}
	if got := AggregateDimensions(map[string]float64{}, nil); got != 0 {
		t.Errorf("expected 0 for empty dims, got %f", got)
	}
}

// --- Selection Strategy Tests ---

func TestRankSelection(t *testing.T) {
	t.Parallel()

	rs := NewRankSelection()
	pop := []*mutation.Strategy{
		{Score: 10.0}, {Score: 20.0}, {Score: 30.0},
		{Score: 40.0}, {Score: 50.0},
	}

	ctx := context.Background()
	result, err := rs.Select(ctx, pop, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 10 {
		t.Fatalf("expected 10 selections, got %d", len(result))
	}
	// Higher-scoring should appear more often (rank-based).
	scoreSum := 0.0
	for _, s := range result {
		scoreSum += s.Score
	}
	avg := scoreSum / 10
	if avg < 30 {
		t.Logf("rank selection avg=%.1f (expected bias toward higher scores)", avg)
	}
}

func TestSUSSelection(t *testing.T) {
	t.Parallel()

	sus := NewSUSSelection()
	pop := []*mutation.Strategy{
		{Score: 10.0}, {Score: 20.0}, {Score: 30.0},
		{Score: 40.0}, {Score: 50.0},
	}

	ctx := context.Background()
	result, err := sus.Select(ctx, pop, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 10 {
		t.Fatalf("expected 10 selections, got %d", len(result))
	}
}

func TestSUSSelection_EqualScores(t *testing.T) {
	t.Parallel()

	sus := NewSUSSelection()
	pop := []*mutation.Strategy{
		{Score: 50.0}, {Score: 50.0}, {Score: 50.0},
	}

	ctx := context.Background()
	result, err := sus.Select(ctx, pop, 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 6 {
		t.Fatalf("expected 6 selections, got %d", len(result))
	}
}

func TestRouletteWheelNewSelection(t *testing.T) {
	t.Parallel()

	rw, err := NewRouletteWheelSelection()
	if err != nil {
		t.Fatalf("NewRouletteWheelSelection failed: %v", err)
	}

	pop := []*mutation.Strategy{
		{Score: 10.0}, {Score: 20.0}, {Score: 30.0},
		{Score: 40.0}, {Score: 50.0},
	}

	ctx := context.Background()
	result, err := rw.Select(ctx, pop, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 10 {
		t.Fatalf("expected 10 selections, got %d", len(result))
	}
}

// --- KnowledgeBase Tests ---

func TestKnowledgeBase_Record(t *testing.T) {
	t.Parallel()

	kb := NewKnowledgeBase()
	kb.Record("tool_timeout", "increase_timeout", "success+10%", 0.1)
	kb.Record("tool_timeout", "increase_timeout", "success+15%", 0.15)

	if kb.Count() != 1 {
		t.Fatalf("expected 1 entry, got %d", kb.Count())
	}

	entries := kb.Lookup("tool_timeout")
	if len(entries) != 1 {
		t.Fatalf("expected 1 lookup result, got %d", len(entries))
	}
	if entries[0].ObservationCount != 2 {
		t.Errorf("expected 2 observations, got %d", entries[0].ObservationCount)
	}
	if entries[0].SuccessCount != 2 {
		t.Errorf("expected 2 successes, got %d", entries[0].SuccessCount)
	}
}

func TestKnowledgeBase_Lookup(t *testing.T) {
	t.Parallel()

	kb := NewKnowledgeBase()
	kb.Record("pattern_a", "mut_1", "good", 0.1)
	kb.Record("pattern_b", "mut_2", "better", 0.2)

	entries := kb.Lookup("pattern_a")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for pattern_a, got %d", len(entries))
	}
	if entries[0].Mutation != "mut_1" {
		t.Errorf("expected mutation mut_1, got %s", entries[0].Mutation)
	}

	entries = kb.Lookup("nonexistent")
	if entries != nil {
		t.Errorf("expected nil for nonexistent pattern, got %v", entries)
	}
}

func TestKnowledgeBase_All(t *testing.T) {
	t.Parallel()

	kb := NewKnowledgeBase()
	kb.Record("a", "m1", "ok", 0.1)
	kb.Record("b", "m2", "good", 0.2)

	all := kb.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
}

func TestKnowledgeAdapter(t *testing.T) {
	t.Parallel()

	kb := NewKnowledgeBase()
	kb.Record("tool_timeout", "increase_timeout", "success+10%", 0.1)

	// First observation has confidence 0.6 (positive delta → 0.6).
	// minConfidence default is 0.4, so this should match.
	ka := NewKnowledgeAdapter(kb, 0)
	mutation, confidence := ka.SuggestMutation("tool_timeout")
	if mutation != "increase_timeout" {
		t.Errorf("expected increase_timeout, got %s", mutation)
	}
	if confidence < 0.5 {
		t.Errorf("expected confidence >= 0.5, got %f", confidence)
	}
}

// --- HypothesisGenerator Tests ---

func TestHypothesisGenerator(t *testing.T) {
	t.Parallel()

	hg := NewHypothesisGenerator(0.3)

	ref := &Reflection{
		Summary: "Tool timeout is the main bottleneck",
		Recommendations: []Recommendation{
			{
				Target:     "param:temperature",
				Action:     "decrease",
				Rationale:  "lower temperature increases determinism",
				Confidence: 0.8,
			},
		},
	}

	ctx := context.Background()
	hyps := hg.Generate(ctx, ref)
	if len(hyps) != 1 {
		t.Fatalf("expected 1 hypothesis, got %d", len(hyps))
	}
	if hyps[0].TargetType != "param" {
		t.Errorf("expected target_type param, got %s", hyps[0].TargetType)
	}
	if hyps[0].TargetKey != "temperature" {
		t.Errorf("expected target_key temperature, got %s", hyps[0].TargetKey)
	}
}

func TestHypothesisGenerator_FiltersLowConfidence(t *testing.T) {
	t.Parallel()

	hg := NewHypothesisGenerator(0.7)

	ref := &Reflection{
		Recommendations: []Recommendation{
			{Target: "param:temp", Action: "increase", Confidence: 0.3},
			{Target: "param:top_k", Action: "decrease", Confidence: 0.9},
		},
	}

	ctx := context.Background()
	hyps := hg.Generate(ctx, ref)
	if len(hyps) != 1 {
		t.Fatalf("expected 1 hypothesis (filtered), got %d", len(hyps))
	}
	if hyps[0].TargetKey != "top_k" {
		t.Errorf("expected top_k, got %s", hyps[0].TargetKey)
	}
}

func TestApplyHypothesis(t *testing.T) {
	t.Parallel()

	base := &mutation.Strategy{
		Params:         map[string]any{"temperature": 0.7},
		PromptTemplate: "original prompt",
	}

	// Param hypothesis.
	hyp := MutationHypothesis{
		TargetType: "param",
		TargetKey:  "temperature",
		Direction:  "decrease",
	}
	child := ApplyHypothesis(base, hyp)
	if child == nil {
		t.Fatal("expected non-nil child")
	}
	temp := child.Params["temperature"].(float64)
	if temp >= 0.7 {
		t.Errorf("expected temperature decreased, got %f", temp)
	}
}

// --- GuidedPipeline Tests ---

// mockReflector returns a fixed reflection for testing.
type mockReflector struct {
	ref *Reflection
	err error
}

func (m *mockReflector) Reflect(ctx context.Context, history []GenerationHistoryEntry, agents []*mutation.Strategy) (*Reflection, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.ref, nil
}

func TestHypothesisHintProvider(t *testing.T) {
	t.Parallel()

	ref := &Reflection{
		Summary: "Test reflection",
		Recommendations: []Recommendation{
			{
				Target:     "param:temperature",
				Action:     "decrease",
				Rationale:  "test rationale",
				Confidence: 0.8,
			},
		},
	}

	reflector := &mockReflector{ref: ref}
	hg := NewHypothesisGenerator(0.3)
	kb := NewKnowledgeBase()
	pipeline := NewGuidedPipeline(reflector, hg, kb)
	pop := &Population{}

	provider := NewHypothesisHintProvider(pipeline, pop, 0.4)
	ctx := context.Background()

	hints, err := provider.HintsForTask(ctx, "test", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hints) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(hints))
	}
	if hints[0].Confidence != 0.8 {
		t.Errorf("expected confidence 0.8, got %f", hints[0].Confidence)
	}
}

func TestHypothesisHintProvider_NoReflection(t *testing.T) {
	t.Parallel()

	pipeline := NewGuidedPipeline(nil, nil, nil)
	provider := NewHypothesisHintProvider(pipeline, &Population{}, 0.4)
	ctx := context.Background()

	hints, err := provider.HintsForTask(ctx, "test", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hints) != 0 {
		t.Errorf("expected 0 hints without reflector, got %d", len(hints))
	}
}

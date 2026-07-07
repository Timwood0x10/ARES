package adapter

import (
	"testing"
	"time"

	ares_evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/knowledge"
)

func TestFromStrategyNil(t *testing.T) {
	if obj := FromStrategy(nil, "test"); obj != nil {
		t.Fatal("expected nil for nil strategy")
	}
}

func TestFromStrategy(t *testing.T) {
	now := time.Now()
	s := &ares_evolution.Strategy{
		ID:                   "strat-123", // nolint:misspell // strategy abbreviation
		Name:                 "Optimized Redis Strategy",
		Version:              3,
		Score:                4.5,
		ParentID:             "strat-122", // nolint:misspell // strategy abbreviation
		StrategyMutationType: "param_tweak",
		MutationDesc:         "Increased timeout from 30s to 60s",
		PromptTemplate:       "You are an agent that...",
		CreatedAt:            now,
	}

	obj := FromStrategy(s, "evo-ns")
	if obj == nil {
		t.Fatal("expected non-nil object")
	}

	if obj.Type != knowledge.ObjectDecision {
		t.Errorf("expected type %q, got %q", knowledge.ObjectDecision, obj.Type)
	}
	if obj.Namespace != "evo-ns" {
		t.Errorf("expected namespace 'evo-ns', got %q", obj.Namespace)
	}
	if obj.Summary != "Optimized Redis Strategy" {
		t.Errorf("expected summary 'Optimized Redis Strategy', got %q", obj.Summary)
	}
	if obj.Confidence < 0.5 || obj.Confidence > 0.99 {
		t.Errorf("confidence %.2f out of expected range [0.5, 0.99] for score 4.5", obj.Confidence)
	}

	// Check tags.
	tagMap := make(map[string]bool)
	for _, tag := range obj.Tags {
		tagMap[tag] = true
	}
	if !tagMap["evolution"] || !tagMap["strategy"] || !tagMap["param_tweak"] {
		t.Errorf("expected tags [evolution, strategy, param_tweak], got %v", obj.Tags)
	}

	// Check metadata.
	if obj.Metadata == nil {
		t.Fatal("expected non-nil metadata")
	}
	if obj.Metadata["strategy_id"] != "strat-123" { // nolint:misspell // strategy abbreviation
		t.Errorf("expected strategy_id 'strat-123', got %v", obj.Metadata["strategy_id"])
	}
	if obj.Metadata["version"] != 3 {
		t.Errorf("expected version 3, got %v", obj.Metadata["version"])
	}
	if obj.Metadata["score"] != 4.5 {
		t.Errorf("expected score 4.5, got %v", obj.Metadata["score"])
	}
}

func TestFromStrategyEmptyName(t *testing.T) {
	s := &ares_evolution.Strategy{
		ID:                   "s1",
		Version:              1,
		Score:                0,
		StrategyMutationType: "initial",
		CreatedAt:            time.Now(),
	}
	obj := FromStrategy(s, "ns")
	if obj == nil {
		t.Fatal("expected non-nil object")
	}
	// Should use fallback summary "Strategy s1 (v1)".
	if obj.Summary != "Strategy s1 (v1)" {
		t.Errorf("expected fallback summary, got %q", obj.Summary)
	}
}

func TestFromStrategies(t *testing.T) {
	strategies := []*ares_evolution.Strategy{
		{ID: "s1", Version: 1, Score: 1.0, CreatedAt: time.Now()},
		{ID: "s2", Version: 2, Score: 2.0, CreatedAt: time.Now()},
		nil,
	}

	objs := FromStrategies(strategies, "ns")
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects (nil skipped), got %d", len(objs))
	}
}

func TestScoreToConfidence(t *testing.T) {
	tests := []struct {
		score float64
		want  float64
	}{
		{0, 0.5},
		{4.5, 0.9},  // ≈ 0.904
		{10, 0.99},  // clamped
		{-10, 0.1},  // clamped
		{-4.5, 0.1}, // ≈ 0.096
		{2.0, 0.73}, // ≈ 0.731
	}

	for _, tt := range tests {
		got := scoreToConfidence(tt.score)
		if got < tt.want-0.05 || got > tt.want+0.05 {
			t.Errorf("scoreToConfidence(%.1f) = %.2f, want ≈%.2f", tt.score, got, tt.want)
		}
	}
}

package knowledge

import (
	"math"
	"testing"
	"time"
)

func TestDefaultQualityGateConfig(t *testing.T) {
	c := DefaultQualityGateConfig()
	want := QualityGateConfig{
		MinExtraction:     0.5,
		MinConsistency:    0.5,
		MinFinalScore:     0.55,
		MaxFactsPerIngest: 50,
		EnableDedup:       true,
		DedupThreshold:    0.85,
	}
	if c != want {
		t.Errorf("DefaultQualityGateConfig() = %+v, want %+v", c, want)
	}
}

func TestComputeFinal(t *testing.T) {
	c := DefaultQualityGateConfig()

	t.Run("nil_quality_returns_zero", func(t *testing.T) {
		if got := c.ComputeFinal(nil); got != 0 {
			t.Errorf("ComputeFinal(nil) = %v, want 0", got)
		}
	})

	t.Run("weighted_sum", func(t *testing.T) {
		q := &Quality{
			ExtractionScore:  0.8,
			ConsistencyScore: 0.6,
			FreshnessScore:   1.0,
			UsageScore:       0.4,
		}
		// 0.4*0.8 + 0.3*0.6 + 0.2*1.0 + 0.1*0.4 = 0.32+0.18+0.2+0.04 = 0.74
		want := 0.4*0.8 + 0.3*0.6 + 0.2*1.0 + 0.1*0.4
		got := c.ComputeFinal(q)
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("ComputeFinal() = %v, want %v", got, want)
		}
	})

	t.Run("zero_quality_returns_zero", func(t *testing.T) {
		q := &Quality{}
		if got := c.ComputeFinal(q); got != 0 {
			t.Errorf("ComputeFinal(zero) = %v, want 0", got)
		}
	})
}

func TestEvaluate(t *testing.T) {
	c := DefaultQualityGateConfig()

	t.Run("nil_object_returns_nil", func(t *testing.T) {
		if got := c.Evaluate(nil); got != nil {
			t.Errorf("Evaluate(nil) = %v, want nil", got)
		}
	})

	t.Run("short_text_no_relations_no_evidence", func(t *testing.T) {
		obj := &KnowledgeObject{
			ID:         "short",
			Normalized: "brief", // len <= 20
			Summary:    "brief",
			CreatedAt:  time.Now(),
		}
		q := c.Evaluate(obj)
		if q == nil {
			t.Fatal("expected non-nil Quality")
		}
		// Base 0.5, no bonuses.
		if math.Abs(q.ExtractionScore-0.5) > 1e-9 {
			t.Errorf("ExtractionScore = %v, want 0.5", q.ExtractionScore)
		}
		if q.ConsistencyScore != 1.0 {
			t.Errorf("ConsistencyScore = %v, want 1.0", q.ConsistencyScore)
		}
		if q.FreshnessScore != 1.0 {
			t.Errorf("FreshnessScore = %v, want 1.0 (just created)", q.FreshnessScore)
		}
		if q.UsageScore != 0 {
			t.Errorf("UsageScore = %v, want 0", q.UsageScore)
		}
	})

	t.Run("long_text_with_relations_and_evidence", func(t *testing.T) {
		obj := &KnowledgeObject{
			ID:         "rich",
			Normalized: "this is a long normalized text exceeding twenty chars",
			Summary:    "rich object summary",
			CreatedAt:  time.Now(),
			Relations:  []Relation{{Predicate: "fixes", ObjectText: "auth bug"}},
			Evidence:   []Evidence{{Source: "git", Ref: "abc"}},
		}
		q := c.Evaluate(obj)
		if q == nil {
			t.Fatal("expected non-nil Quality")
		}
		// 0.5 base + 0.2 (long) + 0.2 (relations) + 0.1 (evidence) = 1.0 (capped).
		if math.Abs(q.ExtractionScore-1.0) > 1e-9 {
			t.Errorf("ExtractionScore = %v, want 1.0", q.ExtractionScore)
		}
		if q.FreshnessScore != 1.0 {
			t.Errorf("FreshnessScore = %v, want 1.0", q.FreshnessScore)
		}
	})

	t.Run("stale_object_low_freshness", func(t *testing.T) {
		obj := &KnowledgeObject{
			ID:         "stale",
			Normalized: "some long normalized content here",
			CreatedAt:  time.Now().Add(-60 * 24 * time.Hour), // > 30 days
		}
		q := c.Evaluate(obj)
		if q == nil {
			t.Fatal("expected non-nil Quality")
		}
		if q.FreshnessScore != 0.3 {
			t.Errorf("FreshnessScore = %v, want 0.3", q.FreshnessScore)
		}
	})

	t.Run("usage_score_from_metadata", func(t *testing.T) {
		obj := &KnowledgeObject{
			ID:         "used",
			Normalized: "some long normalized content here",
			CreatedAt:  time.Now(),
			Metadata:   map[string]any{"usage_count": float64(5)},
		}
		q := c.Evaluate(obj)
		if q == nil {
			t.Fatal("expected non-nil Quality")
		}
		// 5/10 = 0.5
		if math.Abs(q.UsageScore-0.5) > 1e-9 {
			t.Errorf("UsageScore = %v, want 0.5", q.UsageScore)
		}
	})

	t.Run("usage_score_capped_at_one", func(t *testing.T) {
		obj := &KnowledgeObject{
			ID:         "heavy",
			Normalized: "some long normalized content here",
			CreatedAt:  time.Now(),
			Metadata:   map[string]any{"usage_count": float64(50)},
		}
		q := c.Evaluate(obj)
		if q == nil {
			t.Fatal("expected non-nil Quality")
		}
		if q.UsageScore != 1.0 {
			t.Errorf("UsageScore = %v, want 1.0 (capped)", q.UsageScore)
		}
	})
}

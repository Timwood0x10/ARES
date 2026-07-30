package knowledge

import (
	"math"
	"time"
)

// QualityGateConfig configures the AKG quality gate.
type QualityGateConfig struct {
	MinExtraction     float64 `yaml:"min_extraction"`
	MinConsistency    float64 `yaml:"min_consistency"`
	MinFinalScore     float64 `yaml:"min_final_score"`
	MaxFactsPerIngest int     `yaml:"max_facts_per_ingest"`
	EnableDedup       bool    `yaml:"enable_dedup"`
	DedupThreshold    float64 `yaml:"dedup_threshold"`
}

// DefaultQualityGateConfig returns the 0.2.9 defaults (MinFinalScore 0.55
// replaces the old hardcoded 0.4).
func DefaultQualityGateConfig() QualityGateConfig {
	return QualityGateConfig{
		MinExtraction:     0.5,
		MinConsistency:    0.5,
		MinFinalScore:     0.55,
		MaxFactsPerIngest: 50,
		EnableDedup:       true,
		DedupThreshold:    0.85,
	}
}

// ComputeFinal folds a Quality into a single Confidence score using the
// weights 0.4*Extraction + 0.3*Consistency + 0.2*Freshness + 0.1*Usage.
// It returns 0 when q is nil.
func (c QualityGateConfig) ComputeFinal(q *Quality) float64 {
	if q == nil {
		return 0
	}
	return 0.4*q.ExtractionScore +
		0.3*q.ConsistencyScore +
		0.2*q.FreshnessScore +
		0.1*q.UsageScore
}

// Evaluate computes a Quality for an object from its content shape, relations,
// evidence, and freshness. ConsistencyScore defaults to 1.0 (store-layer dedup
// can lower it later). UsageScore is read from metadata["usage_count"]. It
// returns nil when obj is nil.
func (c QualityGateConfig) Evaluate(obj *KnowledgeObject) *Quality {
	if obj == nil {
		return nil
	}
	q := &Quality{}

	// ExtractionScore: based on content length, relations, and evidence.
	q.ExtractionScore = 0.5
	if len(obj.Normalized) > 20 {
		q.ExtractionScore += 0.2
	}
	if len(obj.Relations) > 0 {
		q.ExtractionScore += 0.2
	}
	if len(obj.Evidence) > 0 {
		q.ExtractionScore += 0.1
	}
	if q.ExtractionScore > 1 {
		q.ExtractionScore = 1
	}

	// ConsistencyScore: defaults to 1.0; store-layer dedup lowers it later.
	q.ConsistencyScore = 1.0

	// FreshnessScore: newer objects score higher.
	age := time.Since(obj.CreatedAt)
	switch {
	case age < 24*time.Hour:
		q.FreshnessScore = 1.0
	case age < 7*24*time.Hour:
		q.FreshnessScore = 0.8
	case age < 30*24*time.Hour:
		q.FreshnessScore = 0.5
	default:
		q.FreshnessScore = 0.3
	}

	// UsageScore: starts at 0, updated by feedback; normalized to [0, 1].
	q.UsageScore = 0
	if v, ok := obj.Metadata["usage_count"]; ok {
		if n, ok := v.(float64); ok && n > 0 {
			q.UsageScore = math.Min(1.0, n/10)
		}
	}

	return q
}

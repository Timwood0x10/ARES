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
	// RequireEvidence controls whether the quality gate rejects objects that
	// carry no evidence. It defaults to false for backward compatibility:
	// pre-0.3.4 stored objects and DistillBridge writes that predate the
	// evidence requirement are admitted as before. Once a deployment has
	// backfilled evidence on all write paths, set this to true to make the
	// gate fail-closed on un-evidenced facts.
	RequireEvidence bool `yaml:"require_evidence"`
}

// DefaultQualityGateConfig returns the 0.2.9 defaults (MinFinalScore 0.55
// replaces the old hardcoded 0.4). RequireEvidence defaults to false so
// existing deployments keep admitting objects without evidence until the
// operator explicitly opts in.
func DefaultQualityGateConfig() QualityGateConfig {
	return QualityGateConfig{
		MinExtraction:     0.5,
		MinConsistency:    0.5,
		MinFinalScore:     0.55,
		MaxFactsPerIngest: 50,
		EnableDedup:       true,
		DedupThreshold:    0.85,
		RequireEvidence:   false,
	}
}

// freshnessTiers maps an object's age to its freshness score, newest first.
//
// The tiers are a documented policy rather than a formula: freshness is a coarse
// signal, and a smooth decay would imply a precision nobody measured. They are
// shared by the write-time quality score and the query-time recency term, which
// is why they live in one place — two decay rules for one notion would rank the
// same object differently depending on which path asked.
var freshnessTiers = []struct {
	maxAge time.Duration
	score  float64
}{
	{24 * time.Hour, 1.0},
	{7 * 24 * time.Hour, 0.8},
	{30 * 24 * time.Hour, 0.5},
	{0, 0.3}, // 0 = the open-ended tier (anything older)
}

// FreshnessScore returns a [0,1] freshness score for a timestamp, newest highest.
//
// A zero timestamp means the column is NULL (unknown), NOT that the object is
// ancient: `time.Since(zero)` is a huge age that would land in the oldest tier and
// systematically bury every object stored before the timestamp was populated. An
// unknown timestamp therefore scores the neutral 0.5 — present, but not evidence
// either way.
func FreshnessScore(at time.Time) float64 {
	if at.IsZero() {
		return 0.5
	}
	age := time.Since(at)
	if age < 0 {
		// A future timestamp is clock skew, not a fresher-than-fresh object.
		return 1.0
	}
	for _, tier := range freshnessTiers {
		if tier.maxAge == 0 || age < tier.maxAge {
			return tier.score
		}
	}
	return freshnessTiers[len(freshnessTiers)-1].score
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
//
// When RequireEvidence is true and obj has no evidence, Evaluate returns nil:
// the caller (DistillBridge scoreQuality) treats a nil Quality as a rejection
// (Confidence stays 0, below MinFinalScore, so the object is never promoted).
//
// "Rejected" here means NEVER PROMOTED, not "never stored": the write path
// saves the object as a candidate before evaluating the gate, so an
// un-evidenced object still lands in the store (as StatusCandidate) and is
// simply never promoted to StatusActive — which keeps it out of default recall
// without destroying it. Callers that need "do not persist at all" must check
// the gate before Save.
//
// This is the fail-closed path: pre-0.3.4 objects with no evidence are still
// admitted when RequireEvidence is false (the default), preserving backward
// compatibility.
func (c QualityGateConfig) Evaluate(obj *KnowledgeObject) *Quality {
	if obj == nil {
		return nil
	}
	// Fail-closed: when RequireEvidence is on, an object with no evidence
	// is rejected. Objects already marked active (existing stored facts)
	// are exempt — the migration path is "active = grandfathered", not
	// "re-evaluate everything".
	if c.RequireEvidence && len(obj.Evidence) == 0 && obj.Status != StatusActive {
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

	// FreshnessScore: newer objects score higher (shared tier rule — see
	// FreshnessScore).
	q.FreshnessScore = FreshnessScore(obj.CreatedAt)

	// UsageScore: starts at 0, updated by feedback; normalized to [0, 1].
	q.UsageScore = 0
	if v, ok := obj.Metadata["usage_count"]; ok {
		if n, ok := v.(float64); ok && n > 0 {
			q.UsageScore = math.Min(1.0, n/10)
		}
	}

	return q
}

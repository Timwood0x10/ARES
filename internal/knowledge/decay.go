package knowledge

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// DecayConfig controls the staleness-downweight behaviour (M4).
type DecayConfig struct {
	// StaleAfter is the age at which an object starts losing FreshnessScore.
	// Objects older than this are downweighted but NOT deleted — the
	// knowledge stays traceable, only its recall priority drops. Zero
	// means no decay (every object keeps its original FreshnessScore).
	//
	// NOTE (0.3.4): ApplyDecay does NOT act on StaleAfter — freshness is
	// computed at query time by QualityGate.Evaluate from CreatedAt, so a
	// stale object already scores lower without a write-back. The field is
	// reserved for a future explicit downweight pass; today only ArchiveAfter
	// has an effect.
	StaleAfter time.Duration `yaml:"stale_after"`
	// ArchiveAfter is the age at which an object is marked StatusSuperseded
	// (archived, not deleted). Zero means no archival.
	ArchiveAfter time.Duration `yaml:"archive_after"`
}

// DefaultDecayConfig returns decay defaults that preserve pre-0.3.4
// behaviour: no decay, no archival. Operators opt in by setting non-zero
// durations.
func DefaultDecayConfig() DecayConfig {
	return DecayConfig{
		StaleAfter:   0,
		ArchiveAfter: 0,
	}
}

// TODO(tech-debt): ApplyDecay has no production caller — nothing schedules a
// decay pass — and BuildReport (below) has no producer at all: it is only
// constructed in tests, so TestAKG_BuildReport_ExposesCounters proves nothing
// about a real build. Wire both (or delete) before claiming M4.
//
// ApplyDecay archives stale objects by moving them to StatusSuperseded.
// It never deletes objects: the knowledge stays in the store, only its
// lifecycle status changes, preserving traceability (M4).
//
// Staleness downweighting (FreshnessScore reduction) is NOT done in-place
// here: the QualityGate's Evaluate already computes FreshnessScore from
// CreatedAt at query time, so stale objects naturally score lower without
// a write-back. This avoids the need to mutate stored objects (which the
// store's clone-on-read contract would discard) and keeps the decay pass
// idempotent.
//
// The function iterates objects returned by ListByStatus(StatusActive)
// and uses EffectiveObservedAt for age calculation (falling back to
// UpdatedAt then CreatedAt). Objects with no timestamp are skipped.
func ApplyDecay(ctx context.Context, store KnowledgeStore, namespace string, cfg DecayConfig, now time.Time) (DecayReport, error) {
	report := DecayReport{}
	if namespace == "" {
		// An empty namespace makes ListByStatus drop its namespace predicate
		// and scan EVERY tenant's objects, then fail to update any of them
		// (UpdateStatus is namespace-scoped). Fail closed, matching Save and
		// FindDuplicate, which both treat an empty namespace as a caller error
		// rather than "all tenants".
		return report, errors.New("apply decay: namespace is required")
	}
	if cfg.ArchiveAfter == 0 {
		// No archival configured — staleness is handled at query time by
		// FreshnessScore, so this is a genuine no-op.
		return report, nil
	}

	objects, err := store.ListByStatus(ctx, namespace, StatusActive, 0)
	if err != nil {
		return report, err
	}

	for _, obj := range objects {
		observed := obj.EffectiveObservedAt()
		if observed.IsZero() {
			continue
		}
		age := now.Sub(observed)
		if age > cfg.ArchiveAfter {
			if err := store.UpdateStatus(ctx, namespace, obj.ID, StatusSuperseded); err != nil {
				slog.Warn("decay: archive stale object",
					"object_id", obj.ID, "error", err)
				report.Errors++
				continue
			}
			report.Archived++
		}
	}

	return report, nil
}

// DecayReport summarises a decay pass for observability (M4).
type DecayReport struct {
	// Archived is the number of objects moved to StatusSuperseded.
	Archived int `json:"archived"`
	// Errors is the number of objects that failed to update.
	Errors int `json:"errors"`
}

// BuildReport records the outcome of a knowledge build (M4 observability).
// It exposes the counters that make a build auditable and retryable: how
// much was read, how much was new, how much was rejected, and how long it
// took. Callers can diff BuildReports across runs to detect regressions
// (e.g. a sudden spike in rejected objects).
type BuildReport struct {
	// ReadCount is the number of source records read.
	ReadCount int `json:"read_count"`
	// NewEntities is the number of new KnowledgeObjects created.
	NewEntities int `json:"new_entities"`
	// NewRelations is the number of new relations extracted.
	NewRelations int `json:"new_relations"`
	// Deduped is the number of objects marked superseded by dedup.
	Deduped int `json:"deduped"`
	// Rejected is the number of objects rejected by the quality gate.
	Rejected int `json:"rejected"`
	// Conflicts is the number of validation conflicts detected.
	Conflicts int `json:"conflicts"`
	// Duration is the total build time.
	Duration time.Duration `json:"duration"`
}

// ContentFingerprint computes a lightweight content hash for an object,
// used by incremental builds to detect whether the source has changed since
// the last build (M4). Two objects with the same fingerprint do not need to
// be re-processed.
func ContentFingerprint(obj *KnowledgeObject) string {
	if obj == nil {
		return ""
	}
	// Combine the stable fields that determine the object's knowledge
	// content: Summary + Normalized + Type + SourceVersion.
	data := obj.Summary + "|" + obj.Normalized + "|" + string(obj.Type) + "|" + obj.SourceVersion
	return hashString(data)
}

// hashString returns a short hex hash of s. Using a simple FNV-1a hash
// keeps it dependency-free and fast for the typical content sizes.
func hashString(s string) string {
	const (
		offsetBasis = 14695981039346656037
		fnvPrime    = 1099511628211
	)
	h := uint64(offsetBasis)
	for _, b := range []byte(s) {
		h ^= uint64(b)
		h *= fnvPrime
	}
	// Return 16 hex chars = a full 64-bit digest (the loop consumes all 64
	// bits of h). 32 bits would collide too readily for a content fingerprint.
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 0; i < 16; i++ {
		out[i] = hex[(h>>uint(60-4*i))&0xf]
	}
	return string(out)
}

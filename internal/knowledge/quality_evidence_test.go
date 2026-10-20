package knowledge_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TestQualityGate_RejectsNoEvidence pins the fail-closed path: when
// RequireEvidence is true, Evaluate returns nil for an object with no
// evidence, so the caller (DistillBridge scoreQuality) treats it as rejected
// (Confidence stays 0, below MinFinalScore, no promotion).
func TestQualityGate_RejectsNoEvidence(t *testing.T) {
	t.Parallel()

	cfg := knowledge.QualityGateConfig{
		RequireEvidence: true,
		MinFinalScore:   0.55,
	}

	obj := &knowledge.KnowledgeObject{
		ID:         "no_evidence",
		Summary:    "a fact without evidence",
		Normalized: "a fact without evidence",
		CreatedAt:  time.Now(),
	}

	q := cfg.Evaluate(obj)
	if q != nil {
		t.Errorf("Evaluate with RequireEvidence=true and no evidence should return nil, got %+v", q)
	}

	// Confidence from ComputeFinal(nil) = 0, below any positive MinFinalScore.
	if conf := cfg.ComputeFinal(q); conf != 0 {
		t.Errorf("ComputeFinal(nil) = %v, want 0", conf)
	}
}

// TestQualityGate_RequireEvidence_BackCompat pins that the default config
// (RequireEvidence=false) still admits objects without evidence, preserving
// pre-0.3.4 behaviour for existing deployments.
func TestQualityGate_RequireEvidence_BackCompat(t *testing.T) {
	t.Parallel()

	cfg := knowledge.DefaultQualityGateConfig()

	obj := &knowledge.KnowledgeObject{
		ID:         "legacy_no_evidence",
		Summary:    "an old fact with no evidence",
		Normalized: "an old fact with no evidence that is long enough",
		CreatedAt:  time.Now(),
	}

	q := cfg.Evaluate(obj)
	if q == nil {
		t.Fatal("default config should admit objects without evidence, got nil")
	}
	if q.ExtractionScore < 0.5 {
		t.Errorf("ExtractionScore = %v, want >= 0.5", q.ExtractionScore)
	}
}

// TestQualityGate_RequireEvidence_ActiveExempt pins that objects already
// marked StatusActive (existing stored facts) are exempt from the evidence
// requirement — the migration path is "active = grandfathered", not
// "re-evaluate everything".
func TestQualityGate_RequireEvidence_ActiveExempt(t *testing.T) {
	t.Parallel()

	cfg := knowledge.QualityGateConfig{
		RequireEvidence: true,
	}

	obj := &knowledge.KnowledgeObject{
		ID:         "active_fact",
		Status:     knowledge.StatusActive,
		Summary:    "a grandfathered active fact with no evidence",
		Normalized: "a grandfathered active fact with no evidence long enough",
		CreatedAt:  time.Now(),
	}

	q := cfg.Evaluate(obj)
	if q == nil {
		t.Error("active objects should be exempt from RequireEvidence, got nil")
	}
}

// TestQualityGate_RequireEvidence_WithEvidenceAdmitted pins that an object
// WITH evidence is admitted even when RequireEvidence is true.
func TestQualityGate_RequireEvidence_WithEvidenceAdmitted(t *testing.T) {
	t.Parallel()

	cfg := knowledge.QualityGateConfig{
		RequireEvidence: true,
	}

	obj := &knowledge.KnowledgeObject{
		ID:         "evidenced_fact",
		Summary:    "a fact with proper evidence",
		Normalized: "a fact with proper evidence that is long enough",
		CreatedAt:  time.Now(),
		Evidence: []knowledge.Evidence{{
			Source:    "postgres://orders/2024-01",
			Ref:       "row-42",
			Weight:    0.9,
			Timestamp: time.Now(),
		}},
	}

	q := cfg.Evaluate(obj)
	if q == nil {
		t.Fatal("object with evidence should be admitted, got nil")
	}
	if q.ExtractionScore < 0.79 {
		t.Errorf("ExtractionScore = %v, want >= 0.79 (base + long + evidence)", q.ExtractionScore)
	}
}

// TestEvidenceRef_ZeroValueBackCompat pins backward compatibility for
// pre-0.3.4 evidence JSON: a record without the span key decodes with a nil
// Span (it is a pointer, so "absent" and "empty" stay distinguishable), and a
// nil Span serialises WITHOUT a span key rather than "span":{} — omitempty
// does not omit a zero struct, which is why Span is a pointer.
func TestEvidenceRef_ZeroValueBackCompat(t *testing.T) {
	t.Parallel()

	legacy := `{"source":"git","ref":"abc123","weight":0.9,"timestamp":"2025-01-01T00:00:00Z"}`

	var ev knowledge.Evidence
	if err := json.Unmarshal([]byte(legacy), &ev); err != nil {
		t.Fatalf("unmarshal legacy evidence: %v", err)
	}
	if ev.Span != nil {
		t.Errorf("evidence without a span key must decode with a nil Span, got %+v", ev.Span)
	}
	if ev.Source != "git" || ev.Ref != "abc123" {
		t.Errorf("legacy fields lost in decode: %+v", ev)
	}

	raw, err := json.Marshal(knowledge.Evidence{Source: "git", Ref: "abc123"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "span") {
		t.Errorf("nil Span must not serialise, got %s", raw)
	}
}

// TestEvidenceRef_FullRoundTrip pins that a fully-populated EvidenceRef
// survives a JSON round-trip (not just that the literal was constructed).
func TestEvidenceRef_FullRoundTrip(t *testing.T) {
	t.Parallel()

	ev := knowledge.Evidence{
		Source:    "file://README.md",
		Ref:       "line-42",
		Weight:    0.95,
		Timestamp: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Span: &knowledge.EvidenceRef{
			StartLine:   42,
			EndLine:     45,
			StartOffset: 1200,
			EndOffset:   1400,
			Text:        "AKG provides knowledge graph without LLMs",
		},
	}

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back knowledge.Evidence
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Span == nil {
		t.Fatal("Span lost in JSON round-trip")
	}
	if back.Span.StartLine != 42 || back.Span.EndLine != 45 {
		t.Errorf("Span lines = %d/%d, want 42/45", back.Span.StartLine, back.Span.EndLine)
	}
	if back.Span.StartOffset != 1200 || back.Span.EndOffset != 1400 {
		t.Errorf("Span offsets = %d/%d, want 1200/1400", back.Span.StartOffset, back.Span.EndOffset)
	}
	if back.Span.Text != "AKG provides knowledge graph without LLMs" {
		t.Errorf("Span text = %q, want the canonical excerpt", back.Span.Text)
	}
}

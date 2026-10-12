package knowledge_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TestKnowledgeObjectSchema_RoundTripVersioned pins that a fully-populated
// 0.3.4 object (schema version + all temporal fields) survives a JSON
// round-trip byte-for-byte in the fields that matter.
func TestKnowledgeObjectSchema_RoundTripVersioned(t *testing.T) {
	t.Parallel()

	observed := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)

	in := &knowledge.KnowledgeObject{
		ID:            "obj_temporal_1",
		Type:          knowledge.ObjectMemory,
		Summary:       "user prefers Rust",
		SchemaVersion: knowledge.CurrentSchemaVersion,
		ValidFrom:     &from,
		ValidUntil:    &until,
		ObservedAt:    &observed,
		SourceVersion: "etag-abc123",
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out knowledge.KnowledgeObject
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.SchemaVersion != knowledge.CurrentSchemaVersion {
		t.Errorf("SchemaVersion: got %d want %d", out.SchemaVersion, knowledge.CurrentSchemaVersion)
	}
	if out.ValidFrom == nil || !out.ValidFrom.Equal(from) {
		t.Errorf("ValidFrom: got %v want %s", out.ValidFrom, from)
	}
	if out.ValidUntil == nil || !out.ValidUntil.Equal(until) {
		t.Errorf("ValidUntil: got %v want %s", out.ValidUntil, until)
	}
	if out.ObservedAt == nil || !out.ObservedAt.Equal(observed) {
		t.Errorf("ObservedAt: got %v want %s", out.ObservedAt, observed)
	}
	if out.SourceVersion != "etag-abc123" {
		t.Errorf("SourceVersion: got %q want %q", out.SourceVersion, "etag-abc123")
	}
}

// TestKnowledgeObject_OldJSONDecodesWithZeroTemporal pins backward
// compatibility: a pre-0.3.4 record (no schema_version / temporal keys)
// decodes with zero temporal fields and legacy behaviour — SchemaVersion 0,
// always valid, observed-at falls back to the stored timestamps.
func TestKnowledgeObject_OldJSONDecodesWithZeroTemporal(t *testing.T) {
	t.Parallel()

	// A record as written by a pre-0.3.4 build: no temporal keys at all.
	legacy := `{
		"id": "legacy_1",
		"type": "memory",
		"summary": "old fact",
		"confidence": 0.9,
		"version": 1,
		"created_at": "2025-01-02T03:04:05Z",
		"updated_at": "2025-02-03T04:05:06Z"
	}`

	var out knowledge.KnowledgeObject
	if err := json.Unmarshal([]byte(legacy), &out); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}

	if out.SchemaVersion != 0 {
		t.Errorf("legacy SchemaVersion: got %d want 0", out.SchemaVersion)
	}
	if out.ValidFrom != nil || out.ValidUntil != nil || out.ObservedAt != nil {
		t.Errorf("legacy temporal fields should be nil, got from=%v until=%v observed=%v",
			out.ValidFrom, out.ValidUntil, out.ObservedAt)
	}
	if out.SourceVersion != "" {
		t.Errorf("legacy SourceVersion: got %q want empty", out.SourceVersion)
	}

	// Legacy object is always valid (no bounds) — pre-0.3.4 behaviour.
	if !out.IsValidAt(time.Now()) {
		t.Error("legacy object should be valid at any instant")
	}

	// EffectiveObservedAt falls back to UpdatedAt when ObservedAt is unset.
	want := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	if got := out.EffectiveObservedAt(); !got.Equal(want) {
		t.Errorf("EffectiveObservedAt fallback: got %s want %s (UpdatedAt)", got, want)
	}
}

// TestKnowledgeFact_TemporalStatus exercises IsValidAt bounds and the
// EffectiveObservedAt fallback chain.
func TestKnowledgeFact_TemporalStatus(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	zeroTime := time.Time{}

	tests := []struct {
		name string
		obj  knowledge.KnowledgeObject
		at   time.Time
		want bool
	}{
		{
			name: "within bounds",
			obj:  knowledge.KnowledgeObject{ValidFrom: &from, ValidUntil: &until},
			at:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "before ValidFrom",
			obj:  knowledge.KnowledgeObject{ValidFrom: &from, ValidUntil: &until},
			at:   time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "exactly at ValidFrom is inclusive",
			obj:  knowledge.KnowledgeObject{ValidFrom: &from, ValidUntil: &until},
			at:   from,
			want: true,
		},
		{
			name: "exactly at ValidUntil is exclusive",
			obj:  knowledge.KnowledgeObject{ValidFrom: &from, ValidUntil: &until},
			at:   until,
			want: false,
		},
		{
			name: "no upper bound",
			obj:  knowledge.KnowledgeObject{ValidFrom: &from},
			at:   time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "no bounds at all (legacy)",
			obj:  knowledge.KnowledgeObject{},
			at:   time.Now(),
			want: true,
		},
		{
			name: "zero-time pointers impose no bound",
			obj:  knowledge.KnowledgeObject{ValidFrom: &zeroTime, ValidUntil: &zeroTime},
			at:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			want: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.obj.IsValidAt(tc.at); got != tc.want {
				t.Errorf("IsValidAt(%s) = %v, want %v", tc.at, got, tc.want)
			}
		})
	}
}

// TestKnowledgeObject_EffectiveObservedAtPrecedence pins the fallback order:
// ObservedAt > UpdatedAt > CreatedAt.
func TestKnowledgeObject_EffectiveObservedAtPrecedence(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		obj  knowledge.KnowledgeObject
		want time.Time
	}{
		{
			name: "ObservedAt wins",
			obj:  knowledge.KnowledgeObject{CreatedAt: created, UpdatedAt: updated, ObservedAt: &observed},
			want: observed,
		},
		{
			name: "UpdatedAt when no ObservedAt",
			obj:  knowledge.KnowledgeObject{CreatedAt: created, UpdatedAt: updated},
			want: updated,
		},
		{
			name: "CreatedAt when only CreatedAt",
			obj:  knowledge.KnowledgeObject{CreatedAt: created},
			want: created,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.obj.EffectiveObservedAt(); !got.Equal(tc.want) {
				t.Errorf("EffectiveObservedAt() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestStampWriteSchema pins the write-path stamping contract: zero-version
// objects are stamped with CurrentSchemaVersion, already-versioned objects keep
// their provenance untouched, and nil entries are skipped without panicking.
func TestStampWriteSchema(t *testing.T) {
	t.Parallel()

	fresh := &knowledge.KnowledgeObject{ID: "fresh"}
	imported := &knowledge.KnowledgeObject{ID: "imported", SchemaVersion: 99}

	knowledge.StampWriteSchema(fresh, nil, imported)

	if fresh.SchemaVersion != knowledge.CurrentSchemaVersion {
		t.Errorf("fresh object: got SchemaVersion %d want %d",
			fresh.SchemaVersion, knowledge.CurrentSchemaVersion)
	}
	if imported.SchemaVersion != 99 {
		t.Errorf("imported object provenance clobbered: got %d want 99",
			imported.SchemaVersion)
	}
}

// TestMigrateObject covers the read-side schema upgrade: a legacy (version 0)
// record is stamped to the current version with its temporal semantics intact,
// a current-version record is left untouched, and a forward-dated record from a
// future build is refused rather than silently misread.
func TestMigrateObject(t *testing.T) {
	t.Parallel()

	t.Run("legacy zero migrates to current and stays always-valid", func(t *testing.T) {
		t.Parallel()
		obj := &knowledge.KnowledgeObject{ID: "legacy"}
		if err := knowledge.MigrateObject(obj); err != nil {
			t.Fatalf("MigrateObject: %v", err)
		}
		if obj.SchemaVersion != knowledge.CurrentSchemaVersion {
			t.Errorf("SchemaVersion: got %d want %d", obj.SchemaVersion, knowledge.CurrentSchemaVersion)
		}
		if !obj.IsValidAt(time.Now()) {
			t.Error("migrated legacy object should remain always valid")
		}
	})

	t.Run("current version untouched", func(t *testing.T) {
		t.Parallel()
		obj := &knowledge.KnowledgeObject{ID: "cur", SchemaVersion: knowledge.CurrentSchemaVersion}
		if err := knowledge.MigrateObject(obj); err != nil {
			t.Fatalf("MigrateObject: %v", err)
		}
		if obj.SchemaVersion != knowledge.CurrentSchemaVersion {
			t.Errorf("SchemaVersion changed: got %d want %d", obj.SchemaVersion, knowledge.CurrentSchemaVersion)
		}
	})

	t.Run("future version refused", func(t *testing.T) {
		t.Parallel()
		obj := &knowledge.KnowledgeObject{ID: "future", SchemaVersion: knowledge.CurrentSchemaVersion + 1}
		if err := knowledge.MigrateObject(obj); err == nil {
			t.Error("expected error for a version newer than this build supports")
		}
	})

	t.Run("nil is a no-op", func(t *testing.T) {
		t.Parallel()
		if err := knowledge.MigrateObject(nil); err != nil {
			t.Errorf("MigrateObject(nil): %v", err)
		}
	})
}

// TestKnowledgeObject_ZeroTemporalOmittedFromJSON pins the WIRE contract for
// unset temporal metadata: a zero-value object must not serialise the
// 0001-01-01 sentinel. encoding/json's omitempty does not omit a zero struct,
// so the fields are pointers; a value time.Time field would have emitted
// "0001-01-01T00:00:00Z" on every object, making "unset" indistinguishable from
// "the epoch" for external consumers (export/import, other services).
func TestKnowledgeObject_ZeroTemporalOmittedFromJSON(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(&knowledge.KnowledgeObject{ID: "zero", Summary: "no temporal"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"valid_from", "valid_until", "observed_at"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("zero-value object JSON must omit %q, got %s", key, raw)
		}
	}

	// A set bound must still be present and round-trip.
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	set, err := json.Marshal(&knowledge.KnowledgeObject{ID: "set", ValidFrom: &from})
	if err != nil {
		t.Fatalf("marshal set: %v", err)
	}
	if !strings.Contains(string(set), `"valid_from":"2026-10-01T00:00:00Z"`) {
		t.Errorf("set ValidFrom must serialise, got %s", set)
	}

	var back knowledge.KnowledgeObject
	if err := json.Unmarshal(set, &back); err != nil {
		t.Fatalf("unmarshal set: %v", err)
	}
	if back.ValidFrom == nil || !back.ValidFrom.Equal(from) {
		t.Errorf("ValidFrom round-trip: got %v want %s", back.ValidFrom, from)
	}
	if back.ValidUntil != nil || back.ObservedAt != nil {
		t.Errorf("unset bounds must stay nil after round-trip, got %v / %v", back.ValidUntil, back.ObservedAt)
	}
}

// TestSchemaMigration_MissingVersionTreatedAsBaseline pins that a record
// decoded from JSON with no schema_version field (as written by any pre-0.3.4
// build) is treated as a baseline (version 0) record: it migrates to the
// current version without error, its temporal fields stay nil (legacy
// behaviour), and IsValidAt remains true for any instant.
func TestSchemaMigration_MissingVersionTreatedAsBaseline(t *testing.T) {
	t.Parallel()

	// Simulates a record written before 0.3.4: no schema_version key at all.
	legacyJSON := `{
		"id": "pre_034",
		"type": "memory",
		"summary": "fact from before schema versioning",
		"confidence": 0.85,
		"version": 1,
		"created_at": "2025-06-01T00:00:00Z",
		"updated_at": "2025-06-02T00:00:00Z"
	}`

	var obj knowledge.KnowledgeObject
	if err := json.Unmarshal([]byte(legacyJSON), &obj); err != nil {
		t.Fatalf("unmarshal legacy JSON: %v", err)
	}

	// A missing schema_version field decodes as 0 — the baseline sentinel.
	if obj.SchemaVersion != 0 {
		t.Fatalf("missing schema_version should decode as 0, got %d", obj.SchemaVersion)
	}

	// Migration upgrades 0 → current without error and preserves legacy semantics.
	if err := knowledge.MigrateObject(&obj); err != nil {
		t.Fatalf("MigrateObject: %v", err)
	}
	if obj.SchemaVersion != knowledge.CurrentSchemaVersion {
		t.Errorf("after migration: got SchemaVersion %d want %d",
			obj.SchemaVersion, knowledge.CurrentSchemaVersion)
	}

	// Temporal fields stay nil: pre-0.3.4 records had no temporal metadata.
	if obj.ValidFrom != nil || obj.ValidUntil != nil || obj.ObservedAt != nil {
		t.Errorf("migrated legacy object should have nil temporal fields, got from=%v until=%v observed=%v",
			obj.ValidFrom, obj.ValidUntil, obj.ObservedAt)
	}
	if obj.SourceVersion != "" {
		t.Errorf("migrated legacy SourceVersion: got %q want empty", obj.SourceVersion)
	}

	// Legacy object is always valid — pre-0.3.4 behaviour.
	if !obj.IsValidAt(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("migrated legacy object should be valid at any instant")
	}
}

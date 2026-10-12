package sqlitestore_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Timwood0x10/ares/internal/knowledge"
	sqlitestore "github.com/Timwood0x10/ares/internal/knowledge/store/sqlite"
)

// newStore opens a throwaway file-backed SQLite store through the exported
// constructor so these black-box tests exercise the real schema migrations and
// Save/Get round-trip without reaching into package internals.
func newStore(t *testing.T) *sqlitestore.Store {
	t.Helper()

	f, err := os.CreateTemp("", "akf-sqlite-temporal-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	path := f.Name()
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(path) })

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s, err := sqlitestore.NewWithDB(db)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	return s
}

// TestSaveGet_TemporalFieldsRoundTrip pins that the 0.3.4 schema-version and
// temporal columns survive a Save/Get round-trip through SQLite, including the
// NULL/empty handling that maps unset bounds back to a zero time.
func TestSaveGet_TemporalFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	// Whole-second UTC times: SQLite persists RFC3339, which drops sub-second.
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	obj := &knowledge.KnowledgeObject{
		Namespace:     "default",
		ID:            "temporal1",
		Type:          knowledge.ObjectMemory,
		Summary:       "user prefers Rust",
		Normalized:    "user prefers Rust",
		Confidence:    0.9,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		SchemaVersion: knowledge.CurrentSchemaVersion,
		ValidFrom:     &from,
		ValidUntil:    &until,
		ObservedAt:    &observed,
		SourceVersion: "etag-abc123",
	}
	if err := s.Save(ctx, obj); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, "default", "temporal1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SchemaVersion != knowledge.CurrentSchemaVersion {
		t.Errorf("SchemaVersion: got %d want %d", got.SchemaVersion, knowledge.CurrentSchemaVersion)
	}
	if got.ValidFrom == nil || !got.ValidFrom.Equal(from) {
		t.Errorf("ValidFrom: got %v want %s", got.ValidFrom, from)
	}
	if got.ValidUntil == nil || !got.ValidUntil.Equal(until) {
		t.Errorf("ValidUntil: got %v want %s", got.ValidUntil, until)
	}
	if got.ObservedAt == nil || !got.ObservedAt.Equal(observed) {
		t.Errorf("ObservedAt: got %v want %s", got.ObservedAt, observed)
	}
	if got.SourceVersion != "etag-abc123" {
		t.Errorf("SourceVersion: got %q want %q", got.SourceVersion, "etag-abc123")
	}
}

// TestSaveGet_UnsetTemporalStaysZero pins that an object saved without temporal
// bounds reads back with zero times (not a sentinel epoch) and remains always
// valid — the pre-0.3.4 behaviour preserved through the nullable columns.
func TestSaveGet_UnsetTemporalStaysZero(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	obj := &knowledge.KnowledgeObject{
		Namespace:     "default",
		ID:            "notemporal",
		Type:          knowledge.ObjectMemory,
		Summary:       "no bounds",
		Confidence:    0.5,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		SchemaVersion: knowledge.CurrentSchemaVersion,
	}
	if err := s.Save(ctx, obj); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, "default", "notemporal")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ValidFrom != nil || got.ValidUntil != nil || got.ObservedAt != nil {
		t.Errorf("unset temporal fields should read back nil, got from=%v until=%v observed=%v",
			got.ValidFrom, got.ValidUntil, got.ObservedAt)
	}
	if !got.IsValidAt(time.Now()) {
		t.Error("object with no bounds should be always valid")
	}
}

// TestGet_LegacyRowMigratedOnRead pins the read-side migration: a row persisted
// with schema_version 0 (a pre-0.3.4 record) is stamped to the current version
// when read back, without touching its data.
func TestGet_LegacyRowMigratedOnRead(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	// SchemaVersion left at its zero value simulates a pre-0.3.4 write: the
	// store persists schema_version 0.
	legacy := &knowledge.KnowledgeObject{
		Namespace:  "default",
		ID:         "legacyrow",
		Type:       knowledge.ObjectMemory,
		Summary:    "old fact",
		Confidence: 0.7,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.Save(ctx, legacy); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, "default", "legacyrow")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SchemaVersion != knowledge.CurrentSchemaVersion {
		t.Errorf("legacy row not migrated on read: got SchemaVersion %d want %d",
			got.SchemaVersion, knowledge.CurrentSchemaVersion)
	}
	if !got.IsValidAt(time.Now()) {
		t.Error("migrated legacy row should be always valid")
	}
}

// TestRead_NewerSchemaVersionFailsClosedAndSkips pins the blast-radius
// contract for a record written by a NEWER build: a single-record Get fails
// closed with a distinguishable error, but a list query SKIPS that one row and
// still serves the rest — one unreadable record must not take down the corpus.
func TestRead_NewerSchemaVersionFailsClosedAndSkips(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	ctx := context.Background()

	save := func(obj *knowledge.KnowledgeObject) {
		t.Helper()
		if err := s.Save(ctx, obj); err != nil {
			t.Fatalf("Save(%s): %v", obj.ID, err)
		}
	}
	save(&knowledge.KnowledgeObject{
		Namespace: "default", ID: "future", Type: knowledge.ObjectMemory,
		Summary: "from a newer build", Confidence: 0.9,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		SchemaVersion: knowledge.CurrentSchemaVersion + 1,
	})
	save(&knowledge.KnowledgeObject{
		Namespace: "default", ID: "good", Type: knowledge.ObjectMemory,
		Summary: "readable", Confidence: 0.9,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		SchemaVersion: knowledge.CurrentSchemaVersion,
	})

	// Single-record read: fail closed, with an error callers can classify.
	if _, err := s.Get(ctx, "default", "future"); !errors.Is(err, knowledge.ErrSchemaVersionTooNew) {
		t.Errorf("Get(newer schema version) = %v, want ErrSchemaVersionTooNew", err)
	}

	// List read: the unreadable row is skipped, the readable one is served.
	got, err := s.Query(ctx, knowledge.Query{Namespace: "default"})
	if err != nil {
		t.Fatalf("Query must not fail because one row has a newer schema version: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		ids := make([]string, 0, len(got))
		for _, o := range got {
			ids = append(ids, o.ID)
		}
		t.Errorf("Query returned %v, want just the readable row", ids)
	}
}

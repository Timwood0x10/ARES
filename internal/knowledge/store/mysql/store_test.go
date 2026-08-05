package mysqlstore

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

func TestMarshalQuality_Nil(t *testing.T) {
	if got := marshalQuality(nil); got != "" {
		t.Errorf("expected empty for nil quality, got %q", got)
	}
}

func TestMarshalQuality_RoundTrip(t *testing.T) {
	q := &knowledge.Quality{ExtractionScore: 0.8, ConsistencyScore: 0.9}
	s := marshalQuality(q)
	var got knowledge.Quality
	if err := jsonUnmarshalString(s, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ExtractionScore != 0.8 || got.ConsistencyScore != 0.9 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestMarshalRelations_Empty(t *testing.T) {
	if got := marshalRelations(nil); got != "" {
		t.Errorf("expected empty for nil relations, got %q", got)
	}
	if got := marshalRelations([]knowledge.Relation{}); got != "" {
		t.Errorf("expected empty for empty relations, got %q", got)
	}
}

func TestMarshalRelations_RoundTrip(t *testing.T) {
	rels := []knowledge.Relation{{From: "a", To: "b", Name: knowledge.RelCalls, Score: 0.5}}
	s := marshalRelations(rels)
	var got []knowledge.Relation
	if err := jsonUnmarshalString(s, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].From != "a" || got[0].To != "b" || got[0].Name != knowledge.RelCalls {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

// jsonUnmarshalString is a tiny helper so the test does not need to repeat the
// []byte cast for every assertion.
func jsonUnmarshalString(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}

func TestFlexTime_Scan(t *testing.T) {
	cases := []struct {
		name string
		src  any
		want time.Time
	}{
		{"nil", nil, time.Time{}},
		{"time", time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC), time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)},
		{"rfc3339", "2026-07-30T12:00:00Z", time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)},
		{"mysql_datetime", []byte("2026-07-30 12:00:00"), time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)},
		{"mysql_datetime_millis", "2026-07-30 12:00:00.123", time.Date(2026, 7, 30, 12, 0, 0, 123000000, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f flexTime
			if err := f.Scan(tc.src); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			got := time.Time(f)
			if !got.Equal(tc.want) {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestFlexTime_ScanUnsupported(t *testing.T) {
	var f flexTime
	if err := f.Scan(42); err == nil {
		t.Error("expected error for unsupported type int")
	}
}

func TestFlexTime_ScanBadString(t *testing.T) {
	var f flexTime
	if err := f.Scan("not-a-date"); err == nil {
		t.Error("expected error for unparseable string")
	}
}

func TestHybridConditions_NamespaceTypesStatus(t *testing.T) {
	where, args := hybridConditions(
		"tenant-a",
		[]knowledge.ObjectType{knowledge.ObjectDecision},
		[]knowledge.ObjectStatus{knowledge.StatusActive},
	)
	if where == "" {
		t.Fatal("expected non-empty WHERE clause")
	}
	// 1 namespace + 1 type + 1 status arg ("active"; the empty-status branch
	// is a literal fragment, not a placeholder) = 3.
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d (%v)", len(args), args)
	}
}

func TestHybridConditions_DefaultActiveStatus(t *testing.T) {
	where, args := hybridConditions("", nil, nil)
	// Empty statuses default to [StatusActive], so the helper emits a status
	// group with one placeholder rather than a malformed empty group.
	if where == "" {
		t.Fatal("expected non-empty WHERE clause even with empty inputs")
	}
	if len(args) != 1 {
		t.Fatalf("expected 1 arg (default active status), got %d (%v)", len(args), args)
	}
}

func TestBuildConditions_AllFilters(t *testing.T) {
	conds, args := buildConditions(
		"tenant-b",
		[]knowledge.ObjectType{knowledge.ObjectCode, knowledge.ObjectDocument},
		[]string{"redis", "cache"},
		0,
	)
	if len(conds) != 3 {
		t.Fatalf("expected 3 condition fragments, got %d", len(conds))
	}
	// 1 namespace + 2 types + 2 tags = 5.
	if len(args) != 5 {
		t.Fatalf("expected 5 args, got %d", len(args))
	}
}

func TestNew_EmptyDriver(t *testing.T) {
	if _, err := New("", "user:pass@/db"); err == nil {
		t.Error("expected error for empty driver name")
	}
}

func TestNewWithDB_Nil(t *testing.T) {
	if _, err := NewWithDB(nil); err == nil {
		t.Error("expected error for nil db")
	}
}

func TestClose_NilSafe(t *testing.T) {
	s := &Store{db: nil}
	if err := s.Close(); err != nil {
		t.Errorf("expected nil error closing nil-db store, got %v", err)
	}
}

// TestStore_ImplementsKnowledgeStore is a compile-time guard: adding a method
// to the interface without a MySQL implementation becomes a build break here.
func TestStore_ImplementsKnowledgeStore(t *testing.T) {
	var _ knowledge.KnowledgeStore = (*Store)(nil)
	// Touch sql.NullTime import to keep the linter happy across driver configs.
	_ = sql.NullTime{}
}

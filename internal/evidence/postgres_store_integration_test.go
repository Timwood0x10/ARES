package evidence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// newMockPostgresStore creates a PostgresStore backed by sqlmock for
// integration-style tests of the SQL layer without a live database.
func newMockPostgresStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &PostgresStore{db: db}, mock
}

// ── PostgresStore.Append ────────────────────────────────────────────────────

func TestPostgresStoreAppend_Success(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO evidence_records").
		WithArgs(sqlmock.AnyArg(), "test-src", "fitness", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(0)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	e := NewEvidence("test-src", KindFitness, map[string]any{"value": 0.9})
	if err := store.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStoreAppend_WithTTL(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO evidence_records").
		WithArgs(sqlmock.AnyArg(), "src", "knowledge", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(300)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	e := NewEvidence("src", KindKnowledge, map[string]any{"k": "v"}, WithTTL(5*time.Minute))
	if err := store.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStoreAppend_GeneratedID(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	e := Evidence{
		Source:    "gen-src",
		Kind:      KindFitness,
		Timestamp: time.Unix(1700000000, 0),
		Payload:   json.RawMessage(`{"value":1}`),
	}
	expectedID := generatedEvidenceID(e)

	mock.ExpectExec("INSERT INTO evidence_records").
		WithArgs(expectedID, "gen-src", "fitness", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(0)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := store.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStoreAppend_DBError(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO evidence_records").
		WillReturnError(context.DeadlineExceeded)

	e := NewEvidence("src", KindFitness, nil)
	if err := store.Append(ctx, e); err == nil {
		t.Error("expected error from DB failure")
	}
}

// ── PostgresStore.Query ─────────────────────────────────────────────────────

func TestPostgresStoreQuery_NoFilters(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "source", "kind", "payload", "metadata", "ts"}).
		AddRow("e1", "src", "fitness", []byte(`{"v":1}`), []byte(`{}`), now)

	mock.ExpectQuery("SELECT id, source, kind, payload, metadata, ts").
		WillReturnRows(rows)

	results, err := store.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if results[0].ID != "e1" {
		t.Errorf("ID = %q", results[0].ID)
	}
	if results[0].Kind != KindFitness {
		t.Errorf("Kind = %q", results[0].Kind)
	}
}

func TestPostgresStoreQuery_WithSourceFilter(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{"id", "source", "kind", "payload", "metadata", "ts"}).
		AddRow("e1", "my-src", "fitness", []byte(`{}`), []byte(`{}`), time.Now())

	mock.ExpectQuery("SELECT id, source, kind").
		WithArgs("my-src").
		WillReturnRows(rows)

	results, err := store.Query(ctx, Filter{Source: "my-src"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results len = %d", len(results))
	}
}

func TestPostgresStoreQuery_WithAllFilters(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	since := time.Now().Add(-time.Hour)
	until := time.Now()
	payloadFilter := map[string]any{"strategy_id": "s1"}

	rows := sqlmock.NewRows([]string{"id", "source", "kind", "payload", "metadata", "ts"}).
		AddRow("e1", "src", "fitness", []byte(`{"strategy_id":"s1"}`), []byte(`{}`), time.Now())

	mock.ExpectQuery("SELECT id, source, kind").
		WithArgs("src", "fitness", since, until, sqlmock.AnyArg(), 10).
		WillReturnRows(rows)

	results, err := store.Query(ctx, Filter{
		Source:        "src",
		Kind:          KindFitness,
		Since:         since,
		Until:         until,
		Limit:         10,
		PayloadFilter: payloadFilter,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results len = %d", len(results))
	}
}

func TestPostgresStoreQuery_WithMetadata(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	metaJSON, _ := json.Marshal(map[string]string{"task_id": "t1", "agent_id": "a1"})
	rows := sqlmock.NewRows([]string{"id", "source", "kind", "payload", "metadata", "ts"}).
		AddRow("e1", "src", "fitness", []byte(`{}`), metaJSON, time.Now())

	mock.ExpectQuery("SELECT id, source, kind").WillReturnRows(rows)

	results, err := store.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d", len(results))
	}
	if results[0].Metadata["task_id"] != "t1" {
		t.Errorf("Metadata[task_id] = %q", results[0].Metadata["task_id"])
	}
	if results[0].Metadata["agent_id"] != "a1" {
		t.Errorf("Metadata[agent_id] = %q", results[0].Metadata["agent_id"])
	}
}

func TestPostgresStoreQuery_EmptyMetadata(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{"id", "source", "kind", "payload", "metadata", "ts"}).
		AddRow("e1", "src", "fitness", []byte(`{}`), []byte(`{}`), time.Now())

	mock.ExpectQuery("SELECT id, source, kind").WillReturnRows(rows)

	results, err := store.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d", len(results))
	}
	if len(results[0].Metadata) != 0 {
		t.Errorf("Metadata should be empty, got %v", results[0].Metadata)
	}
}

func TestPostgresStoreQuery_DBError(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	mock.ExpectQuery("SELECT id, source, kind").WillReturnError(context.DeadlineExceeded)

	_, err := store.Query(ctx, Filter{})
	if err == nil {
		t.Error("expected error from DB failure")
	}
}

func TestPostgresStoreQuery_ScanError(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{"id", "source", "kind", "payload", "metadata", "ts"}).
		AddRow(nil, "src", "fitness", []byte(`{}`), []byte(`{}`), time.Now())

	mock.ExpectQuery("SELECT id, source, kind").WillReturnRows(rows)

	_, err := store.Query(ctx, Filter{})
	if err == nil {
		t.Error("expected scan error for nil id")
	}
}

// ── PostgresStore.Aggregate ─────────────────────────────────────────────────

func TestPostgresStoreAggregate_Success(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{"id", "source", "kind", "payload", "metadata", "ts"}).
		AddRow("e1", "src", "fitness", []byte(`1.0`), []byte(`{}`), time.Now()).
		AddRow("e2", "src", "fitness", []byte(`2.0`), []byte(`{}`), time.Now()).
		AddRow("e3", "src", "fitness", []byte(`3.0`), []byte(`{}`), time.Now())

	mock.ExpectQuery("SELECT id, source, kind").WillReturnRows(rows)

	sum := func(values []float64) float64 {
		total := 0.0
		for _, v := range values {
			total += v
		}
		return total
	}

	result, err := store.Aggregate(ctx, Filter{Source: "src"}, sum)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if result != 6.0 {
		t.Errorf("Aggregate = %v, want 6.0", result)
	}
}

func TestPostgresStoreAggregate_Empty(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{"id", "source", "kind", "payload", "metadata", "ts"})
	mock.ExpectQuery("SELECT id, source, kind").WillReturnRows(rows)

	fn := func(values []float64) float64 { return 42.0 }
	result, err := store.Aggregate(ctx, Filter{}, fn)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if result != 0 {
		t.Errorf("Aggregate on empty = %v, want 0", result)
	}
}

func TestPostgresStoreAggregate_NonNumericPayloads(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{"id", "source", "kind", "payload", "metadata", "ts"}).
		AddRow("e1", "src", "fitness", []byte(`{"text":"not a number"}`), []byte(`{}`), time.Now())

	mock.ExpectQuery("SELECT id, source, kind").WillReturnRows(rows)

	fn := func(values []float64) float64 { return 99.0 }
	result, err := store.Aggregate(ctx, Filter{Source: "src"}, fn)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if result != 0 {
		t.Errorf("Aggregate = %v, want 0 for non-numeric payloads", result)
	}
}

func TestPostgresStoreAggregate_QueryError(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	mock.ExpectQuery("SELECT id, source, kind").WillReturnError(context.DeadlineExceeded)

	fn := func(values []float64) float64 { return 0 }
	_, err := store.Aggregate(ctx, Filter{}, fn)
	if err == nil {
		t.Error("expected error from query failure")
	}
}

// ── PostgresStore.CleanupExpired ────────────────────────────────────────────

func TestPostgresStoreCleanupExpired_Success(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	mock.ExpectExec("DELETE FROM evidence_records").
		WillReturnResult(sqlmock.NewResult(0, 5))

	n, err := store.CleanupExpired(ctx)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if n != 5 {
		t.Errorf("CleanupExpired = %d, want 5", n)
	}
}

func TestPostgresStoreCleanupExpired_Zero(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	mock.ExpectExec("DELETE FROM evidence_records").
		WillReturnResult(sqlmock.NewResult(0, 0))

	n, err := store.CleanupExpired(ctx)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if n != 0 {
		t.Errorf("CleanupExpired = %d, want 0", n)
	}
}

func TestPostgresStoreCleanupExpired_DBError(t *testing.T) {
	store, mock := newMockPostgresStore(t)
	ctx := context.Background()

	mock.ExpectExec("DELETE FROM evidence_records").
		WillReturnError(context.DeadlineExceeded)

	_, err := store.CleanupExpired(ctx)
	if err == nil {
		t.Error("expected error from DB failure")
	}
}

// ── PostgresStore.Close ─────────────────────────────────────────────────────

func TestPostgresStoreClose_RealDB(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	store := &PostgresStore{db: db}
	mock.ExpectClose()
	if err := store.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// ── ttlSeconds comprehensive ────────────────────────────────────────────────

func TestTTLSecondsComprehensive(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want int64
	}{
		{"zero_means_no_expiry", 0, 0},
		{"negative_means_no_expiry", -1 * time.Hour, 0},
		{"exactly_one_second", time.Second, 1},
		{"rounds_up_subsecond", 100 * time.Millisecond, 1},
		{"rounds_up_half_second", 500 * time.Millisecond, 1},
		{"two_seconds", 2 * time.Second, 2},
		{"one_minute", time.Minute, 60},
		{"one_hour", time.Hour, 3600},
		{"one_day", 24 * time.Hour, 86400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ttlSeconds(tt.ttl)
			if got != tt.want {
				t.Errorf("ttlSeconds(%v) = %d, want %d", tt.ttl, got, tt.want)
			}
		})
	}
}

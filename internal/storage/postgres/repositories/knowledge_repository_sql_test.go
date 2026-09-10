// Package repositories — unit tests for the tenant-scoped content_hash
// conflict target (REVIEW 2.6#31) that do not need a live database: a
// recording sql driver captures the SQL the repository sends, and the
// assertions lock the exact conflict target the unique constraint provides.
package repositories

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// recordingDriver records every query statement executed through it and
// answers result sets with zero rows (Create only scans the RETURNING id,
// so it observes sql.ErrNoRows — the SQL has already been recorded by then).
type recordingDriver struct {
	mu      sync.Mutex
	queries []string
}

func (d *recordingDriver) Open(string) (driver.Conn, error) { return recordingConn{d: d}, nil }

func (d *recordingDriver) record(query string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queries = append(d.queries, query)
}

func (d *recordingDriver) all() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.queries...)
}

type recordingConn struct {
	d *recordingDriver
}

func (recordingConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not implemented") }
func (recordingConn) Close() error                        { return nil }
func (recordingConn) Begin() (driver.Tx, error)           { return nil, errors.New("not implemented") }

func (c recordingConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.record(query)
	return emptyRows{}, nil
}

func (c recordingConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.d.record(query)
	return driver.RowsAffected(0), nil
}

type emptyRows struct{}

func (emptyRows) Columns() []string         { return []string{"id"} }
func (emptyRows) Close() error              { return nil }
func (emptyRows) Next([]driver.Value) error { return io.EOF }

// newRecordingDB registers a fresh recording driver and opens a pool over it.
func newRecordingDB(t *testing.T) (*sql.DB, *recordingDriver) {
	t.Helper()
	drv := &recordingDriver{}
	sql.Register(driverNameForTest(t), drv)
	db, err := sql.Open(driverNameForTest(t), "")
	if err != nil {
		t.Fatalf("open recording db: %v", err)
	}
	return db, drv
}

func driverNameForTest(t *testing.T) string {
	return "recording-" + t.Name() + "-" + strings.ReplaceAll(strings.ReplaceAll(t.Name(), "/", "-"), "_", "-")
}

// TestKnowledgeRepository_Create_PerTenantConflictTarget verifies the dedup
// upsert conflicts on (tenant_id, content_hash), not the global content_hash:
// with the old global target, tenant B ingesting content tenant A already
// stored hit A's row, bumped A's access_count and returned A's id —
// cross-tenant corruption plus silent data loss for B.
func TestKnowledgeRepository_Create_PerTenantConflictTarget(t *testing.T) {
	db, drv := newRecordingDB(t)
	defer func() { _ = db.Close() }()
	repo := NewKnowledgeRepository(db, nil)

	// ErrNoRows from the RETURNING scan is expected with the empty driver;
	// the assertion target is the SQL text itself.
	_ = repo.Create(context.Background(), &storage_models.KnowledgeChunk{
		TenantID:    "tenant-b",
		Content:     "shared content",
		ContentHash: "hash-shared",
	})

	queries := drv.all()
	if len(queries) != 1 {
		t.Fatalf("expected exactly 1 insert query, got %d: %q", len(queries), queries)
	}
	q := queries[0]
	if !strings.Contains(q, "ON CONFLICT (tenant_id, content_hash)") {
		t.Errorf("conflict target must be (tenant_id, content_hash), query: %s", q)
	}
	if strings.Contains(q, "ON CONFLICT (content_hash)") {
		t.Errorf("global content_hash conflict target must be gone, query: %s", q)
	}
}

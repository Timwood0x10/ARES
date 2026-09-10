package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// tenantRecordingDriver records every statement executed through it,
// including the set_config calls that bind and clear the tenant GUC.
type tenantRecordingDriver struct {
	mu      sync.Mutex
	queries []string
	closed  int
}

func (d *tenantRecordingDriver) Open(string) (driver.Conn, error) {
	return tenantRecordingConn{d: d}, nil
}

func (d *tenantRecordingDriver) record(query string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queries = append(d.queries, query)
}

func (d *tenantRecordingDriver) all() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.queries...)
}

type tenantRecordingConn struct{ d *tenantRecordingDriver }

func (tenantRecordingConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c tenantRecordingConn) Close() error {
	c.d.mu.Lock()
	c.d.closed++
	c.d.mu.Unlock()
	return nil
}
func (tenantRecordingConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("not implemented") }

func (c tenantRecordingConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.record(query)
	return tenantEmptyRows{}, nil
}

func (c tenantRecordingConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.d.record(query)
	return driver.RowsAffected(0), nil
}

type tenantEmptyRows struct{}

func (tenantEmptyRows) Columns() []string         { return []string{"id"} }
func (tenantEmptyRows) Close() error              { return nil }
func (tenantEmptyRows) Next([]driver.Value) error { return io.EOF }

// waitFor polls cond until it returns true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// runtimeGC forces garbage collection and gives finalizer goroutines a
// moment to run. A second GC cycle catches objects queued during the first.
func runtimeGC() {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
}

// dropRef clears the caller's *ManagedRows reference so the runtime can
// collect it (and run its finalizer).
func dropRef(mr **ManagedRows) { *mr = nil }

// TestQueryWithTenantFinalizerClearsTenantContext locks REVIEW 3.5b:
// QueryWithTenant sets a connection-level GUC, so a caller that forgets
// Close() leaks BOTH the connection and the tenant binding into the pool.
// Query and QueryRow already had finalizers; QueryWithTenant had none, and
// its finalizer must additionally clear the tenant context before releasing
// the connection — a plain release would leak RLS state to other tenants.
func TestQueryWithTenantFinalizerClearsTenantContext(t *testing.T) {
	drv := &tenantRecordingDriver{}
	name := "tenant-recording-" + strings.ReplaceAll(t.Name(), "/", "-")
	sql.Register(name, drv)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	pool := &Pool{db: db}

	mr, err := pool.QueryWithTenant(context.Background(), "tenant-a", "SELECT 1")
	if err != nil {
		t.Fatalf("QueryWithTenant: %v", err)
	}
	// Drop the reference WITHOUT calling Close, then force collection so the
	// finalizer runs. The nil assignment goes through a pointer parameter so
	// it reads as a real store (ineffassign would flag a direct `mr = nil`
	// on a variable that is never read again) while still dropping the only
	// remaining reference to the ManagedRows.
	dropRef(&mr)
	runtimeGC()

	// Release() returns the *sql.Conn to database/sql's pool (the driver
	// connection stays open), so the observable proof is the tenant-clear
	// set_config the finalizer must execute before releasing.
	sawClear := func() bool {
		for _, q := range drv.all() {
			if strings.Contains(q, "set_config('app.tenant_id', '', false)") {
				return true
			}
		}
		return false
	}
	waitFor(t, "finalizer to clear tenant context", sawClear)

	if !sawClear() {
		t.Fatalf("finalizer released the connection without clearing the tenant GUC; queries: %v", drv.all())
	}

	// The bind must have happened before the clear (order sanity).
	queries := drv.all()
	bindIdx, clearIdx := -1, -1
	for i, q := range queries {
		if strings.Contains(q, "'app.tenant_id'") && strings.Contains(q, "$1") {
			bindIdx = i
		}
		if strings.Contains(q, "set_config('app.tenant_id', '', false)") {
			clearIdx = i
		}
	}
	if bindIdx == -1 || clearIdx == -1 || clearIdx < bindIdx {
		t.Fatalf("expected bind before clear, queries: %v", queries)
	}
}

// TestSaveProfileIsSingleUpsert locks REVIEW 3.5b: SaveProfile used to do
// exists-then-Create, a TOCTOU that produced duplicate-key errors when two
// writers raced. The fix routes it through ProfileRepository.Upsert — a
// single INSERT ... ON CONFLICT (user_id) DO UPDATE — so the decision is
// atomic in the database and exactly one statement is issued.
func TestSaveProfileIsSingleUpsert(t *testing.T) {
	drv := &tenantRecordingDriver{}
	name := "profile-recording-" + strings.ReplaceAll(t.Name(), "/", "-")
	sql.Register(name, drv)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := &Repository{Profile: NewProfileRepositoryWithDB(db)}
	profile := &models.UserProfile{UserID: "race-user", Name: "racer"}
	if err := repo.SaveProfile(context.Background(), profile); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	queries := drv.all()
	if len(queries) != 1 {
		t.Fatalf("SaveProfile must issue exactly one statement, got %d: %q", len(queries), queries)
	}
	q := queries[0]
	if !strings.Contains(q, "ON CONFLICT (user_id) DO UPDATE") {
		t.Errorf("SaveProfile must be an atomic upsert, got: %s", q)
	}
	if strings.Contains(q, "SELECT EXISTS") {
		t.Errorf("SaveProfile must not pre-check existence (the TOCTOU source), got: %s", q)
	}
}

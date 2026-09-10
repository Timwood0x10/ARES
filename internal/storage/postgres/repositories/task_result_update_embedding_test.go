package repositories

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// argsRecordingDriver records each ExecContext statement together with its
// arguments so tests can assert on bound parameter values.
type argsRecordingDriver struct {
	mu    sync.Mutex
	calls []argsCall
}

type argsCall struct {
	query string
	args  []driver.NamedValue
}

func (d *argsRecordingDriver) Open(string) (driver.Conn, error) { return argsRecordingConn{d: d}, nil }

func (d *argsRecordingDriver) record(query string, args []driver.NamedValue) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Copy the args: the NamedValue slice is reused by database/sql.
	cp := make([]driver.NamedValue, len(args))
	copy(cp, args)
	d.calls = append(d.calls, argsCall{query: query, args: cp})
}

func (d *argsRecordingDriver) execs() []argsCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]argsCall(nil), d.calls...)
}

type argsRecordingConn struct{ d *argsRecordingDriver }

func (argsRecordingConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("not implemented")
}
func (argsRecordingConn) Close() error              { return nil }
func (argsRecordingConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("not implemented") }

func (c argsRecordingConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.d.record(query, args)
	return argsRowsAffected(1), nil
}

func (c argsRecordingConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.d.record(query, args)
	return argsEmptyRows{}, nil
}

type argsRowsAffected int64

func (a argsRowsAffected) LastInsertId() (int64, error) { return 0, nil }
func (a argsRowsAffected) RowsAffected() (int64, error) { return int64(a), nil }

type argsEmptyRows struct{}

func (argsEmptyRows) Columns() []string         { return []string{"id"} }
func (argsEmptyRows) Close() error              { return nil }
func (argsEmptyRows) Next([]driver.Value) error { return io.EOF }

// TestTaskResultUpdateEmptyEmbeddingBindsNull locks REVIEW 3.7: Update used
// FormatVector unconditionally, so an empty embedding became the literal
// string "[]" and the $6::vector cast rejected it. Update must bind NULL
// for an empty embedding, exactly like Create.
func TestTaskResultUpdateEmptyEmbeddingBindsNull(t *testing.T) {
	drv := &argsRecordingDriver{}
	name := "args-recording-" + strings.ReplaceAll(strings.ReplaceAll(t.Name(), "/", "-"), "_", "-")
	sql.Register(name, drv)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewTaskResultRepository(db)
	result := &storage_models.TaskResult{
		ID: "tr-1", TenantID: "tenant-1", SessionID: "s", TaskType: "t", AgentID: "a",
		Status: "done",
		// Embedding intentionally empty.
	}
	if err := repo.Update(context.Background(), result); err != nil {
		t.Fatalf("Update: %v", err)
	}

	execs := drv.execs()
	if len(execs) != 1 {
		t.Fatalf("expected exactly 1 exec, got %d", len(execs))
	}
	// The embedding is the 6th parameter ($6::vector).
	if len(execs[0].args) < 6 {
		t.Fatalf("expected at least 6 args, got %d", len(execs[0].args))
	}
	emb := execs[0].args[5].Value
	if emb != nil {
		t.Fatalf("empty embedding must bind NULL, got %#v (%T)", emb, emb)
	}

	// Non-empty embedding must still format as a vector literal.
	result.Embedding = []float64{0.5, 0.25}
	if err := repo.Update(context.Background(), result); err != nil {
		t.Fatalf("Update with embedding: %v", err)
	}
	execs = drv.execs()
	emb = execs[len(execs)-1].args[5].Value
	s, ok := emb.(string)
	if !ok {
		t.Fatalf("non-empty embedding must bind a string literal, got %#v (%T)", emb, emb)
	}
	if !strings.HasPrefix(s, "[") || !strings.Contains(s, "0.5") {
		t.Fatalf("non-empty embedding must be a pgvector literal, got %q", s)
	}
}

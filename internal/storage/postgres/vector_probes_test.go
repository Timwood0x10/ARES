package postgres

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestWithVectorSearchTuning_OpensTheTransactionTheSettingNeeds is the H1 contract:
// SET LOCAL only applies inside a transaction block, so the helper must open one and
// run both the setting and the query inside it. Without the transaction the setting
// is silently ignored — the deployment reads "probes configured" and gets untuned
// recall.
func TestWithVectorSearchTuning_OpensTheTransactionTheSettingNeeds(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL ivfflat.probes = 10")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1")).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
	mock.ExpectCommit()

	ran := false
	err = WithVectorSearchTuning(context.Background(), db, VectorSearchTuning{Probes: 10}, func(ctx context.Context, handle DBTX) error {
		ran = true
		var x int
		return handle.QueryRowContext(ctx, "SELECT 1").Scan(&x)
	})
	if err != nil {
		t.Fatalf("WithVectorSearchTuning: %v", err)
	}
	if !ran {
		t.Fatal("the work must run inside the probe-scoped transaction")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("the transaction must wrap the setting AND the work: %v", err)
	}
}

// TestWithVectorSearchTuning_RollsBackOnFailure keeps a failed search from leaving the
// connection inside an open transaction.
func TestWithVectorSearchTuning_RollsBackOnFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL ivfflat.probes = 5")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	wantErr := sql.ErrConnDone
	err = WithVectorSearchTuning(context.Background(), db, VectorSearchTuning{Probes: 5}, func(context.Context, DBTX) error {
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("err = %v, want the work's own error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("a failed search must roll back: %v", err)
	}
}

// TestWithVectorSearchTuning_UnconfiguredOpensNothing: an unset probe count must not
// pay for a transaction it does not need.
func TestWithVectorSearchTuning_UnconfiguredOpensNothing(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1")).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))

	err = WithVectorSearchTuning(context.Background(), db, VectorSearchTuning{Probes: 0}, func(ctx context.Context, handle DBTX) error {
		var x int
		return handle.QueryRowContext(ctx, "SELECT 1").Scan(&x)
	})
	if err != nil {
		t.Fatalf("WithVectorSearchTuning: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("probes=0 must run the work directly, in no transaction: %v", err)
	}
}

// TestWithVectorSearchTuning_TxHandleUsesTheExistingTransaction: a handle that is
// already a transaction sets the probe count in it, without opening a nested one
// (Postgres has no nested BEGIN, so this is a correctness requirement, not a
// preference).
func TestWithVectorSearchTuning_TxHandleUsesTheExistingTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL ivfflat.probes = 7")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err = WithVectorSearchTuning(context.Background(), tx, VectorSearchTuning{Probes: 7}, func(ctx context.Context, handle DBTX) error {
		if handle != tx {
			t.Error("the work must run on the CALLER's transaction, not a new one")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithVectorSearchTuning: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("a transaction handle must not open a second transaction: %v", err)
	}
}

// probeOpaqueHandle implements DBTX without being any shape the helper can open a
// transaction on.
type probeOpaqueHandle struct{ ran bool }

func (h *probeOpaqueHandle) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, nil
}

func (h *probeOpaqueHandle) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, sql.ErrConnDone
}

func (h *probeOpaqueHandle) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, sql.ErrConnDone
}

func (h *probeOpaqueHandle) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	return &sql.Row{}
}

// TestWithVectorSearchTuning_UnsupportedHandleStillRunsTheWork: a handle that cannot
// carry a transaction-local setting must not fail the search — but it must not pretend
// the knob was applied either (the helper logs a Warning; this pins the "still do the
// work" half).
func TestWithVectorSearchTuning_UnsupportedHandleStillRunsTheWork(t *testing.T) {
	handle := &probeOpaqueHandle{}

	err := WithVectorSearchTuning(context.Background(), handle, VectorSearchTuning{Probes: 9}, func(context.Context, DBTX) error {
		handle.ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("an unsupported handle must not fail the search: %v", err)
	}
	if !handle.ran {
		t.Error("the work must still run when probes cannot be applied")
	}
}

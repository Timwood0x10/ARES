// nolint: errcheck // Operations may ignore return values
package postgres

import (
	"context"
	"database/sql"
	"log/slog"
	"runtime"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	coreerrors "github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/errors"
)

// Pool represents a database connection pool with "get usage release" pattern.
type Pool struct {
	cfg          *Config
	db           *sql.DB
	mu           sync.RWMutex
	waitCount    int
	waitDuration time.Duration
}

// NewPool creates a new database connection pool.
func NewPool(cfg *Config) (*Pool, error) {
	if err := cfg.Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid config")
	}

	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		return nil, errors.Wrap(err, "failed to open database")
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	if err := db.Ping(); err != nil {
		return nil, errors.Wrap(err, "failed to ping database")
	}

	return &Pool{
		cfg: cfg,
		db:  db,
	}, nil
}

// Get acquires a connection from the pool.
func (p *Pool) Get(ctx context.Context) (*sql.Conn, error) {
	start := time.Now()

	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get connection")
	}

	p.mu.Lock()
	elapsed := time.Since(start)
	p.waitDuration += elapsed
	if elapsed > time.Second {
		p.waitCount++
	}
	p.mu.Unlock()

	return conn, nil
}

// Release returns a connection to the pool.
func (p *Pool) Release(conn *sql.Conn) {
	if conn == nil {
		return
	}

	_ = conn.Close()
}

// WithConnection executes a function with a connection from the pool.
// This is the recommended pattern: get usage release.
func (p *Pool) WithConnection(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := p.Get(ctx)
	if err != nil {
		return err
	}
	defer p.Release(conn)

	return fn(conn)
}

// Close closes all connections in the pool.
func (p *Pool) Close() error {
	return p.db.Close()
}

// Stats returns connection pool statistics.
func (p *Pool) Stats() *PoolStats {
	stats := p.db.Stats()

	p.mu.RLock()
	defer p.mu.RUnlock()

	return &PoolStats{
		OpenConnections:  stats.OpenConnections,
		InUseConnections: stats.InUse,
		IdleConnections:  stats.Idle,
		WaitCount:        stats.WaitCount + int64(p.waitCount),
		WaitDuration:     stats.WaitDuration + p.waitDuration,
		MaxOpenConns:     p.cfg.MaxOpenConns,
	}
}

// PoolStats holds pool statistics.
type PoolStats struct {
	OpenConnections  int
	InUseConnections int
	IdleConnections  int
	WaitCount        int64
	WaitDuration     time.Duration
	MaxOpenConns     int
}

// IsHealthy checks if the pool is healthy.
func (p *Pool) IsHealthy() bool {
	stats := p.Stats()
	return stats.OpenConnections > 0 && stats.OpenConnections < stats.MaxOpenConns
}

// Ping pings the database to check connectivity.
func (p *Pool) Ping(ctx context.Context) error {
	return p.db.PingContext(ctx)
}

// GetDB returns the underlying *sql.DB for repository initialization.
// This is needed for repository constructors that require *sql.DB.
func (p *Pool) GetDB() *sql.DB {
	return p.db
}

// Exec executes a query without returning rows.
func (p *Pool) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.cfg.QueryTimeout)
		defer cancel()
	}

	var result sql.Result
	var execErr error

	if err := p.WithConnection(ctx, func(conn *sql.Conn) error {
		result, execErr = conn.ExecContext(ctx, query, args...)
		return execErr
	}); err != nil {
		return nil, err
	}

	return result, execErr
}

// Query executes a query and returns rows.
// The connection is released when rows are closed.
func (p *Pool) Query(ctx context.Context, query string, args ...any) (*ManagedRows, error) {
	// Add query timeout if not already set in context
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.cfg.QueryTimeout)
		defer cancel()
	}

	conn, err := p.Get(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		p.Release(conn)
		return nil, err
	}

	mr := &ManagedRows{
		Rows: rows,
		conn: conn,
		pool: p,
	}
	// Set finalizer to release connection if caller forgets to call Close()
	runtime.SetFinalizer(mr, func(m *ManagedRows) {
		if m.conn != nil {
			slog.Warn("ManagedRows garbage collected without Close() being called, releasing connection")
			m.pool.Release(m.conn)
			m.conn = nil
		}
	})

	return mr, nil
}

// ManagedRows wraps sql.Rows and manages connection lifecycle.
type ManagedRows struct {
	*sql.Rows
	conn *sql.Conn
	pool *Pool
}

// Close closes the rows and releases the connection.
func (m *ManagedRows) Close() error {
	if m.conn != nil {
		m.pool.Release(m.conn)
		m.conn = nil
		runtime.SetFinalizer(m, nil)
	}
	return m.Rows.Close()
}

// QueryRow executes a query and returns a single row.
func (p *Pool) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	// Add query timeout if not already set in context
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.cfg.QueryTimeout)
		defer cancel()
	}

	var (
		row     *sql.Row
		connErr error
	)

	connErr = p.WithConnection(ctx, func(conn *sql.Conn) error {
		row = conn.QueryRowContext(ctx, query, args...)
		return nil
	})

	// If connection acquisition failed, return a row that produces a clear error on Scan.
	// We use a cancelled context with a descriptive query to help identify the error source.
	// The caller should check the error from Scan to detect connection failures.
	if connErr != nil || row == nil {
		if connErr != nil {
			slog.Error("Failed to acquire database connection for QueryRow", "error", connErr)
		}
		cancelCtx, cancel := context.WithCancel(context.Background())
		cancel()
		// Return a row that will fail on Scan with "context canceled" error.
		// The log above provides the actual error reason for debugging.
		return p.db.QueryRowContext(cancelCtx, "SELECT 1 WHERE 1=0")
	}

	return row
}

// Begin starts a new transaction.
func (p *Pool) Begin(ctx context.Context) (*sql.Tx, error) {
	return p.db.BeginTx(ctx, nil)
}

// NOTE: This module uses the standard library's database/sql package
// which already implements a connection pool. The Pool wrapper provides
// additional statistics and the "get usage release" pattern.
var _ = coreerrors.ErrDBConnectionFailed

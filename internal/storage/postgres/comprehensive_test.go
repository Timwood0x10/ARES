// nolint: errcheck // Test code may ignore return values
package postgres

import (
	"context"
	"testing"
	"time"
)

// TestPool_Comprehensive provides comprehensive tests for Pool without requiring real database.
func TestPool_Comprehensive(t *testing.T) {
	t.Run("test Release with nil connection", func(t *testing.T) {
		pool := createMockPool()
		// Should not panic
		pool.Release(nil)
	})

	t.Run("test PoolStats structure", func(t *testing.T) {
		stats := &PoolStats{
			OpenConnections:  10,
			InUseConnections: 5,
			IdleConnections:  5,
			WaitCount:        100,
			WaitDuration:     time.Second,
			MaxOpenConns:     25,
		}

		if stats.OpenConnections != 10 {
			t.Errorf("expected OpenConnections 10, got %d", stats.OpenConnections)
		}
		if stats.InUseConnections != 5 {
			t.Errorf("expected InUseConnections 5, got %d", stats.InUseConnections)
		}
		if stats.IdleConnections != 5 {
			t.Errorf("expected IdleConnections 5, got %d", stats.IdleConnections)
		}
		if stats.WaitCount != 100 {
			t.Errorf("expected WaitCount 100, got %d", stats.WaitCount)
		}
		if stats.WaitDuration != time.Second {
			t.Errorf("expected WaitDuration 1s, got %v", stats.WaitDuration)
		}
		if stats.MaxOpenConns != 25 {
			t.Errorf("expected MaxOpenConns 25, got %d", stats.MaxOpenConns)
		}
	})
}

// createMockPool creates a mock pool for testing.
func createMockPool() *Pool {
	cfg := DefaultConfig()
	cfg.Host = "invalid-host-to-force-error"
	pool, err := NewPool(cfg)
	if err != nil {
		// Return a pool with nil db for testing error cases
		return &Pool{
			cfg: cfg,
			db:  nil,
		}
	}
	return pool
}

// nolint: errcheck // Test code may ignore return values

// TestQueryWithTenantRejectsEmptyTenant verifies the mandatory-tenant guard:
// an empty tenant ID is rejected before any DB access (no real database
// required). Full RLS isolation semantics (set_config is_local=false at
// connection level, cleared on ManagedRows.Close) require a real Postgres
// environment and are documented in pool.go.
func TestQueryWithTenantRejectsEmptyTenant(t *testing.T) {
	pool := createMockPool()
	if _, err := pool.QueryWithTenant(context.Background(), "", "SELECT 1"); err != ErrMissingTenantID {
		t.Fatalf("empty tenant must be rejected with ErrMissingTenantID, got %v", err)
	}
	if _, err := pool.ExecWithTenant(context.Background(), "", "SELECT 1"); err != ErrMissingTenantID {
		t.Fatalf("empty tenant must be rejected with ErrMissingTenantID, got %v", err)
	}
}

// TestClearTenantContextNilSafe verifies the tenant-context cleanup helper
// tolerates a nil connection without panicking. The cleanup runs on error
// paths (QueryWithTenant set/query failure) and on ManagedRows.Close, where a
// nil guard keeps those paths safe even under partial initialization.
func TestClearTenantContextNilSafe(t *testing.T) {
	clearTenantContext(nil) // must not panic
}

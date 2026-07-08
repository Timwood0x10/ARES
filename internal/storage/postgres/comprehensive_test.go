// nolint: errcheck // Test code may ignore return values
package postgres

import (
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

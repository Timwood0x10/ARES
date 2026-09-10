// package integration provides end-to-end integration tests with real PostgreSQL.
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// getTestPool creates a PostgreSQL connection pool for integration tests.
// Returns nil and skips the test when TEST_POSTGRES_DSN is not set.
func getTestPool(t *testing.T) *postgres.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set, skipping integration test")
		return nil
	}

	cfg := &postgres.Config{
		Host:            "localhost",
		Port:            5432,
		User:            "test",
		Password:        "test",
		Database:        "testdb",
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 1 * time.Minute,
	}

	pool, err := postgres.NewPool(cfg)
	if err != nil {
		t.Skipf("could not create test pool: %v", err)
		return nil
	}

	require.NoError(t, pool.Ping(context.Background()), "database ping failed")
	return pool
}

// runMigrations runs both Migrate and MigrateStorage to create all required tables.
func runMigrations(t *testing.T, pool *postgres.Pool) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, postgres.Migrate(ctx, pool), "Migrate failed")
	require.NoError(t, postgres.MigrateStorage(ctx, pool), "MigrateStorage failed")
}

// cleanupTables deletes all test data from the tables used by integration tests.
// This is called after each test to ensure isolation.
func cleanupTables(t *testing.T, pool *postgres.Pool, tables ...string) {
	t.Helper()

	ctx := context.Background()
	for _, table := range tables {
		_, err := pool.Exec(ctx, "DELETE FROM "+table)
		if err != nil {
			t.Logf("cleanup warning for table %s: %v", table, err)
		}
	}
}

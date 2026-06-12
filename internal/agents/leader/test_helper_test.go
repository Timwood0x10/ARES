package leader

import (
	"os"
	"testing"
	"time"

	"goagentx/internal/storage/postgres"
)

// getTestPool creates a PostgreSQL connection pool for integration tests.
// Returns nil when TEST_POSTGRES_DSN is not set (caller should skip).
func getTestPool(t *testing.T) *postgres.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
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
		t.Logf("could not create test pool: %v", err)
		return nil
	}
	return pool
}

package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/spf13/cobra"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// testEnvOr reads a test-only override variable. TEST_POSTGRES_DSN and the
// DB_* test knobs are CI integration-test gates, not runtime configuration —
// runtime reads the ares.yaml config file only.
func testEnvOr(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

var dbSetupTestCmd = &cobra.Command{
	Use:   "setup-test",
	Short: "Setup test database",
	Long: `Creates and migrates the test database (integration-test helper).
Respects TEST_POSTGRES_DSN first, then falls back to the test DB_* variables.
Default: postgres://postgres:postgres@localhost:5432/ARES_test?sslmode=disable`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDbSetupTest()
	},
}

func init() {
	dbCmd.AddCommand(dbSetupTestCmd)
}

func runDbSetupTest() error {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		host := testEnvOr("DB_HOST", "localhost")
		port := testEnvOr("DB_PORT", "5433")
		user := testEnvOr("DB_USER", "postgres")
		password := testEnvOr("DB_PASSWORD", "postgres")
		dbname := testEnvOr("DB_NAME", "ARES_test")
		dsn = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			url.QueryEscape(user), url.QueryEscape(password),
			host, port, dbname)
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("invalid DSN: %w", err)
	}
	dbname := strings.TrimPrefix(parsed.Path, "/")

	adminDB := connectAdmin(changeDB(dsn, "postgres"))
	defer func() { _ = adminDB.Close() }()

	ensureDatabase(adminDB, dbname)
	_ = adminDB.Close()

	cfg := &postgres.Config{
		Host:            parsed.Hostname(),
		Port:            parsePort(parsed.Port(), 5432),
		User:            parsed.User.Username(),
		Password:        passwordFromURL(parsed),
		Database:        dbname,
		SSLMode:         testEnvOr("DB_SSL_MODE", "disable"),
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 0,
		ConnMaxIdleTime: 0,
		QueryTimeout:    10 * time.Second,
	}

	pool, err := postgres.NewPool(cfg)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer func() { _ = pool.Close() }()

	ctx := context.Background()

	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return fmt.Errorf("enable pgvector: %w", err)
	}
	fmt.Println("pgvector extension enabled")

	if err := postgres.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("core migration: %w", err)
	}

	if err := postgres.MigrateStorage(ctx, pool); err != nil {
		return fmt.Errorf("migration: %w", err)
	}
	fmt.Println("Test database migrations completed successfully")

	return nil
}

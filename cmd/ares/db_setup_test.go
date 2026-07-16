package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/spf13/cobra"
)

var dbSetupTestCmd = &cobra.Command{
	Use:   "setup-test",
	Short: "Setup test database",
	Long: `Creates and migrates the test database.
Respects TEST_POSTGRES_DSN first, then falls back to DB_* env vars.
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
		host := getEnv("DB_HOST", "localhost")
		port := getEnv("DB_PORT", "5433")
		user := getEnv("DB_USER", "postgres")
		password := getEnv("DB_PASSWORD", "postgres")
		dbname := getEnv("DB_NAME", "ARES_test")
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

	if err := postgres.MigrateStorage(ctx, pool); err != nil {
		return fmt.Errorf("migration: %w", err)
	}
	fmt.Println("Test database migrations completed successfully")

	return nil
}

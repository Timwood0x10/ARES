// Package main creates and migrates the test database.
// It respects TEST_POSTGRES_DSN first, then falls back to DB_* env vars.
// Default: postgres://postgres:postgres@localhost:5432/ARES_test?sslmode=disable
package main

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
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
		log.Error("invalid DSN", "dsn", dsn, "error", err)
		os.Exit(1)
	}
	dbname := strings.TrimPrefix(parsed.Path, "/")

	adminDB := connectAdmin(changeDB(dsn, "postgres"))

	ensureDatabase(adminDB, dbname)
	if err := adminDB.Close(); err != nil {
		log.Warn("adminDB.Close", "error", err)
	}

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
		log.Error("failed to connect to target database", "database", dbname, "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		_ = pool.Close()
		log.Error("failed to enable pgvector", "error", err)
		os.Exit(1)
	}
	fmt.Println("pgvector extension enabled")

	if err := postgres.MigrateStorage(ctx, pool); err != nil {
		_ = pool.Close()
		log.Error("migration failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			log.Warn("pool.Close", "error", err)
		}
	}()
	fmt.Println("Test database migrations completed successfully")
}

func connectAdmin(dsn string) *sql.DB {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	if err := db.PingContext(context.Background()); err != nil {
		log.Error("failed to ping postgres", "error", err)
		os.Exit(1)
	}
	return db
}

func ensureDatabase(db *sql.DB, name string) {
	var exists bool
	if err := db.QueryRowContext(context.Background(), "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		log.Error("failed to check database existence", "database", name, "error", err)
		os.Exit(1)
	}
	if !exists {
		if _, err := db.ExecContext(context.Background(), fmt.Sprintf("CREATE DATABASE %s", pqQuoteIdent(name))); err != nil {
			log.Error("failed to create database", "database", name, "error", err)
			os.Exit(1)
		}
		fmt.Printf("Created database: %s\n", name)
	}
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func changeDB(dsn, dbname string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + dbname
	return u.String()
}

func parsePort(port string, defaultPort int) int {
	if port == "" {
		return defaultPort
	}
	var p int
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil || p <= 0 {
		return defaultPort
	}
	return p
}

func passwordFromURL(u *url.URL) string {
	if pw, ok := u.User.Password(); ok {
		return pw
	}
	return ""
}

func pqQuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

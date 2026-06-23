// Package main creates and migrates the production ares database.
// It reads DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME env vars.
// Default: postgres://postgres:postgres@localhost:5432/goagent?sslmode=disable
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	host := getEnv("DB_HOST", "localhost")
	port := getEnv("DB_PORT", "5433")
	user := getEnv("DB_USER", "postgres")
	password := getEnv("DB_PASSWORD", "postgres")
	dbname := getEnv("DB_NAME", "goagent")

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		url.QueryEscape(user), url.QueryEscape(password),
		host, port, dbname)

	parsed, _ := url.Parse(dsn)
	dbname = strings.TrimPrefix(parsed.Path, "/")
	portStr := parsed.Port()

	adminDB := connectAdmin(changeDB(dsn, "postgres"))
	defer func() {
		if err := adminDB.Close(); err != nil {
			slog.Warn("adminDB.Close", "error", err)
		}
	}()

	ensureDatabase(adminDB, dbname)
	if err := adminDB.Close(); err != nil {
		slog.Warn("adminDB.Close", "error", err)
	}

	cfg := &postgres.Config{
		Host:            parsed.Hostname(),
		Port:            parsePort(portStr, 5432),
		User:            parsed.User.Username(),
		Password:        passwordFromURL(parsed),
		Database:        dbname,
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 0,
		ConnMaxIdleTime: 0,
		QueryTimeout:    30 * time.Second,
	}

	pool, err := postgres.NewPool(cfg)
	if err != nil {
		slog.Error("failed to connect to database", "database", dbname, "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			slog.Warn("pool.Close", "error", err)
		}
	}()

	ctx := context.Background()

	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		slog.Error("failed to enable pgvector", "error", err)
		os.Exit(1)
	}
	fmt.Println("pgvector extension enabled")

	if err := postgres.MigrateStorage(ctx, pool); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	fmt.Println("Production database migrations completed successfully")
	fmt.Println()
	fmt.Println("Tables created:")
	fmt.Println("  - knowledge_chunks_1024")
	fmt.Println("  - experiences_1024")
	fmt.Println("  - tools")
	fmt.Println("  - conversations")
	fmt.Println("  - task_results_1024")
	fmt.Println("  - secrets")
	fmt.Println("  - embedding_queue")
	fmt.Println("  - embedding_dead_letter")
	fmt.Println("  - distilled_memories")
}

func connectAdmin(dsn string) *sql.DB {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	if err := db.Ping(); err != nil {
		slog.Error("failed to ping postgres", "error", err)
		os.Exit(1)
	}
	return db
}

func ensureDatabase(db *sql.DB, name string) {
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		slog.Error("failed to check database existence", "database", name, "error", err)
		os.Exit(1)
	}
	if !exists {
		if _, err := db.Exec(fmt.Sprintf("CREATE DATABASE %s", pqQuoteIdent(name))); err != nil {
			slog.Error("failed to create database", "database", name, "error", err)
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

// Package main creates the distilled_memories table (also included in full migrate).
// This is a focused migration tool that only creates the distilled_memories table,
// for use when the full MigrateStorage is not desired.
// Env vars: DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"

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

	adminDB := connectAdmin(changeDB(dsn, "postgres"))
	defer func() {
		if err := adminDB.Close(); err != nil {
			log.Warn("adminDB.Close", "error", err)
		}
	}()

	ensureDatabase(adminDB, dbname)
	if err := adminDB.Close(); err != nil {
		log.Warn("adminDB.Close", "error", err)
	}

	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Error("failed to connect", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	createTableSQL := `
		CREATE TABLE IF NOT EXISTS distilled_memories (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			user_id TEXT,
			session_id TEXT,
			content TEXT NOT NULL,
			embedding VECTOR(1024),
			embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			memory_type VARCHAR(50) DEFAULT 'profile',
			importance FLOAT DEFAULT 0.5,
			metadata JSONB DEFAULT '{}'::jsonb,
			access_count INTEGER DEFAULT 0,
			last_accessed_at TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NOW() + INTERVAL '90 days',
			created_at TIMESTAMP DEFAULT NOW()
		)
	`
	if _, err := pool.ExecContext(ctx, createTableSQL); err != nil {
		_ = pool.Close()
		log.Error("failed to create distilled_memories table", "error", err)
		os.Exit(1)
	}
	fmt.Println("distilled_memories table created successfully")

	defer func() {
		if err := pool.Close(); err != nil {
			log.Warn("pool.Close", "error", err)
		}
	}()

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_tenant ON distilled_memories(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_user ON distilled_memories(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_session ON distilled_memories(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_type ON distilled_memories(memory_type)`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_expires ON distilled_memories(expires_at) WHERE expires_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_importance ON distilled_memories(importance DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_embedding ON distilled_memories USING ivfflat (embedding vector_cosine_ops) WHERE embedding IS NOT NULL`,
	}
	for _, idx := range indexes {
		if _, err := pool.ExecContext(ctx, idx); err != nil {
			log.Warn("failed to create index", "index", idx, "error", err)
		}
	}
	fmt.Println("Indexes created successfully")

	if _, err := pool.ExecContext(ctx, `ALTER TABLE distilled_memories ENABLE ROW LEVEL SECURITY`); err != nil {
		log.Warn("failed to enable RLS", "error", err)
	}
	rlsSQL := `CREATE POLICY IF NOT EXISTS tenant_isolation_distilled_memories ON distilled_memories USING (tenant_id = current_setting('app.tenant_id', true))`
	if _, err := pool.ExecContext(ctx, rlsSQL); err != nil {
		log.Warn("failed to create RLS policy", "error", err)
	}
	fmt.Println("Row Level Security enabled")
}

func connectAdmin(dsn string) *sql.DB {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	if err := db.Ping(); err != nil {
		log.Error("failed to ping postgres", "error", err)
		os.Exit(1)
	}
	return db
}

func ensureDatabase(db *sql.DB, name string) {
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		log.Error("failed to check database existence", "database", name, "error", err)
		os.Exit(1)
	}
	if !exists {
		if _, err := db.Exec(fmt.Sprintf("CREATE DATABASE %s", pqQuoteIdent(name))); err != nil {
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

func pqQuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

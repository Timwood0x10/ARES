package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/spf13/cobra"
)

var dbCreateTableCmd = &cobra.Command{
	Use:   "create-table",
	Short: "Create distilled_memories table",
	Long: `Creates the distilled_memories table with indexes and RLS.
Env vars: DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME.
Default: postgres://postgres:postgres@localhost:5432/goagent?sslmode=disable`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDbCreateTable()
	},
}

func init() {
	dbCmd.AddCommand(dbCreateTableCmd)
}

func runDbCreateTable() error {
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
	defer func() { _ = adminDB.Close() }()

	ensureDatabase(adminDB, dbname)
	if err := adminDB.Close(); err != nil {
		return fmt.Errorf("close admin db: %w", err)
	}

	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = pool.Close() }()

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
		return fmt.Errorf("create table: %w", err)
	}
	fmt.Println("distilled_memories table created successfully")

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
			fmt.Fprintf(os.Stderr, "warning: failed to create index: %s\n  error: %v\n", idx, err)
		}
	}
	fmt.Println("Indexes created successfully")

	if _, err := pool.ExecContext(ctx, `ALTER TABLE distilled_memories ENABLE ROW LEVEL SECURITY`); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to enable RLS: %v\n", err)
	}
	rlsSQL := `CREATE POLICY IF NOT EXISTS tenant_isolation_distilled_memories ON distilled_memories USING (tenant_id = current_setting('app.tenant_id', true))`
	if _, err := pool.ExecContext(ctx, rlsSQL); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create RLS policy: %v\n", err)
	}
	fmt.Println("Row Level Security enabled")

	return nil
}

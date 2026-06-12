package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"goagentx/internal/storage/postgres"
)

func main() {
	dbConfig := &postgres.Config{
		Host:            "localhost",
		Port:            5433,
		User:            "postgres",
		Password:        "postgres",
		Database:        "goagent",
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		QueryTimeout:    30 * time.Second,
		Embedding:       postgres.DefaultEmbeddingConfig(),
	}

	pool, err := postgres.NewPool(dbConfig)
	if err != nil {
		log.Fatalf("Failed to create database pool: %v", err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			slog.Error("Warning: Failed to close database pool:", "error", err)
		}
	}()

	ctx := context.Background()

	// Create distilled_memories table
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS distilled_memories (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			user_id TEXT,
			session_id TEXT,
			content TEXT NOT NULL,
			embedding VECTOR(1024),
			embedding_model TEXT NOT NULL DEFAULT 'e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			memory_type VARCHAR(50) DEFAULT 'profile',
			importance FLOAT DEFAULT 0.5,
			metadata JSONB DEFAULT '{}'::jsonb,
			access_count INTEGER DEFAULT 0,
			last_accessed_at TIMESTAMP,
			expires_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`

	if _, err := pool.Exec(ctx, createTableSQL); err != nil {
		log.Fatalf("Failed to create distilled_memories table: %v", err)
	}

	fmt.Println("distilled_memories table created successfully")

	// Create indexes
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_tenant ON distilled_memories(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_user ON distilled_memories(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_session ON distilled_memories(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_type ON distilled_memories(memory_type)`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_expires ON distilled_memories(expires_at) WHERE expires_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_embedding ON distilled_memories USING ivfflat (embedding vector_cosine_ops) WHERE embedding IS NOT NULL`,
	}

	for _, indexSQL := range indexes {
		if _, err := pool.Exec(ctx, indexSQL); err != nil {
			log.Printf("Warning: Failed to create index: %v", err)
		}
	}

	fmt.Println("Indexes created successfully")

	// Enable RLS
	if _, err := pool.Exec(ctx, `ALTER TABLE distilled_memories ENABLE ROW LEVEL SECURITY`); err != nil {
		log.Printf("Warning: Failed to enable RLS: %v", err)
	}

	// Create tenant isolation policy
	policySQL := `
		CREATE POLICY IF NOT EXISTS tenant_isolation_distilled_memories 
		ON distilled_memories 
		USING (tenant_id = current_setting('app.tenant_id', true))
	`
	if _, err := pool.Exec(ctx, policySQL); err != nil {
		log.Printf("Warning: Failed to create tenant isolation policy: %v", err)
	}

	fmt.Println("Row Level Security enabled successfully")
}

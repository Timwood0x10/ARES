package postgres

import (
	"context"

	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/errors"
)

// coreMigrationStatements contains the DDL for core application tables.
var coreMigrationStatements = []string{
	`CREATE TABLE IF NOT EXISTS user_profiles (
			user_id VARCHAR(255) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			gender VARCHAR(50),
			age INTEGER,
			occupation VARCHAR(255),
			style JSONB,
			budget JSONB,
			colors JSONB,
			occasions JSONB,
			body_type VARCHAR(100),
			preferences JSONB,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

	`CREATE TABLE IF NOT EXISTS sessions (
			session_id VARCHAR(255) PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL,
			input TEXT,
			status VARCHAR(50),
			user_profile JSONB,
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			expired_at TIMESTAMP
		)`,

	`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_expired_at ON sessions(expired_at)`,

	`CREATE TABLE IF NOT EXISTS recommendations (
			id SERIAL PRIMARY KEY,
			session_id VARCHAR(255) UNIQUE NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			items JSONB,
			reason TEXT,
			total_price DECIMAL(10, 2),
			match_score DECIMAL(5, 2),
			occasion VARCHAR(100),
			season VARCHAR(50),
			feedback JSONB,
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

	`CREATE INDEX IF NOT EXISTS idx_recommendations_user_id ON recommendations(user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_recommendations_created_at ON recommendations(created_at)`,

	`CREATE TABLE IF NOT EXISTS embeddings (
			id VARCHAR(255) PRIMARY KEY,
			table_name VARCHAR(100) NOT NULL,
			embedding VECTOR(1536),
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

	`CREATE INDEX IF NOT EXISTS idx_embeddings_table_name ON embeddings(table_name)`,

	`CREATE TABLE IF NOT EXISTS leader_checkpoints (
			leader_id VARCHAR(255) NOT NULL,
			session_id VARCHAR(255) NOT NULL,
			status VARCHAR(50) NOT NULL DEFAULT 'active',
			metadata JSONB DEFAULT '{}'::jsonb,
			updated_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (leader_id)
		)`,

	`CREATE INDEX IF NOT EXISTS idx_leader_checkpoints_status ON leader_checkpoints(status)`,

	// knowledge_chunks_1024 - RAG knowledge base with fixed 1024 dimensions.
	`CREATE TABLE IF NOT EXISTS knowledge_chunks_1024 (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			content TEXT NOT NULL,
			embedding VECTOR(1024),
			embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			embedding_status TEXT DEFAULT 'completed',
			embedding_queued_at TIMESTAMP,
			embedding_processed_at TIMESTAMP,
			embedding_error TEXT,
			tsv TSVECTOR,
			source_type VARCHAR(50),
			source TEXT,
			metadata JSONB DEFAULT '{}'::jsonb,
			document_id UUID,
			chunk_index INTEGER,
			content_hash TEXT UNIQUE,
			access_count INTEGER DEFAULT 0,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

	`CREATE INDEX IF NOT EXISTS idx_knowledge_1024_tenant
		ON knowledge_chunks_1024(tenant_id)`,

	`CREATE INDEX IF NOT EXISTS idx_knowledge_1024_content_hash
		ON knowledge_chunks_1024(content_hash)`,

	// experiences_1024 - Agent experiences with decay mechanism.
	`CREATE TABLE IF NOT EXISTS experiences_1024 (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			type VARCHAR(50) NOT NULL CHECK (type IN ('query', 'solution', 'failure', 'pattern', 'distilled')),
			input TEXT,
			output TEXT,
			embedding VECTOR(1024) NOT NULL,
			embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			score FLOAT DEFAULT 0.5 CHECK (score >= 0 AND score <= 1),
			success BOOLEAN DEFAULT true,
			agent_id VARCHAR(255),
			metadata JSONB DEFAULT '{}'::jsonb,
			decay_at TIMESTAMP DEFAULT NOW() + INTERVAL '30 days',
			created_at TIMESTAMP DEFAULT NOW()
		)`,

	`CREATE INDEX IF NOT EXISTS idx_experiences_1024_tenant
		ON experiences_1024(tenant_id)`,

	`CREATE INDEX IF NOT EXISTS idx_experiences_1024_decay
		ON experiences_1024(decay_at) WHERE decay_at IS NOT NULL`,

	// embedding_queue - Async embedding task queue with idempotency.
	`CREATE TABLE IF NOT EXISTS embedding_queue (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			task_id TEXT NOT NULL,
			table_name TEXT NOT NULL,
			content TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			embedding_model TEXT DEFAULT 'e5-large',
			embedding_version INT DEFAULT 1,
			dedupe_key TEXT UNIQUE,
			retry_count INTEGER DEFAULT 0,
			status TEXT DEFAULT 'pending',
			queued_at TIMESTAMP DEFAULT NOW(),
			processing_at TIMESTAMP,
			completed_at TIMESTAMP,
			error_message TEXT
		)`,

	`CREATE INDEX IF NOT EXISTS idx_embedding_queue_status ON embedding_queue(status, queued_at)
		WHERE status IN ('pending', 'processing')`,

	// embedding_dead_letter - Failed embedding tasks moved after max retries.
	`CREATE TABLE IF NOT EXISTS embedding_dead_letter (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			task_id TEXT NOT NULL,
			table_name TEXT NOT NULL,
			content TEXT NOT NULL,
			tenant_id TEXT NOT NULL,
			embedding_model TEXT,
			embedding_version INT,
			error_message TEXT,
			retry_count INTEGER,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

	`CREATE INDEX IF NOT EXISTS idx_embedding_dead_letter_tenant ON embedding_dead_letter(tenant_id)`,
	`CREATE INDEX IF NOT EXISTS idx_embedding_dead_letter_created ON embedding_dead_letter(created_at)`,

	// events - Event sourcing store with optimistic concurrency control.
	`CREATE TABLE IF NOT EXISTS events (
			id VARCHAR(255) NOT NULL,
			stream_id VARCHAR(255) NOT NULL,
			type VARCHAR(100) NOT NULL,
			payload JSONB NOT NULL,
			metadata JSONB DEFAULT '{}',
			version BIGINT NOT NULL,
			created_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (id)
		)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uq_events_stream_version ON events(stream_id, version)`,
	`CREATE INDEX IF NOT EXISTS idx_events_type ON events(type)`,

	// event_summaries - Compacted event summaries stored in relational DB (not vector DB).
	// Each summary represents a window of events that have been compacted/summarized
	// for long-running agent tasks. Bound to agent, task, and user request context.
	`CREATE TABLE IF NOT EXISTS event_summaries (
			id VARCHAR(255) PRIMARY KEY,
			stream_id VARCHAR(255) NOT NULL,
			agent_id VARCHAR(255) NOT NULL,
			task_id VARCHAR(255),
			session_id VARCHAR(255),
			user_id VARCHAR(255),
			summary_text TEXT NOT NULL,
			event_count INTEGER NOT NULL DEFAULT 0,
			start_version BIGINT NOT NULL,
			end_version BIGINT NOT NULL,
			start_time TIMESTAMP NOT NULL,
			end_time TIMESTAMP NOT NULL,
			event_type_counts JSONB DEFAULT '{}'::jsonb,
			tasks_created JSONB DEFAULT '[]'::jsonb,
			tools_called JSONB DEFAULT '[]'::jsonb,
			errors JSONB DEFAULT '[]'::jsonb,
			request_summary TEXT,
			outcome VARCHAR(50) NOT NULL DEFAULT 'active',
			metadata JSONB DEFAULT '{}'::jsonb,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

	`CREATE INDEX IF NOT EXISTS idx_event_summaries_stream ON event_summaries(stream_id)`,
	`CREATE INDEX IF NOT EXISTS idx_event_summaries_agent ON event_summaries(agent_id)`,
	`CREATE INDEX IF NOT EXISTS idx_event_summaries_agent_task ON event_summaries(agent_id, task_id)`,
	`CREATE INDEX IF NOT EXISTS idx_event_summaries_created ON event_summaries(created_at)`,

	// Evolution strategies — persisted state for autonomous evolution system.
	`CREATE TABLE IF NOT EXISTS evolution_strategies (
			id VARCHAR(255) PRIMARY KEY,
			is_active BOOLEAN NOT NULL DEFAULT false,
			name VARCHAR(255) NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			params JSONB DEFAULT '{}'::jsonb,
			parent_id VARCHAR(255) DEFAULT '',
			prompt_template TEXT DEFAULT '',
			strategy_mutation_type VARCHAR(100) DEFAULT '',
			mutation_desc TEXT DEFAULT '',
			score DOUBLE PRECISION DEFAULT -1,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

	`CREATE INDEX IF NOT EXISTS idx_evolution_strategies_active ON evolution_strategies(is_active)`,
	`CREATE INDEX IF NOT EXISTS idx_evolution_strategies_score ON evolution_strategies(score)`,

	// Rollback events — audit trail for strategy rollback decisions.
	`CREATE TABLE IF NOT EXISTS evolution_rollback_events (
			id SERIAL PRIMARY KEY,
			strategy_id VARCHAR(255) NOT NULL,
			previous_strategy_id VARCHAR(255) DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			decision JSONB DEFAULT '{}'::jsonb,
			current_score DOUBLE PRECISION DEFAULT 0,
			reference_score DOUBLE PRECISION DEFAULT 0,
			degradation DOUBLE PRECISION DEFAULT 0,
			threshold DOUBLE PRECISION DEFAULT 0,
			recommended_action TEXT DEFAULT '',
			created_at TIMESTAMP DEFAULT NOW()
		)`,

	`CREATE INDEX IF NOT EXISTS idx_rollback_events_strategy ON evolution_rollback_events(strategy_id)`,
	`CREATE INDEX IF NOT EXISTS idx_rollback_events_created ON evolution_rollback_events(created_at)`,
}

// Migrate runs database migrations.
func Migrate(ctx context.Context, pool *Pool) error {
	for i, migration := range coreMigrationStatements {
		if _, err := pool.Exec(ctx, migration); err != nil {
			return errors.Wrapf(err, "migration %d failed", i)
		}
	}
	return nil
}

// RollbackLast rolls back the last migration.
func RollbackLast(ctx context.Context, pool *Pool) error {
	// Note: This is a simplified implementation
	// In production, use a proper migration tool like golang-migrate
	return coreerrors.ErrQueryFailed
}

// Seed creates seed data for testing.
func Seed(ctx context.Context, pool *Pool) error {
	// Add sample data for testing
	return nil
}

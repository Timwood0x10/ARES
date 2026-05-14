// Package postgres provides PostgreSQL database operations for the storage system.
package postgres

import (
	"context"

	"github.com/Timwood0x10/goagent/internal/errors"
)

// MigrateStorage runs the storage system database migrations.
// This creates the new vector-based storage schema with 6 core tables and supporting indexes.
func MigrateStorage(ctx context.Context, pool *Pool) error {
	migrations := []string{
		// 1. knowledge_chunks_1024 table - RAG knowledge base with fixed 1024 dimensions
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

		// Enable RLS for knowledge_chunks_1024
		`ALTER TABLE knowledge_chunks_1024 ENABLE ROW LEVEL SECURITY`,

		// Create tenant isolation policy
		`CREATE POLICY tenant_isolation_knowledge_1024 ON knowledge_chunks_1024
		USING (tenant_id = current_setting('app.tenant_id', true))`,

		// Create auto-update trigger for tsv
		`CREATE TRIGGER tsvector_update_knowledge_1024 BEFORE INSERT OR UPDATE ON knowledge_chunks_1024
		FOR EACH ROW EXECUTE FUNCTION
		tsvector_update_trigger(tsv, 'pg_catalog.simple', content)`,

		// Create indexes for knowledge_chunks_1024
		`CREATE INDEX IF NOT EXISTS idx_knowledge_1024_embedding 
		ON knowledge_chunks_1024 
		USING ivfflat (embedding vector_cosine_ops) 
		WITH (lists = 100)`,

		`CREATE INDEX IF NOT EXISTS idx_knowledge_1024_tsv 
		ON knowledge_chunks_1024 
		USING GIN(tsv)`,

		`CREATE INDEX IF NOT EXISTS idx_knowledge_1024_doc_chunk 
		ON knowledge_chunks_1024(document_id, chunk_index)`,

		`CREATE INDEX IF NOT EXISTS idx_knowledge_1024_source_type 
		ON knowledge_chunks_1024(source_type)`,

		`CREATE INDEX IF NOT EXISTS idx_knowledge_1024_tenant 
		ON knowledge_chunks_1024(tenant_id)`,

		`CREATE INDEX IF NOT EXISTS idx_knowledge_1024_content_hash 
		ON knowledge_chunks_1024(content_hash)`,

		// 2. experiences_1024 table - Agent experiences with decay mechanism
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

		`ALTER TABLE experiences_1024 ENABLE ROW LEVEL SECURITY`,

		`CREATE POLICY tenant_isolation_experiences_1024 ON experiences_1024
		USING (tenant_id = current_setting('app.tenant_id', true))`,

		// Create indexes for experiences_1024
		`CREATE INDEX IF NOT EXISTS idx_experiences_1024_embedding 
		ON experiences_1024 
		USING ivfflat (embedding vector_cosine_ops) 
		WITH (lists = 100)`,

		`CREATE INDEX IF NOT EXISTS idx_experiences_1024_type 
		ON experiences_1024(type)`,

		`CREATE INDEX IF NOT EXISTS idx_experiences_1024_agent 
		ON experiences_1024(agent_id)`,

		`CREATE INDEX IF NOT EXISTS idx_experiences_1024_score 
		ON experiences_1024(score DESC)`,

		`CREATE INDEX IF NOT EXISTS idx_experiences_1024_tenant 
		ON experiences_1024(tenant_id)`,

		`CREATE INDEX IF NOT EXISTS idx_experiences_1024_decay 
		ON experiences_1024(decay_at) WHERE decay_at IS NOT NULL`,

		// 3. tools table - Tools with semantic embedding
		`CREATE TABLE IF NOT EXISTS tools (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			embedding VECTOR(1024) NOT NULL,
			embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			agent_type VARCHAR(50),
			tags TEXT[] DEFAULT ARRAY[]::TEXT[],
			usage_count INTEGER DEFAULT 0,
			success_rate FLOAT DEFAULT 0.0,
			last_used_at TIMESTAMP,
			metadata JSONB DEFAULT '{}'::jsonb,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`ALTER TABLE tools ENABLE ROW LEVEL SECURITY`,

		`CREATE POLICY tenant_isolation_tools ON tools
		USING (tenant_id = current_setting('app.tenant_id', true))`,

		// Create indexes for tools
		`CREATE INDEX IF NOT EXISTS idx_tools_tenant_name 
		ON tools(tenant_id, name)`,

		`CREATE INDEX IF NOT EXISTS idx_tools_usage_count 
		ON tools(usage_count DESC)`,

		`CREATE INDEX IF NOT EXISTS idx_tools_agent_type 
		ON tools(agent_type)`,

		`CREATE INDEX IF NOT EXISTS idx_tools_tags 
		ON tools USING GIN(tags)`,

		`CREATE INDEX IF NOT EXISTS idx_tools_embedding 
		ON tools 
		USING ivfflat (embedding vector_cosine_ops) 
		WHERE embedding IS NOT NULL`,

		// 4. conversations table - Conversation history without vector embedding
		`CREATE TABLE IF NOT EXISTS conversations (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			session_id VARCHAR(64) NOT NULL,
			tenant_id TEXT NOT NULL,
			user_id VARCHAR(64),
			agent_id VARCHAR(64),
			role VARCHAR(32) NOT NULL,
			content TEXT NOT NULL,
			expires_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`ALTER TABLE conversations ENABLE ROW LEVEL SECURITY`,

		`CREATE POLICY tenant_isolation_conversations ON conversations
		USING (tenant_id = current_setting('app.tenant_id', true))`,

		// Create indexes for conversations
		`CREATE INDEX IF NOT EXISTS idx_conversations_session 
		ON conversations(session_id, created_at)`,

		`CREATE INDEX IF NOT EXISTS idx_conversations_tenant 
		ON conversations(tenant_id)`,

		`CREATE INDEX IF NOT EXISTS idx_conversations_user 
		ON conversations(user_id, created_at)`,

		`CREATE INDEX IF NOT EXISTS idx_conversations_agent 
		ON conversations(agent_id, created_at)`,

		`CREATE INDEX IF NOT EXISTS idx_conversations_expires 
		ON conversations(expires_at) WHERE expires_at IS NOT NULL`,

		// 5. task_results_1024 table - Task execution results with vector embedding
		`CREATE TABLE IF NOT EXISTS task_results_1024 (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			session_id VARCHAR(64) NOT NULL,
			task_type VARCHAR(64),
			agent_id VARCHAR(64),
			input JSONB NOT NULL,
			output JSONB,
			embedding VECTOR(1024),
			embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			status VARCHAR(32) NOT NULL DEFAULT 'pending',
			error TEXT,
			latency_ms INTEGER,
			metadata JSONB DEFAULT '{}'::jsonb,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`ALTER TABLE task_results_1024 ENABLE ROW LEVEL SECURITY`,

		`CREATE POLICY tenant_isolation_task_results_1024 ON task_results_1024
		USING (tenant_id = current_setting('app.tenant_id', true))`,

		// Create indexes for task_results_1024
		`CREATE INDEX IF NOT EXISTS idx_task_results_1024_embedding 
		ON task_results_1024 
		USING ivfflat (embedding vector_cosine_ops) 
		WHERE embedding IS NOT NULL`,

		`CREATE INDEX IF NOT EXISTS idx_task_results_1024_type 
		ON task_results_1024(task_type)`,

		`CREATE INDEX IF NOT EXISTS idx_task_results_1024_status 
		ON task_results_1024(status)`,

		`CREATE INDEX IF NOT EXISTS idx_task_results_1024_session 
		ON task_results_1024(session_id)`,

		`CREATE INDEX IF NOT EXISTS idx_task_results_1024_agent 
		ON task_results_1024(agent_id)`,

		`CREATE INDEX IF NOT EXISTS idx_task_results_1024_tenant 
		ON task_results_1024(tenant_id)`,

		// 6. secrets table - Encrypted sensitive data
		`CREATE TABLE IF NOT EXISTS secrets (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			key VARCHAR(255) NOT NULL,
			value BYTEA NOT NULL,
			key_version INTEGER NOT NULL DEFAULT 1,
			algorithm VARCHAR(32) NOT NULL DEFAULT 'aes-gcm',
			expires_at TIMESTAMP,
			metadata JSONB DEFAULT '{}'::jsonb,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`ALTER TABLE secrets ENABLE ROW LEVEL SECURITY`,

		`CREATE POLICY tenant_isolation_secrets ON secrets
		USING (tenant_id = current_setting('app.tenant_id', true))`,

		// Create indexes for secrets
		`CREATE INDEX IF NOT EXISTS idx_secrets_tenant_key 
		ON secrets(tenant_id, key)`,

		`CREATE INDEX IF NOT EXISTS idx_secrets_tenant 
		ON secrets(tenant_id)`,

		`CREATE INDEX IF NOT EXISTS idx_secrets_expires 
		ON secrets(expires_at) WHERE expires_at IS NOT NULL`,

		`CREATE INDEX IF NOT EXISTS idx_secrets_key_version 
		ON secrets(key_version)`,

		// 7. embedding_queue table - Async embedding task queue with idempotency
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

		// Create indexes for embedding_queue
		`CREATE UNIQUE INDEX idx_embedding_queue_dedupe ON embedding_queue(dedupe_key)`,

		`CREATE INDEX idx_embedding_queue_status ON embedding_queue(status, queued_at) 
		WHERE status IN ('pending', 'processing')`,

		// 8. embedding_dead_letter table - Failed embedding tasks
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

		// Create indexes for embedding_dead_letter
		`CREATE INDEX idx_embedding_dead_letter_tenant ON embedding_dead_letter(tenant_id)`,
		`CREATE INDEX idx_embedding_dead_letter_created ON embedding_dead_letter(created_at)`,

		// 9. distilled_memories table - Distilled conversation memories for cross-session context
		`CREATE TABLE IF NOT EXISTS distilled_memories (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			user_id TEXT,
			session_id VARCHAR(64) NOT NULL,
			content TEXT NOT NULL,
			embedding VECTOR(1024) NOT NULL,
			embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			memory_type VARCHAR(50) NOT NULL CHECK (memory_type IN ('preference', 'interaction', 'profile', 'knowledge')),
			importance FLOAT DEFAULT 0.5 CHECK (importance >= 0 AND importance <= 1),
			metadata JSONB DEFAULT '{}'::jsonb,
			access_count INTEGER DEFAULT 0,
			last_accessed_at TIMESTAMP,
			expires_at TIMESTAMP DEFAULT NOW() + INTERVAL '90 days',
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`ALTER TABLE distilled_memories ENABLE ROW LEVEL SECURITY`,

		`CREATE POLICY tenant_isolation_distilled_memories ON distilled_memories
		USING (tenant_id = current_setting('app.tenant_id', true))`,

		// Create indexes for distilled_memories
		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_embedding
		ON distilled_memories
		USING ivfflat (embedding vector_cosine_ops)
		WITH (lists = 100)`,

		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_user
		ON distilled_memories(user_id, created_at)`,

		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_session
		ON distilled_memories(session_id)`,

		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_type
		ON distilled_memories(memory_type)`,

		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_importance
		ON distilled_memories(importance DESC)`,

		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_expires
		ON distilled_memories(expires_at) WHERE expires_at IS NOT NULL`,

		`CREATE INDEX IF NOT EXISTS idx_distilled_memories_tenant
		ON distilled_memories(tenant_id)`,
	}

	// Execute migrations
	for _, migration := range migrations {
		if _, err := pool.db.ExecContext(ctx, migration); err != nil {
			return errors.Wrap(err, "migration failed")
		}
	}

	return nil
}

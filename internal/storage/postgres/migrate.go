package postgres

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/errors"
)

// coreMigrationStatements contains the DDL for core application tables.
//
// Note: The storage/vector tables (knowledge_chunks_1024, experiences_1024,
// embedding_queue, embedding_dead_letter, tools, conversations,
// task_results_1024, secrets) are owned by
// migrate_storage.go (storageMigrations) which defines them with full
// Row-Level Security policies and complete indexes. They must NOT be
// duplicated here to avoid schema drift between the two definitions.
// The distilled_memories DDL was REMOVED: its repository
// and tools were deleted as a schema ghost, so fresh deployments no longer
// create the table; existing databases keep theirs (removal is inert for
// them).
// DefaultTenantID is the tenant assigned to rows that predate the multi-tenant
// column backfill and to single-tenant deployments that never set a tenant.
// Every tenant-scoped query filters on tenant_id, so a row must always carry
// exactly one — the migration below backfills legacy rows with this value.
const DefaultTenantID = "default"

var coreMigrationStatements = []string{
	// user_profiles: PK stays user_id. Note for future multi-tenant hardening:
	// uniqueness is still GLOBAL on user_id, so two tenants cannot register the
	// same user_id. Promoting the PK to (tenant_id, user_id) is a separate,
	// breaking schema change deferred until a second tenant actually exists.
	`CREATE TABLE IF NOT EXISTS user_profiles (
			user_id VARCHAR(255) PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT 'default',
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

	// Backfill tenant_id for databases created before the multi-tenant column.
	// Idempotent: IF NOT EXISTS makes a re-run a no-op. The DEFAULT assigns
	// legacy rows to DefaultTenantID so the NOT NULL constraint holds.
	`ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default'`,

	`CREATE TABLE IF NOT EXISTS sessions (
			session_id VARCHAR(255) PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT 'default',
			user_id VARCHAR(255) NOT NULL,
			input TEXT,
			status VARCHAR(50),
			user_profile JSONB,
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			expired_at TIMESTAMP
		)`,

	`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default'`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_expired_at ON sessions(expired_at)`,

	`CREATE TABLE IF NOT EXISTS recommendations (
			id SERIAL PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT 'default',
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

	`ALTER TABLE recommendations ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default'`,
	`CREATE INDEX IF NOT EXISTS idx_recommendations_user_id ON recommendations(user_id)`,
	`CREATE INDEX IF NOT EXISTS idx_recommendations_created_at ON recommendations(created_at)`,

	// The embeddings table carries no dedicated repository (VectorSearcher
	// owns per-collection tables), but its vector column MUST declare the
	// same dimension as everything else in this package
	// (defaultVectorDimension): a hardcoded 1536 here made any writer using
	// the codebase-standard 1024-dim embeddings fail the insert.
	fmt.Sprintf(`CREATE TABLE IF NOT EXISTS embeddings (
			id VARCHAR(255) PRIMARY KEY,
			table_name VARCHAR(100) NOT NULL,
			embedding VECTOR(%d),
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)`, defaultVectorDimension),

	`CREATE INDEX IF NOT EXISTS idx_embeddings_table_name ON embeddings(table_name)`,

	// agent_checkpoints - session checkpoints for agent recovery. Renamed from
	// leader_checkpoints. The DO block migrates
	// existing databases without data loss; on fresh databases all IF EXISTS
	// checks are no-ops. Rename only when the target table is absent — if
	// agent_checkpoints already exists (idempotent re-run), the old table's
	// data was already migrated and a rename would collide.
	`DO $$ BEGIN
		IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'leader_checkpoints')
			AND NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'agent_checkpoints') THEN
			ALTER TABLE leader_checkpoints RENAME TO agent_checkpoints;
		END IF;
		IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'agent_checkpoints') THEN
			IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'agent_checkpoints' AND column_name = 'leader_id') THEN
				ALTER TABLE agent_checkpoints RENAME COLUMN leader_id TO agent_id;
			END IF;
			IF EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_leader_checkpoints_status') THEN
				ALTER INDEX idx_leader_checkpoints_status RENAME TO idx_agent_checkpoints_status;
			END IF;
		END IF;
	END $$;`,

	// agent_checkpoints carries a tenant_id column for schema parity, but note
	// it currently has NO production query against it (only this DDL and a
	// base_repository whitelist entry) — so the column is future readiness,
	// not a live isolation barrier.
	`CREATE TABLE IF NOT EXISTS agent_checkpoints (
			agent_id VARCHAR(255) NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT 'default',
			session_id VARCHAR(255) NOT NULL,
			status VARCHAR(50) NOT NULL DEFAULT 'active',
			metadata JSONB DEFAULT '{}'::jsonb,
			updated_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (agent_id)
		)`,

	`ALTER TABLE agent_checkpoints ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default'`,
	`CREATE INDEX IF NOT EXISTS idx_agent_checkpoints_status ON agent_checkpoints(status)`,

	// events - Event sourcing store with optimistic concurrency control.
	// tenant_id is schema readiness only — NOT a live isolation barrier: the
	// events store (internal/ares_events PG store) neither reads nor filters
	// this column today; an event's tenant scope lives in the distilled-hint
	// payload key (EventKeyTenantID), not in stream queries. Per-tenant
	// stream reads are future work; until then, note uq_events_stream_version
	// below is also not tenant-scoped, so a second tenant reusing a
	// stream_id fails on version uniqueness (fail-closed reject, not a leak).
	`CREATE TABLE IF NOT EXISTS events (
			id VARCHAR(255) NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT 'default',
			stream_id VARCHAR(255) NOT NULL,
			type VARCHAR(100) NOT NULL,
			payload JSONB NOT NULL,
			metadata JSONB DEFAULT '{}',
			version BIGINT NOT NULL,
			created_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (id)
		)`,

	`ALTER TABLE events ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default'`,
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
	//
	// Schema mirrors runtime/ares_evolution's PGStrategyStore, which is the only
	// store actually wired in production (ares_bootstrap). It stores one row per
	// strategy *version* (BIGSERIAL id + strategy_id) rather than one row per
	// strategy, because GetHistory must be able to return prior versions — a
	// model that a VARCHAR primary key cannot express.
	//
	// NOTE: the storage-layer StrategyRepository that assumed a one-row-per-
	// strategy schema (id VARCHAR PK, strategy_mutation_type, updated_at) was
	// deleted as unwired dead code — it had no production caller and could not
	// express the append-only history this store needs. Do not reintroduce a
	// VARCHAR-keyed repository against this table; this shape is the only
	// schema authority.
	//
	// Pre-existing deployments whose evolution_strategies was created with the
	// older incompatible shape (VARCHAR id PK, no strategy_id column) keep
	// booting: CREATE TABLE IF NOT EXISTS is inert on them and the strategy_id
	// index below is guarded on column existence. They must still rebuild the
	// table to use the PG strategy store — the primary key type change cannot
	// be expressed as an ALTER, and their shape cannot accept this store's
	// INSERTs anyway (strategy_id NOT NULL); until rebuilt, the store's own
	// createTable fails on that shape exactly as it did before this change.
	`CREATE TABLE IF NOT EXISTS evolution_strategies (
			id BIGSERIAL PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT 'default',
			strategy_id TEXT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			name TEXT NOT NULL DEFAULT '',
			parent_id TEXT NOT NULL DEFAULT '',
			prompt_template TEXT NOT NULL DEFAULT '',
			mutation_type TEXT NOT NULL DEFAULT '',
			mutation_desc TEXT NOT NULL DEFAULT '',
			params JSONB NOT NULL DEFAULT '{}',
			score DOUBLE PRECISION NOT NULL DEFAULT -1,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			is_active BOOLEAN NOT NULL DEFAULT FALSE
		)`,

	`ALTER TABLE evolution_strategies ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default'`,
	// Legacy-shape tables (pre-BIGSERIAL) have no strategy_id column; creating
	// this index unconditionally would abort Migrate and break boot on them.
	`DO $$ BEGIN
		IF EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'evolution_strategies'
			  AND column_name = 'strategy_id'
		) THEN
			EXECUTE 'CREATE INDEX IF NOT EXISTS idx_evolution_strategies_sid ON evolution_strategies(strategy_id)';
		END IF;
	END $$;`,
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
// The migration list is a flat set of idempotent DDL statements without a
// version table, so a precise rollback is not possible. It validates the
// pool and returns a clear error instead of pretending success.
// TODO: introduce a schema_migrations version table to enable real rollback
// (expected by 2026-12-31).
func RollbackLast(ctx context.Context, pool *Pool) error {
	if pool == nil {
		return errors.Wrap(errors.ErrNilPointer, "rollback last migration")
	}
	return errors.Wrap(errors.ErrRollbackUnsupported, "rollback last migration")
}

// Seed creates seed data for testing.
// It inserts a sample user profile idempotently so tests and demo setups
// have baseline data without requiring an external fixture.
func Seed(ctx context.Context, pool *Pool) error {
	if pool == nil {
		return errors.Wrap(errors.ErrNilPointer, "seed data")
	}
	query := `
		INSERT INTO user_profiles (user_id, name, created_at, updated_at)
		VALUES ('seed-user', 'Seed User', NOW(), NOW())
		ON CONFLICT (user_id) DO NOTHING
	`
	if _, err := pool.Exec(ctx, query); err != nil {
		return errors.Wrap(err, "seed user profile")
	}
	return nil
}

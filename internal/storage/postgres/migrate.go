package postgres

import (
	"context"

	coreerrors "goagent/internal/core/errors"
	"goagent/internal/errors"
)

// Migrate runs database migrations.
func Migrate(ctx context.Context, pool *Pool) error {
	migrations := []string{
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
	}

	for i, migration := range migrations {
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

package postgres

import (
	"context"

	"github.com/Timwood0x10/ares/internal/errors"
)

// evalMigrationStatements contains DDL for evaluation results storage tables.
// These tables persist agent evaluation run results to support historical
// analysis, leaderboard tracking, and side-by-side comparison queries.
var evalMigrationStatements = []string{
	`CREATE TABLE IF NOT EXISTS eval_results (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			run_id VARCHAR(255) NOT NULL,
			config_name TEXT NOT NULL,
			suite_name TEXT NOT NULL,
			test_case_id TEXT NOT NULL,
			test_case_name TEXT NOT NULL,
			score DOUBLE PRECISION DEFAULT 0,
			dimensions JSONB DEFAULT '{}'::jsonb,
			status TEXT NOT NULL DEFAULT 'error',
			error_message TEXT,
			duration_ms INTEGER DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uq_eval_results_run_config_test
		ON eval_results(run_id, config_name, test_case_id)`,

	`CREATE INDEX IF NOT EXISTS idx_eval_results_run_id
		ON eval_results(run_id)`,

	`CREATE INDEX IF NOT EXISTS idx_eval_results_config_name
		ON eval_results(config_name)`,

	`CREATE INDEX IF NOT EXISTS idx_eval_results_created_at
		ON eval_results(created_at DESC)`,
}

// MigrateEval creates the evaluation results storage tables if they do not exist.
// This is a separate migration call so eval API integration is optional
// (systems that don't use eval persistence don't need these tables).
//
// Args:
//
//	ctx - database operation context.
//	pool - database connection pool.
//
// Returns:
//
//	error - non-nil if any migration statement fails.
func MigrateEval(ctx context.Context, pool *Pool) error {
	for i, migration := range evalMigrationStatements {
		if _, err := pool.Exec(ctx, migration); err != nil {
			return errors.Wrapf(err, "eval migration %d failed", i)
		}
	}
	return nil
}

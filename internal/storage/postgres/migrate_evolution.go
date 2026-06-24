package postgres

import (
	"context"

	"github.com/Timwood0x10/ares/internal/errors"
)

// evolutionMigrationStatements contains DDL for autonomous evolution storage tables.
// These tables persist strategy snapshots and genealogy lineage records
// to support evolution state restoration and post-hoc analysis.
var evolutionMigrationStatements = []string{
	`CREATE TABLE IF NOT EXISTS evolution_strategies (
			id VARCHAR(255) PRIMARY KEY,
			parent_id VARCHAR(255),
			name VARCHAR(255) NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			params JSONB DEFAULT '{}',
			prompt_template TEXT DEFAULT '',
			strategy_mutation_type VARCHAR(50) DEFAULT 'unknown',
			mutation_desc TEXT DEFAULT '',
			score DOUBLE PRECISION DEFAULT -1,
			is_active BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

	`CREATE INDEX IF NOT EXISTS idx_evolution_strategies_is_active
		 ON evolution_strategies(is_active)`,

	`CREATE INDEX IF NOT EXISTS idx_evolution_strategies_version
		 ON evolution_strategies(version DESC)`,

	`CREATE TABLE IF NOT EXISTS evolution_lineages (
			id BIGSERIAL PRIMARY KEY,
			parent_id VARCHAR(255) NOT NULL,
			child_id VARCHAR(255) NOT NULL,
			mutation_type VARCHAR(50) DEFAULT '',
			win_rate DOUBLE PRECISION DEFAULT 0,
			score_improvement DOUBLE PRECISION DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

	`CREATE INDEX IF NOT EXISTS idx_evolution_lineages_parent
		 ON evolution_lineages(parent_id)`,

	`CREATE INDEX IF NOT EXISTS idx_evolution_lineages_child
		 ON evolution_lineages(child_id)`,
}

// MigrateEvolution creates the evolution storage tables if they do not exist.
// This is a separate migration call so evolution integration is optional
// (systems that don't use evolution don't need these tables).
//
// Args:
//
//	ctx - database operation context.
//	pool - database connection pool.
//
// Returns:
//
//	error - non-nil if any migration statement fails.
func MigrateEvolution(ctx context.Context, pool *Pool) error {
	for i, migration := range evolutionMigrationStatements {
		if _, err := pool.Exec(ctx, migration); err != nil {
			return errors.Wrapf(err, "evolution migration %d failed", i)
		}
	}
	return nil
}

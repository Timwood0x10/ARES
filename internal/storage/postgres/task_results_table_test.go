package postgres

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// TestTaskResultsTable_ConstantValue verifies the shared constant still
// holds the canonical table name. This is the single source of truth that
// every other file must reference.
func TestTaskResultsTable_ConstantValue(t *testing.T) {
	assert.Equal(t, "task_results_1024", models.TaskResultsTable,
		"TaskResultsTable must equal the canonical table name")
}

// TestTaskResultsTable_AllowedTablesWhitelist verifies that the
// allowedTables whitelist (used by GetByID/DeleteByID/CountByTenant for
// SQL-injection defense) includes the shared constant, so the runtime
// whitelist stays in sync with the model's table name.
func TestTaskResultsTable_AllowedTablesWhitelist(t *testing.T) {
	_, ok := allowedTables[models.TaskResultsTable]
	assert.True(t, ok,
		"allowedTables must contain models.TaskResultsTable (%q)",
		models.TaskResultsTable)
}

// TestTaskResultsTable_MigrationsReferenceConstant verifies that every
// migration statement that touches the task-results table uses the shared
// constant value. Because storageMigrations is built via string
// concatenation with models.TaskResultsTable, each statement that
// references the table must contain the constant's value. This is the
// runtime mirror of the grep-verified call sites.
func TestTaskResultsTable_MigrationsReferenceConstant(t *testing.T) {
	table := models.TaskResultsTable
	// Count migration statements that reference the table name. We expect
	// at least the CREATE TABLE plus ALTER TABLE, policy, and index
	// statements — i.e. more than one.
	var hits int
	for _, stmt := range storageMigrations {
		if strings.Contains(stmt, table) {
			hits++
		}
	}
	assert.GreaterOrEqual(t, hits, 5,
		"expected at least 5 migration statements referencing %q, got %d",
		table, hits)
}

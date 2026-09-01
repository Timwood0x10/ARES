package postgres

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findMigration returns the single migration statement containing marker.
func findMigration(t *testing.T, marker string) string {
	t.Helper()
	var found []string
	for _, stmt := range storageMigrations {
		if strings.Contains(stmt, marker) {
			found = append(found, stmt)
		}
	}
	require.Len(t, found, 1, "expected exactly one migration containing %q", marker)
	return found[0]
}

// TestVectorIndexes_ArePartialOnNotNull locks the planner-side half of the
// NULL-embedding fix. The readers filter `embedding IS NOT NULL`; the ivfflat
// indexes must declare the same predicate so the planner can prove the filter
// is implied and still choose the index.
//
// This is NOT about row counts: `ORDER BY <distance>` is ascending and
// PostgreSQL sorts NULLs last, so a vector-less row can never displace a real
// result. It is about not scanning rows that cannot be ranked and about giving
// the planner a usable predicate.
func TestVectorIndexes_ArePartialOnNotNull(t *testing.T) {
	cases := []struct {
		name   string
		marker string
	}{
		{"knowledge chunks", "idx_knowledge_1024_embedding"},
		{"experiences", "idx_experiences_1024_embedding"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt := findMigration(t, tc.marker)
			assert.Contains(t, stmt, "USING ivfflat",
				"vector index must stay an ivfflat index")
			assert.Contains(t, stmt, "WHERE embedding IS NOT NULL",
				"vector index must be partial so it matches the reader's filter")
			// WITH must precede WHERE, otherwise PostgreSQL rejects the DDL.
			withIdx := strings.Index(stmt, "WITH (")
			whereIdx := strings.Index(stmt, "WHERE ")
			require.NotEqual(t, -1, withIdx, "expected a WITH (lists = ...) clause")
			assert.Less(t, withIdx, whereIdx,
				"WITH (lists = ...) must come before WHERE in CREATE INDEX")
		})
	}
}

// TestNullableEmbeddingColumns_StayNullable guards the premise the readers rely
// on: these two vector columns are nullable, which is exactly why the readers
// need an explicit IS NOT NULL filter. A future migration that marks either
// column NOT NULL would break the async-backfill insert path.
func TestNullableEmbeddingColumns_StayNullable(t *testing.T) {
	knowledge := findMigration(t, "CREATE TABLE IF NOT EXISTS knowledge_chunks_1024")
	assert.Contains(t, knowledge, "embedding VECTOR(1024),",
		"knowledge_chunks_1024.embedding must stay nullable for async backfill")

	experiences := findMigration(t, "CREATE TABLE IF NOT EXISTS experiences_1024")
	assert.Contains(t, experiences, "embedding VECTOR(1024),",
		"experiences_1024.embedding must stay nullable for async backfill")
}

package postgres

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVectorCollectionDDLCarriesTenantID locks the tenant_id column into the
// ad-hoc collection DDL produced by the production builder.
//
// Regression (docs/reviews/0.3.1-final-deep-review.md §5.3 S-4): CreateCollection
// created a table without tenant_id while Search ran `WHERE tenant_id = $3`, so
// every search of a collection created through this API failed with
// `column "tenant_id" does not exist`. knowledge/provider/vector calls
// CreateCollection at construction and Search on every Stream, so the
// combination was unusable in a real configuration.
func TestVectorCollectionDDLCarriesTenantID(t *testing.T) {
	ddl := vectorCollectionDDL("docs", 1024)

	require.Contains(t, ddl, "tenant_id", "collection DDL must declare tenant_id because Search filters on it")
	assert.Contains(t, ddl, "TEXT NOT NULL DEFAULT ''",
		"tenant_id must have a default so writes that carry no tenant are storable")
	assert.Contains(t, ddl, "VECTOR(1024)", "the requested dimension must reach the DDL")
	assert.Contains(t, ddl, "CREATE TABLE IF NOT EXISTS docs", "the table name must be interpolated")
}

// TestVectorCollectionDDLUsesPerCollectionName verifies the builder does not
// hardcode a name, so a missing parameter cannot silently create the wrong
// table.
func TestVectorCollectionDDLUsesPerCollectionName(t *testing.T) {
	first := vectorCollectionDDL("alpha", 4)
	second := vectorCollectionDDL("beta", 8)

	assert.NotContains(t, first, "beta")
	assert.Contains(t, first, "CREATE TABLE IF NOT EXISTS alpha")
	assert.Contains(t, second, "CREATE TABLE IF NOT EXISTS beta")
	assert.Contains(t, second, "VECTOR(8)")
}

// TestVectorCollectionDDLDoesNotEmitEmptyVectorLiteral guards against the DDL
// ever regressing into something pgvector rejects.
func TestVectorCollectionDDLDoesNotEmitEmptyVectorLiteral(t *testing.T) {
	ddl := vectorCollectionDDL("docs", 1024)
	assert.NotContains(t, strings.ReplaceAll(ddl, " ", ""), "VECTOR()")
}

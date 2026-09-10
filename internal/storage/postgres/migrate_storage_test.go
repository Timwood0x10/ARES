package postgres

import (
	"fmt"
	"strings"
	"testing"
)

// TestStorageMigrations_PerTenantContentHash locks REVIEW 2.6#31: the
// knowledge_chunks_1024 dedup constraint must be scoped per tenant.
//
//   - fresh databases: the CREATE TABLE must NOT declare a global
//     `content_hash TEXT UNIQUE`;
//   - existing databases: guarded DO-block migrations must drop any legacy
//     single-column unique constraint on content_hash and add
//     uq_knowledge_1024_tenant_content_hash instead. Both statements are
//     idempotent so repeated `db migrate` runs are inert.
func TestStorageMigrations_PerTenantContentHash(t *testing.T) {
	var createTable, dropLegacy, addTenantScoped bool
	for _, m := range storageMigrations {
		if strings.Contains(m, "CREATE TABLE IF NOT EXISTS knowledge_chunks_1024") {
			createTable = true
			if strings.Contains(m, "content_hash TEXT UNIQUE") {
				t.Error("CREATE TABLE must not declare a global content_hash UNIQUE; " +
					"uniqueness is per (tenant_id, content_hash)")
			}
		}
		if strings.Contains(m, "pg_constraint") && strings.Contains(m, "DROP CONSTRAINT") &&
			strings.Contains(m, "ARRAY['content_hash']") {
			dropLegacy = true
		}
		if strings.Contains(m, "ADD CONSTRAINT uq_knowledge_1024_tenant_content_hash") &&
			strings.Contains(m, "UNIQUE (tenant_id, content_hash)") {
			addTenantScoped = true
		}
	}
	if !createTable {
		t.Fatal("knowledge_chunks_1024 CREATE TABLE statement not found in migrations")
	}
	if !dropLegacy {
		t.Error("missing guarded migration that drops the legacy global content_hash unique constraint")
	}
	if !addTenantScoped {
		t.Error("missing migration that adds uq_knowledge_1024_tenant_content_hash")
	}
}

// TestCoreMigrationsEmbeddingsDimensionMatchesCanonical locks REVIEW 3.5b:
// the legacy embeddings table used to declare VECTOR(1536) while every other
// vector column in the package is defaultVectorDimension (1024). A writer
// using the canonical dimension could never insert into the mismatched
// table. The DDL must derive from the single source of truth.
func TestCoreMigrationsEmbeddingsDimensionMatchesCanonical(t *testing.T) {
	want := fmt.Sprintf("embedding VECTOR(%d)", defaultVectorDimension)
	found := false
	for _, m := range coreMigrationStatements {
		if strings.Contains(m, "CREATE TABLE IF NOT EXISTS embeddings (") {
			found = true
			if !strings.Contains(m, want) {
				t.Errorf("embeddings table must declare %s (canonical dimension), DDL:\n%s", want, m)
			}
			if strings.Contains(m, "VECTOR(1536)") {
				t.Errorf("embeddings table must not hardcode VECTOR(1536); it diverges from the canonical dimension")
			}
		}
	}
	if !found {
		t.Fatal("embeddings CREATE TABLE statement not found in coreMigrationStatements")
	}
}

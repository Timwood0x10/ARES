package postgres

import (
	"strings"
	"testing"
)

// TestStorageMigrationsNeverActivateRLS locks the signed 方案 B decision
// (plan/0.3.1plan/tenant_isolation.md, 残骸清除 2026-09-13): tenant isolation
// is carried by explicit tenant_id predicates in repository queries only —
// never by RLS policies.
//
// Two failure modes this guards against:
//
//  1. CREATE POLICY has no IF NOT EXISTS form, and MigrateStorage re-runs the
//     whole statement list on every boot with no version tracking. A bare
//     CREATE POLICY therefore breaks the second boot against any database
//     (including one this build itself created) with "policy already exists".
//  2. An ENABLEd policy that reads current_setting('app.tenant_id', true)
//     silently blanks every row for non-owner roles (the app itself connects
//     as the owner and bypasses RLS), implying a DB-level backstop that does
//     not exist for the application.
//
// The cleanup statements (DISABLE ROW LEVEL SECURITY / DROP POLICY IF EXISTS)
// are idempotent and must stay: they strip residue from databases created by
// older builds that still carry the policies.
func TestStorageMigrationsNeverActivateRLS(t *testing.T) {
	for i, migration := range storageMigrations {
		if strings.Contains(migration, "CREATE POLICY") {
			t.Errorf("migration[%d]: CREATE POLICY is forbidden — non-idempotent under the version-less every-boot runner, and signed 方案 B keeps isolation in application-layer predicates:\n%s", i, migration)
		}
		if strings.Contains(migration, "ENABLE ROW LEVEL SECURITY") {
			t.Errorf("migration[%d]: ENABLE ROW LEVEL SECURITY is forbidden — inert for the owner connection, silent blackout for non-owner roles; signed 方案 B keeps isolation in application-layer predicates:\n%s", i, migration)
		}
	}
}

// Package repositories provides tenant-isolation contract tests for the
// conversation repository that run without a database.
package repositories

import (
	"context"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// TestConversationGetByIDQueryIsTenantScoped locks the tenant predicate on the
// conversation lookup query.
//
// Regression (docs/reviews/0.3.1-final-deep-review.md finding S-1): GetByID took
// no tenant argument and ran `WHERE id = $1`, so any caller holding a UUID
// could read another tenant's conversation. RLS does not cover this path —
// repositories query the raw *sql.DB and never set app.tenant_id — so the
// explicit predicate is the only isolation barrier.
func TestConversationGetByIDQueryIsTenantScoped(t *testing.T) {
	q := conversationGetByIDQuery
	if !strings.Contains(q, "tenant_id") {
		t.Fatalf("conversationGetByIDQuery must filter by tenant_id, got:\n%s", q)
	}
	if !strings.Contains(q, "WHERE id = $1 AND tenant_id = $2") {
		t.Fatalf("conversationGetByIDQuery must scope the lookup to (id, tenant_id), got:\n%s", q)
	}
}

// TestConversationGetByIDRejectsEmptyTenant locks the fail-closed guard: an
// empty tenant is rejected before any SQL runs, so it can never silently
// degrade into an unscoped (cross-tenant) lookup.
//
// The repository is constructed with a nil DB on purpose — the guard must
// fire before the handle is dereferenced, and a nil handle proves it did.
func TestConversationGetByIDRejectsEmptyTenant(t *testing.T) {
	repo := NewConversationRepository(nil)

	_, err := repo.GetByID(context.Background(), "", "some-id")
	if err == nil {
		t.Fatal("GetByID with an empty tenant must fail, got nil error")
	}
	if err != postgres.ErrMissingTenantID {
		t.Fatalf("GetByID empty tenant error = %v, want ErrMissingTenantID", err)
	}
}

// TestConversationGetByIDRejectsEmptyID verifies the existing argument guard
// still runs first, so an empty id keeps returning ErrInvalidArgument rather
// than changing error identity.
func TestConversationGetByIDRejectsEmptyID(t *testing.T) {
	repo := NewConversationRepository(nil)

	_, err := repo.GetByID(context.Background(), "tenant-1", "")
	if err == nil {
		t.Fatal("GetByID with an empty id must fail, got nil error")
	}
	if !strings.Contains(err.Error(), "invalid argument") {
		t.Fatalf("GetByID empty id error = %v, want an invalid-argument error", err)
	}
}

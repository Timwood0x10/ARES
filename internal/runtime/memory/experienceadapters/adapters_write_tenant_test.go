package experienceadapters

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	experience "github.com/Timwood0x10/ares/internal/llmexp"
	"github.com/Timwood0x10/ares/internal/runtime/memory/distillation"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// H3 regression: the distillation write path must honor a runtime tenant
// override. The llmexp Experience DTO carries no TenantID field, so
// DistillationRepo stamps one. Before the fix it stamped its
// construction-time DefaultTenant unconditionally, which meant
// memoryManager.SetDefaultTenantID re-scoped READS while every write kept
// landing in the construction-time tenant — a cross-tenant leak the moment
// a second tenant was introduced.
//
// The fix threads the tenant through context (distillation.WithTenant) at
// each distiller write site; the adapter reads it back in preference to
// DefaultTenant.

// tenantRecordingRepo embeds the shared fake and captures the tenant each
// write actually received, following the countingExpRepo embedding pattern.
type tenantRecordingRepo struct {
	fakeExpRepo
	createdTenants []string
	deletedTenants []string
	updatedTenants []string
}

func (t *tenantRecordingRepo) Create(_ context.Context, exp *storage_models.Experience) error {
	t.createdTenants = append(t.createdTenants, exp.TenantID)
	return nil
}

func (t *tenantRecordingRepo) Update(_ context.Context, exp *storage_models.Experience) error {
	t.updatedTenants = append(t.updatedTenants, exp.TenantID)
	return nil
}

func (t *tenantRecordingRepo) Delete(_ context.Context, _, tenantID string) error {
	t.deletedTenants = append(t.deletedTenants, tenantID)
	return nil
}

func (t *tenantRecordingRepo) GetByID(context.Context, string, string) (*storage_models.Experience, error) {
	return nil, errors.New("not found")
}

// TestWriteTenantHonorsContextOverride pins the fix: a tenant injected via
// distillation.WithTenant must reach Create/Update/Delete instead of the
// adapter's construction-time DefaultTenant.
func TestWriteTenantHonorsContextOverride(t *testing.T) {
	repo := &tenantRecordingRepo{}
	r := NewDistillationRepo(repo, "tenant-construction")
	require.Equal(t, "tenant-construction", r.DefaultTenant)

	ctx := distillation.WithTenant(context.Background(), "tenant-runtime")
	exp := &experience.Experience{ID: "exp-1", Problem: "p", Solution: "s"}

	require.NoError(t, r.Create(ctx, exp))
	require.NoError(t, r.Update(ctx, exp))
	require.NoError(t, r.Delete(ctx, "exp-1"))

	assert.Equal(t, []string{"tenant-runtime"}, repo.createdTenants,
		"Create must stamp the context tenant, not the construction-time default")
	assert.Equal(t, []string{"tenant-runtime"}, repo.updatedTenants,
		"Update must stamp the context tenant, not the construction-time default")
	assert.Equal(t, []string{"tenant-runtime"}, repo.deletedTenants,
		"Delete must scope to the context tenant, not the construction-time default")
}

// TestWriteTenantFallsBackToDefault pins the other half of the contract: a
// context with no tenant keeps the pre-existing single-tenant behavior, so
// callers that never inject a tenant are unaffected.
func TestWriteTenantFallsBackToDefault(t *testing.T) {
	repo := &tenantRecordingRepo{}
	r := NewDistillationRepo(repo, "tenant-construction")

	exp := &experience.Experience{ID: "exp-2", Problem: "p", Solution: "s"}
	require.NoError(t, r.Create(context.Background(), exp))
	require.NoError(t, r.Delete(context.Background(), "exp-2"))

	assert.Equal(t, []string{"tenant-construction"}, repo.createdTenants,
		"Create without a context tenant must fall back to DefaultTenant")
	assert.Equal(t, []string{"tenant-construction"}, repo.deletedTenants,
		"Delete without a context tenant must fall back to DefaultTenant")
}

// TestWriteTenantEmptyContextValueIsIgnored pins that an explicitly empty
// tenant does not blank the row: WithTenant refuses to store "", and
// writeTenant treats the resulting absence as "use the default".
func TestWriteTenantEmptyContextValueIsIgnored(t *testing.T) {
	repo := &tenantRecordingRepo{}
	r := NewDistillationRepo(repo, "tenant-construction")

	ctx := distillation.WithTenant(context.Background(), "")
	exp := &experience.Experience{ID: "exp-3", Problem: "p", Solution: "s"}
	require.NoError(t, r.Create(ctx, exp))

	assert.Equal(t, []string{"tenant-construction"}, repo.createdTenants,
		"an empty context tenant must not blank the write tenant")
}

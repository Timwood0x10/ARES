package sub

import (
	"testing"

	"github.com/stretchr/testify/assert"

	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// TestDistillTenantIDFollowsTask pins the write-side half of the AKG tenant
// coherence: the tenant stamped onto distillation events is the executing
// task's own tenant (restored from its checkpoint envelope by the scheduler),
// falling back to the documented default for tenant-less tasks. Pre-fix this
// was hardcoded to the default, so tenant-attributed facts were distilled
// into the shared namespace.
func TestDistillTenantIDFollowsTask(t *testing.T) {
	// A task carrying a tenant scopes its own facts.
	carrying := models.NewTask("t1", "echo", nil)
	carrying.TenantID = "tenant-acme"
	assert.Equal(t, "tenant-acme", distillTenantID(carrying))

	// A tenant-less task (single-tenant deployment, pre-v5 envelope) keeps
	// the documented default — the value the read side's fallback expects.
	assert.Equal(t, ares_events.DefaultTenantID, distillTenantID(models.NewTask("t2", "echo", nil)))

	// Defensive: a nil task cannot panic the event emitter.
	assert.Equal(t, ares_events.DefaultTenantID, distillTenantID(nil))
}

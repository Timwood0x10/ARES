package taskfabric

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCheckpointEnvelopeTenantRoundTrip pins the v5 envelope contract: the
// tenant stamped at submission survives encode→decode and yield→resume
// re-wraps, and a pre-v5 envelope decodes with an empty tenant (forward
// compatibility — no migration needed).
func TestCheckpointEnvelopeTenantRoundTrip(t *testing.T) {
	env := NewCheckpointEnvelope(map[string]any{"input": "q"})
	env.TenantID = "tenant-acme"

	dc, err := DecodeCheckpoint(env)
	require.NoError(t, err)
	require.Equal(t, "tenant-acme", dc.TenantID)
	require.Equal(t, CurrentCheckpointSchemaVersion, dc.SchemaVersion)

	// The yield/done re-wrap path: the scheduler decodes the task's envelope
	// and re-encodes it around a quantum result — the tenant must ride that
	// cycle (it is how a multi-quantum task keeps its scope).
	dc.StepCheckpoint = map[string]any{"progress": 1}
	rewrapped, err := DecodeCheckpoint(EncodeCheckpoint(dc))
	require.NoError(t, err)
	require.Equal(t, "tenant-acme", rewrapped.TenantID)
}

// TestCheckpointEnvelopeV4DecodesWithEmptyTenant pins the v4→v5 forward
// compatibility: an envelope written by pre-v5 code (no TenantID field)
// decodes with an empty tenant, and consumers treat empty as "no tenant
// known" — never as another tenant's scope.
func TestCheckpointEnvelopeV4DecodesWithEmptyTenant(t *testing.T) {
	env := NewCheckpointEnvelope(map[string]any{"input": "q"})
	env.SchemaVersion = 4 // a pre-v5 writer

	dc, err := DecodeCheckpoint(env)
	require.NoError(t, err)
	require.Equal(t, 4, dc.SchemaVersion)
	require.Empty(t, dc.TenantID, "a v4 envelope must decode with an empty tenant")
}

package agentfabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTryBeginQuantum_GuardsTheDispatchClaim is the contract for the guarded
// claim the dispatch path now uses (the dispatch-window residual of P1-6): it
// must succeed exactly when the agent is a valid worker slot and fail otherwise,
// so a quantum is never started on an agent that left IDLE between candidate
// selection and dispatch — most importantly one that idle reclamation retired,
// whose execution body is already released.
//
// The table is exhaustive over the states an agent can be in when a dispatcher
// arrives, which is what makes "losing the claim" a case the caller must handle
// rather than a race nobody thought about.
func TestTryBeginQuantum_GuardsTheDispatchClaim(t *testing.T) {
	ctx := context.Background()
	f := NewFabric()
	for _, id := range []string{"idle", "busy", "retired", "paused"} {
		_, err := f.Spawn(ctx, SpawnSpec{Identity: id, Capabilities: []string{"coder"}})
		require.NoError(t, err)
	}

	agent := func(id string) *Agent {
		a, err := f.Get(id)
		require.NoError(t, err)
		return a
	}
	require.NoError(t, f.Retire(ctx, "retired"))
	require.NoError(t, f.Suspend(ctx, "paused"))
	agent("busy").SetBusy(true)

	tests := []struct {
		name  string
		id    string
		want  bool
		after string
	}{
		{name: "idle agent is claimed and marked busy", id: "idle", want: true},
		{name: "already busy agent is refused", id: "busy", want: false},
		{name: "retired agent is refused", id: "retired", want: false},
		{name: "suspended agent is refused", id: "paused", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := agent(tt.id)
			assert.Equal(t, tt.want, a.TryBeginQuantum())
			if tt.want {
				assert.True(t, a.IsBusy(), "a successful claim must mark the agent busy")
				// The claim is symmetric with the release the dispatch path
				// defers: once cleared, the same agent can be claimed again.
				a.SetBusy(false)
				assert.True(t, a.TryBeginQuantum(),
					"clearing the flag must make the agent claimable again")
				a.SetBusy(false)
			}
		})
	}
}

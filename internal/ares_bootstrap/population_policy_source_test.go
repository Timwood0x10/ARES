package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/aresrecovery"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
)

// TestPopulationPolicySource_AnonymousSpawnSpecsGetStableIdentities locks
// REVIEW 2.7#40: the kernel re-applies the population policy every minute,
// and the population adapter skips spawn specs whose identity already has a
// live agent. Anonymous specs (no identity) previously got a fresh
// fabric-assigned id on every apply — unbounded agent generation. The source
// must stamp deterministic identities so re-applying the same strategy is
// idempotent.
func TestPopulationPolicySource_AnonymousSpawnSpecsGetStableIdentities(t *testing.T) {
	store := &stubStrategyStore{
		active: &evolution.Strategy{
			ID: "strategy-7",
			Params: map[string]any{
				"population.spawn": []any{
					// Anonymous spec: two of them.
					map[string]any{"capabilities": []any{"rust"}},
					map[string]any{},
					// Named spec: identity must be preserved verbatim.
					map[string]any{"identity": "worker-1", "capabilities": []any{"go"}},
				},
				"population.retire": []any{"old-agent"},
			},
		},
	}
	src := NewPopulationPolicySource(store)
	require.NotNil(t, src)

	p1, err := src.ActivePopulationPolicy(context.Background())
	require.NoError(t, err)
	require.Len(t, p1.Spawn, 3)

	// Anonymous specs are stamped deterministically from (strategy ID, index).
	assert.Equal(t, "evo-pop-strategy-7-0", p1.Spawn[0].Identity)
	assert.Equal(t, "evo-pop-strategy-7-1", p1.Spawn[1].Identity)
	// Named specs keep their identity.
	assert.Equal(t, "worker-1", p1.Spawn[2].Identity)
	assert.Equal(t, []string{"old-agent"}, p1.Retire)

	// Re-applying the SAME strategy yields the SAME identities — the
	// idempotency the adapter's identity guard needs.
	p2, err := src.ActivePopulationPolicy(context.Background())
	require.NoError(t, err)
	for i := range p1.Spawn {
		assert.Equal(t, p1.Spawn[i].Identity, p2.Spawn[i].Identity,
			"spawn spec %d identity must be stable across applies", i)
	}

	// A different strategy intentionally produces different identities: it
	// is a new population decision.
	store.active = &evolution.Strategy{
		ID:     "strategy-8",
		Params: store.active.Params,
	}
	p3, err := src.ActivePopulationPolicy(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "evo-pop-strategy-8-0", p3.Spawn[0].Identity)
}

// TestPopulationPolicy_AdaptPopulationIdempotentAcrossApplies runs the
// adapter-level end-to-end check: applying the same policy twice through the
// real EvolutionAdapter spawns anonymous agents exactly once.
func TestPopulationPolicy_AdaptPopulationIdempotentAcrossApplies(t *testing.T) {
	store := &stubStrategyStore{
		active: &evolution.Strategy{
			ID: "strategy-7",
			Params: map[string]any{
				"population.spawn": []any{
					map[string]any{"capabilities": []any{"rust"}},
				},
			},
		},
	}
	agents := agentfabric.NewFabric()
	adapter := aresrecovery.NewPopulationAdapter(agents, NewPopulationPolicySource(store))
	ctx := context.Background()

	first, err := adapter.Apply(ctx)
	require.NoError(t, err)
	require.Len(t, first, 1, "first apply spawns the anonymous agent")

	second, err := adapter.Apply(ctx)
	require.NoError(t, err)
	assert.Empty(t, second, "re-applying the same policy must not spawn again (idempotent)")

	third, err := adapter.Apply(ctx)
	require.NoError(t, err)
	assert.Empty(t, third)
}

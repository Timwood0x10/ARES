package agentfabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingSink records every lifecycle event, so a test can assert what a retirement
// carried rather than only that it happened.
type capturingSink struct{ events []AgentEvent }

func (s *capturingSink) Emit(_ context.Context, ev AgentEvent) error {
	s.events = append(s.events, ev)
	return nil
}

// retiredPayload returns the payload of agentID's retirement event, if one was recorded.
func (s *capturingSink) retiredPayload(agentID string) (map[string]any, bool) {
	for _, ev := range s.events {
		if ev.Type == EventAgentRetired && ev.AgentID == agentID {
			return ev.Payload, true
		}
	}
	return nil, false
}

// TestRetire_ReportsTheSpawnsItLeavesLive is the A-factory4 / P2-7 visibility contract.
//
// Retirement is single-node by design (Agent.Parent is PROVENANCE, not a lifecycle
// relation), so the agents a retired agent spawned stay live and keep their resource
// claims. That is exactly how a spawn/retire loop accumulates agents nothing reclaims —
// and before this, the retirement event said an agent was gone while nothing said what
// it left behind, so the accumulation was silent.
func TestRetire_ReportsTheSpawnsItLeavesLive(t *testing.T) {
	sink := &capturingSink{}
	f := NewFabric().WithEventSink(sink)

	ctx := context.Background()
	parent, err := f.Spawn(ctx, SpawnSpec{Identity: "parent", Capabilities: []string{"coder"}})
	require.NoError(t, err)
	for _, id := range []string{"child-1", "child-2"} {
		_, err := f.Spawn(ctx, SpawnSpec{
			Identity: id, Capabilities: []string{"coder"}, ParentID: parent.Identity,
		})
		require.NoError(t, err)
	}

	require.NoError(t, f.Retire(ctx, parent.Identity))

	payload, ok := sink.retiredPayload(parent.Identity)
	require.True(t, ok, "the retirement must be recorded")
	orphans, ok := payload[PayloadKeyOrphanedSpawns].([]string)
	require.True(t, ok, "retiring a parent must name the agents it left live")
	assert.ElementsMatch(t, []string{"child-1", "child-2"}, orphans)

	// The report is NOT a cascade: if the children had been retired too, they would
	// have retirement events of their own.
	for _, id := range []string{"child-1", "child-2"} {
		_, retired := sink.retiredPayload(id)
		assert.False(t, retired, "child %s must still be live — retirement is single-node", id)
	}
}

// TestRetire_OmitsTheKeyWhenNothingIsOrphaned: an empty list on every retirement would
// be noise; the key is what says "this agent left something behind".
func TestRetire_OmitsTheKeyWhenNothingIsOrphaned(t *testing.T) {
	sink := &capturingSink{}
	f := NewFabric().WithEventSink(sink)
	ctx := context.Background()

	a, err := f.Spawn(ctx, SpawnSpec{Identity: "lonely", Capabilities: []string{"coder"}})
	require.NoError(t, err)
	require.NoError(t, f.Retire(ctx, a.Identity))

	payload, ok := sink.retiredPayload("lonely")
	require.True(t, ok)
	_, present := payload[PayloadKeyOrphanedSpawns]
	assert.False(t, present, "no orphans means no key, not an empty list")
}

// TestOrphanedSpawnsLocked_ExcludesRetiredAndDeeperDescendants pins the helper's edges:
// it reports DIRECT live children only — a grandchild belongs to its own parent, and an
// already-retired child is not something this retirement left behind.
func TestOrphanedSpawnsLocked_ExcludesRetiredAndDeeperDescendants(t *testing.T) {
	sink := &capturingSink{}
	f := NewFabric().WithEventSink(sink)
	ctx := context.Background()

	_, err := f.Spawn(ctx, SpawnSpec{Identity: "p", Capabilities: []string{"coder"}})
	require.NoError(t, err)
	_, err = f.Spawn(ctx, SpawnSpec{Identity: "c", Capabilities: []string{"coder"}, ParentID: "p"})
	require.NoError(t, err)
	_, err = f.Spawn(ctx, SpawnSpec{Identity: "g", Capabilities: []string{"coder"}, ParentID: "c"})
	require.NoError(t, err)

	f.mu.Lock()
	direct := f.orphanedSpawnsLocked("p")
	f.mu.Unlock()
	assert.Equal(t, []string{"c"}, direct, "a grandchild belongs to its own parent")

	// Retire the child first: it is no longer something a later retirement leaves live.
	require.NoError(t, f.Retire(ctx, "c"))
	f.mu.Lock()
	after := f.orphanedSpawnsLocked("p")
	f.mu.Unlock()
	assert.Empty(t, after, "an already-retired child is not an orphan")

	// A root agent has no parent to look for, so the lookup short-circuits.
	f.mu.Lock()
	rootOrphans := f.orphanedSpawnsLocked("")
	f.mu.Unlock()
	assert.Nil(t, rootOrphans)
}

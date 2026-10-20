package agentfabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spawnTree builds parent → child → grandchild, each with a distinct identity.
func spawnTree(t *testing.T, f *Fabric) {
	t.Helper()
	ctx := context.Background()
	for _, spec := range []SpawnSpec{
		{Identity: "root", Capabilities: []string{"coder"}},
		{Identity: "child", Capabilities: []string{"coder"}, ParentID: "root"},
		{Identity: "grandchild", Capabilities: []string{"coder"}, ParentID: "child"},
	} {
		_, err := f.Spawn(ctx, spec)
		require.NoError(t, err)
	}
}

// TestRetire_CascadesByDefault is P2-7's policy half after the 0.3.4 contract change:
// Agent.Parent is a LIFECYCLE relation, so retiring a parent retires its subtree.
//
// The old contract (provenance only) made a spawn/retire loop accumulate live agents that
// nothing owned — each retirement looked reasonable on its own while the pool grew. The
// cascade closes that by construction, and the events name the root so it is attributable
// rather than looking like three unrelated retirements.
func TestRetire_CascadesByDefault(t *testing.T) {
	ctx := context.Background()

	sink := &capturingSink{}
	f := NewFabric().WithEventSink(sink)
	spawnTree(t, f)

	require.NoError(t, f.Retire(ctx, "root"))

	for _, id := range []string{"root", "child", "grandchild"} {
		a, err := f.Get(id)
		require.NoError(t, err)
		assert.Equal(t, StateRetired, a.State, "%s must be retired by the cascade", id)
	}
	assert.Zero(t, f.AuditOrphans(5).Agents, "a cascade leaves nothing orphaned")

	// Attribution: the root's own event carries no cascade_root; the descendants name it.
	var rootEvents, descendantEvents int
	for _, ev := range sink.events {
		if ev.Type != EventAgentRetired {
			continue
		}
		root, hasRoot := ev.Payload[PayloadKeyCascadeRoot].(string)
		if ev.AgentID == "root" {
			rootEvents++
			assert.False(t, hasRoot, "the agent the caller retired was not cascaded into")
			continue
		}
		descendantEvents++
		assert.Equal(t, "root", root, "%s must name the root that took it down", ev.AgentID)
	}
	assert.Equal(t, 1, rootEvents)
	assert.Equal(t, 2, descendantEvents, "each retired descendant gets its own event")
}

// TestRetire_CascadeCanBeTurnedOff keeps the OLD behaviour reachable: a caller that manages
// its own subtree asks for single-node retirement explicitly.
//
// It also pins why that mode still exists alongside the orphan audit — a sparse tree is not
// the same fact as a corrupt one, and a caller that deliberately keeps children alive should
// not be told it has a leak.
func TestRetire_CascadeCanBeTurnedOff(t *testing.T) {
	ctx := context.Background()

	f := NewFabric().WithCascadeRetire(false)
	spawnTree(t, f)
	require.NoError(t, f.Retire(ctx, "root"))

	child, err := f.Get("child")
	require.NoError(t, err, "with the cascade off a child must survive its parent")
	assert.NotEqual(t, StateRetired, child.State)

	// ONE orphan, not two: `child` lost its parent, while `grandchild`'s parent is still
	// alive — an orphan is an agent whose own parent is gone, not any agent under a
	// retired root.
	assert.Equal(t, 1, f.AuditOrphans(5).Agents)
}

// TestDescendantsLocked_SurvivesACyclicIndex: `children` is append-only and spawn is its
// only writer, so a cycle cannot arise from the API — but this walk runs while HOLDING THE
// FABRIC LOCK, and a corrupt or reconstructed index would then hang every caller. The
// visited set is insurance against a failure mode that looks like a production deadlock.
func TestDescendantsLocked_SurvivesACyclicIndex(t *testing.T) {
	f := NewFabric()
	spawnTree(t, f)

	f.mu.Lock()
	f.children["grandchild"] = append(f.children["grandchild"], "root") // introduce a cycle
	descendants := f.descendantsLocked("root")
	f.mu.Unlock()

	assert.ElementsMatch(t, []string{"child", "grandchild"}, descendants)
}

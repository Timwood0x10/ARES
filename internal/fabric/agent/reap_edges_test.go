package agentfabric

// reap_edges_test.go covers the half of the reaper that is not the registry: the
// process-tree edges that Spawn writes into f.children.
//
// They were the last unbounded structure in this fabric. Every spawn carrying a
// ParentID appended to the parent's child list and nothing ever removed one — not
// Kill, not Retire, not the reaper — so a long-lived process accumulated a
// provenance graph whose nodes had stopped existing, and Children() (a public
// accessor) could hand out ids no lookup could resolve.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// spawnAgedFabric returns a fabric with a driven clock plus a helper that spawns
// and requires success, so each test below reads as the tree operations it is.
func spawnAgedFabric(t *testing.T) (*Fabric, *time.Time, func(SpawnSpec)) {
	t.Helper()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	at := now
	f := NewFabric().WithClock(func() time.Time { return at })
	spawn := func(spec SpawnSpec) {
		t.Helper()
		_, err := f.Spawn(context.Background(), spec)
		require.NoError(t, err, "spawn %s", spec.Identity)
	}
	return f, &at, spawn
}

// TestReapRetired_DropsTheReapedAgentsEdges pins the rule: when a record is
// reaped, the edges that named it go with it — both the lists it owned and its
// own id inside its parent's list.
func TestReapRetired_DropsTheReapedAgentsEdges(t *testing.T) {
	ctx := context.Background()
	f, at, spawn := spawnAgedFabric(t)

	// parent -> child -> grandchild, so the doomed id appears BOTH as a key (its
	// own list) and as an element (its parent's list).
	spawn(SpawnSpec{Identity: "parent"})
	spawn(SpawnSpec{Identity: "child", ParentID: "parent"})
	spawn(SpawnSpec{Identity: "grandchild", ParentID: "child"})
	require.Equal(t, []string{"child"}, f.Children("parent"))

	// Reap only the child: it is retired and aged, its parent and its own child
	// are not.
	require.NoError(t, f.Retire(ctx, "child"))
	*at = at.Add(2 * time.Hour)

	require.Equal(t, []string{"child"}, f.ReapRetired(ctx, DefaultRetiredAgentRetention))

	// Its edge in the parent's list is gone...
	require.Empty(t, f.Children("parent"),
		"the edge names a reaped agent, so it must not be handed out")
	// ...and so is the list it owned (the grandchild keeps its own record).
	require.Empty(t, f.Children("child"), "a reaped agent's own edges are gone")
	_, err := f.Get("grandchild")
	require.NoError(t, err, "a reaped parent's child must still exist (parent death ≠ child death)")

	// The emptied parent entry must be DELETED, not left behind as an empty slice:
	// the map is the structure that grew, so an empty entry is still growth.
	f.mu.Lock()
	_, parentEntry := f.children["parent"]
	_, childEntry := f.children["child"]
	f.mu.Unlock()
	require.False(t, parentEntry, "an emptied child list must be deleted")
	require.False(t, childEntry, "a reaped agent must own no list")
}

// TestReapRetired_LeavesLiveEdgesAlone is the other side of the same rule: the
// reaper must not touch a tree whose members are still live, or a process tree
// would lose its shape the moment anything was retired nearby.
func TestReapRetired_LeavesLiveEdgesAlone(t *testing.T) {
	ctx := context.Background()
	f, at, spawn := spawnAgedFabric(t)

	spawn(SpawnSpec{Identity: "root"})
	spawn(SpawnSpec{Identity: "k1", ParentID: "root"})
	spawn(SpawnSpec{Identity: "k2", ParentID: "root"})
	// An unrelated retiree, so the sweep has something to reap.
	spawn(SpawnSpec{Identity: "unrelated"})
	require.NoError(t, f.Retire(ctx, "unrelated"))

	*at = at.Add(2 * time.Hour)
	require.Equal(t, []string{"unrelated"}, f.ReapRetired(ctx, DefaultRetiredAgentRetention))

	require.Len(t, f.Children("root"), 2, "both live children must keep their edge")
}

// TestReapRetired_BoundsTheProvenanceGraph states the property the fix is for: the
// edge map shrinks back to the agents the registry still holds, however many
// spawn/retire cycles a process runs.
func TestReapRetired_BoundsTheProvenanceGraph(t *testing.T) {
	ctx := context.Background()
	f, at, spawn := spawnAgedFabric(t)

	spawn(SpawnSpec{Identity: "root"})
	for _, id := range []string{"a", "b", "c"} {
		spawn(SpawnSpec{Identity: id, ParentID: "root"})
	}
	require.Len(t, f.Children("root"), 3)

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, f.Retire(ctx, id))
	}
	*at = at.Add(2 * time.Hour)
	require.Len(t, f.ReapRetired(ctx, DefaultRetiredAgentRetention), 3)

	f.mu.Lock()
	edges := len(f.children)
	f.mu.Unlock()
	require.Zero(t, edges,
		"every reaped child's entry must be gone: the map is bounded by the live tree")
}

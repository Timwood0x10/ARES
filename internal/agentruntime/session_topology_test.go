package agentruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestSessionStalled_DoesNotFireWhileAProjectionIsPending is RT-02, and it is the failure that loses
// an answer rather than merely slowing it: the graph grows a node, the projection has not compiled it
// yet, so the fabric holds no unfinished task — and the waiter concludes the session can never answer.
// The answer node is on its way the whole time.
func TestSessionStalled_DoesNotFireWhileAProjectionIsPending(t *testing.T) {
	ctx := context.Background()
	s, fabric := newTestSessions(t)

	require.NoError(t, s.Admit(ctx, "s-topo", "prompt"))
	g, err := s.Reg.GetSession("s-topo")
	require.NoError(t, err)

	// Every task the fabric knows about is finished…
	completeTask(t, fabric, g.Root())

	// …and the graph holds a node the fabric has never seen (the projection is "still working").
	nodeID := "sess/s-topo/projected-later"
	require.NoError(t, g.AddToolNode(ctx, nodeID, "grep", map[string]any{"query": "x"}, g.Root()))

	// Without topology the legacy verdict fires: this is the defect, stated rather than assumed.
	assert.True(t, SessionStalled(fabric, "s-topo", ""),
		"precondition: the fabric-only verdict says stalled, which is what made this a bug")

	topo := s.PendingProjections("s-topo")
	require.Equal(t, 1, topo, "one graph node has no compiled task")

	assert.False(t, SessionStalledWith(fabric, "s-topo", "", s),
		"a pending projection is work in progress, not a stall")

	// Once the node is projected (a task exists) and finishes, the verdict returns — the check is not a
	// blanket "never stalled".
	ensureTask(t, fabric, nodeID)
	completeTask(t, fabric, nodeID)
	assert.True(t, SessionStalledWith(fabric, "s-topo", "", s),
		"with every node compiled and settled, the session really is done")
}

// TestStallDetector_AccumulatesOnlyRealStalls keeps the confirmation counter honest: a pending
// projection must reset it, or a flapping projection would add up to a verdict across polls.
func TestStallDetector_AccumulatesOnlyRealStalls(t *testing.T) {
	ctx := context.Background()
	s, fabric := newTestSessions(t)
	require.NoError(t, s.Admit(ctx, "s-topo2", "prompt"))
	g, err := s.Reg.GetSession("s-topo2")
	require.NoError(t, err)
	completeTask(t, fabric, g.Root())

	nodeID := "sess/s-topo2/later"
	require.NoError(t, g.AddToolNode(ctx, nodeID, "grep", map[string]any{"query": "x"}, g.Root()))

	det := (&StallDetector{}).WithTopology(s)
	for i := 0; i < StallConfirmations+2; i++ {
		require.False(t, det.Stalled(fabric, "s-topo2", ""),
			"poll %d: a pending projection must not accumulate stall evidence", i+1)
		time.Sleep(time.Millisecond)
	}

	ensureTask(t, fabric, nodeID)
	completeTask(t, fabric, nodeID)
	var fired bool
	for i := 0; i < StallConfirmations; i++ {
		if det.Stalled(fabric, "s-topo2", "") {
			fired = true
			break
		}
	}
	assert.True(t, fired, "once the projection lands, the verdict must be reachable again")
}

// ensureTask creates the task for a graph node unless something already projected it. The fixture's
// projection is not what this test is about: some paths compile a node on admission, and asserting
// "it was not created yet" here would be testing the fixture.
func ensureTask(t *testing.T, fabric *taskfabric.Fabric, id string) {
	t.Helper()
	if _, err := fabric.Task(id); err == nil {
		return
	}
	if err := fabric.Create(&taskfabric.Task{ID: id, Capability: "tool/grep"}); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create %s: %v", id, err)
	}
}

package agentruntime

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// submitFixture builds a Submitter over a fresh fabric + registry, mirroring
// the Execution assembly (no chat client/binder needed for submission).
func submitFixture() (*Submitter, *taskfabric.Fabric, *agentfabric.SessionRegistry) {
	fabric := taskfabric.NewFabric()
	reg := agentfabric.NewSessionRegistry()
	sessions := &Sessions{Reg: reg, Fabric: fabric, Compile: planprojection.NewCompileCoordinator(fabric, nil)}
	return NewSubmitter(sessions), fabric, reg
}

// TestMaxRestoredSeq locks the extraction contract for every
// counter-derived task-ID family the seeder must dominate after a durable
// restore (see MaxRestoredSeq for the family list).
func TestMaxRestoredSeq(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		want int64
	}{
		{"empty fabric", nil, 0},
		{"no counter derived ids", []string{"uuid-550e8400e29b", "step:root"}, 0},
		{"peer plan submissions", []string{"peer-plan-2", "peer-plan-7", "peer-plan-1"}, 7},
		{"session node tasks", []string{"sess/sess-auto-3/d0/ares#1", "sess/sess-auto-11/d1/tool#2"}, 11},
		{"syscall task ids", []string{"task-ares/plan-1", "task-tool/web-search-4"}, 4},
		{"plan loop round tasks", []string{"plan-agent-A-5/r1#step"}, 5},
		{"mixed families take the max", []string{"peer-plan-2", "sess/sess-auto-3/d0/ares#1", "task-ares/plan-9"}, 9},
		{"non numeric tail ignored", []string{"peer-plan-abc", "peer-plan-"}, 0},
		{"zero tail ignored", []string{"peer-plan-0"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, MaxRestoredSeq(tc.ids))
		})
	}
}

// TestSeedPastRestoredIDs reproduces the restart collision the seeder exists
// to prevent: a fresh process mints peer-plan-N from a reset counter while
// the restored fabric still holds the previous boot's peer-plan-N. Without
// the seed the next submission fails Create with ErrTaskExists; with it the
// minted ID is strictly greater than every restored counter value.
func TestSeedPastRestoredIDs(t *testing.T) {
	sub, fabric, _ := submitFixture()
	restored := []string{
		"peer-plan-2",
		"peer-plan-4",
		"sess/sess-auto-3/d0/ares#1",
		"task-ares/plan-9",
	}
	for _, id := range restored {
		require.NoError(t, fabric.Create(&taskfabric.Task{ID: id, Capability: PlanCapability}))
	}

	sub.Seed(MaxRestoredSeq(fabric.IDs()))
	require.GreaterOrEqual(t, sub.seq.Load(), int64(9),
		"sequence must dominate the max restored counter value")

	taskID, _, err := sub.Submit(context.Background(), PlanCapability, map[string]any{"input": "x"})
	require.NoError(t, err, "next minted peer-plan ID must not collide with restored tasks")
	require.NotContains(t, restored, taskID)
}

// TestSeedGrowOnly locks the grow-only contract: a restore whose max N is
// below the live sequence (fresh store, or a busier process) must never move
// the counter backwards.
func TestSeedGrowOnly(t *testing.T) {
	sub, _, _ := submitFixture()
	sub.Seed(100)
	sub.Seed(90)
	require.Equal(t, int64(100), sub.seq.Load(), "seed below the current value must be a no-op")
}

// TestSubmitAdmitsSessionFirst pins the submission contract: a submission
// registers the session and compiles its root BEFORE the user task is
// created, so the planner's first quantum finds a live graph. The returned
// session ID equals the payload's session_id.
func TestSubmitAdmitsSessionFirst(t *testing.T) {
	ctx := context.Background()
	sub, fabric, reg := submitFixture()

	taskID, sessionID, err := sub.Submit(ctx, PlanCapability, map[string]any{
		"session_id": "sub-1",
		"input":      "find the answer",
	})
	require.NoError(t, err)
	require.Equal(t, "sub-1", sessionID)

	got, err := reg.GetSession("sub-1")
	require.NoError(t, err, "session must exist after submission")
	require.Equal(t, agentfabric.SessionRootID("sub-1"), got.Root())
	_, err = fabric.Task(got.Root())
	require.NoError(t, err, "root must be compiled into the fabric")

	tk, err := fabric.Task(taskID)
	require.NoError(t, err)
	require.Equal(t, PlanCapability, tk.Capability)
	require.Equal(t, taskfabric.StateReady, tk.State)
	require.Equal(t, "sub-1", tk.Checkpoint.(*taskfabric.CheckpointEnvelope).SessionID)
}

// TestSubmitAutoAdmitsSession pins session-less normalization: the submission
// auto-admits into a minted sess-auto-N session (stamped on the created
// task's envelope; the caller's payload map stays untouched — copy-on-submit)
// and the capability is normalized to PlanCapability.
func TestSubmitAutoAdmitsSession(t *testing.T) {
	ctx := context.Background()
	sub, _, reg := submitFixture()

	payload := map[string]any{"input": "hi"}
	taskID, sessionID, err := sub.Submit(ctx, "worker", payload)
	require.NoError(t, err)
	require.NotEmpty(t, sessionID)
	require.NotContains(t, payload, "session_id", "caller payload must not be mutated")
	_, err = reg.GetSession(sessionID)
	require.NoError(t, err)
	tk, err := sub.sessions.Fabric.Task(taskID)
	require.NoError(t, err)
	require.Equal(t, PlanCapability, tk.Capability, "capability must normalize to the single L2 path")
	require.Equal(t, sessionID, tk.Checkpoint.(*taskfabric.CheckpointEnvelope).SessionID,
		"created task envelope must carry the auto session id")
}

// TestSubmitMintsUniqueIDs pins the sequence contract: consecutive
// submissions never collide within one runtime.
func TestSubmitMintsUniqueIDs(t *testing.T) {
	ctx := context.Background()
	sub, _, _ := submitFixture()
	seen := make(map[string]bool)
	for i := 0; i < 5; i++ {
		taskID, _, err := sub.Submit(ctx, PlanCapability, map[string]any{"input": fmt.Sprintf("j%d", i)})
		require.NoError(t, err)
		require.False(t, seen[taskID], "duplicate task ID %s", taskID)
		seen[taskID] = true
	}
}

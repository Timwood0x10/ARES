package agentruntime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// newTestSessions wires a Sessions over an in-memory fabric with a nil-store
// compile coordinator — the same shape NewExecution builds, minus the planner
// cognition (these tests pin the session-lifecycle contracts, not planning).
func newTestSessions(t *testing.T) (*Sessions, *taskfabric.Fabric) {
	t.Helper()
	fabric := taskfabric.NewFabric()
	return &Sessions{
		Reg:     agentfabric.NewSessionRegistry(),
		Fabric:  fabric,
		Compile: planprojection.NewCompileCoordinator(fabric, nil),
	}, fabric
}

// completeTask drives a task to COMPLETED through the real lease protocol so
// terminal-state branches are exercised against genuine fabric semantics.
func completeTask(t *testing.T, fabric *taskfabric.Fabric, taskID string) {
	t.Helper()
	epoch, err := fabric.Acquire(taskID, "test-agent", time.Minute)
	require.NoError(t, err)
	require.NoError(t, fabric.Start(taskID, "test-agent", epoch))
	require.NoError(t, fabric.Complete(taskID, "test-agent", epoch))
}

// TestSessionsAdmit_Idempotent pins the admission idempotency contract: a
// resubmit into a live session is a continuation, not an error, and must not
// compile a duplicate root.
func TestSessionsAdmit_Idempotent(t *testing.T) {
	ctx := context.Background()
	s, fabric := newTestSessions(t)

	require.NoError(t, s.Admit(ctx, "s1", "first prompt"))
	g, err := s.Reg.GetSession("s1")
	require.NoError(t, err)
	rootID := g.Root()
	_, err = fabric.Task(rootID)
	require.NoError(t, err, "root must be compiled after first admission")

	require.NoError(t, s.Admit(ctx, "s1", "second prompt"), "resubmit into a live session must not fail")

	ids := fabric.IDs()
	require.Len(t, ids, 1, "continuation must not compile a duplicate root")
	require.Equal(t, rootID, ids[0])
}

// TestSessionsAdmit_RejectsSlashInID pins the boundary guard: a session ID
// containing "/" would break SessionIDFromNode's first-slash reverse parse and
// let the reaper resolve a live session's tasks as harvestable — admission
// must fail instead.
func TestSessionsAdmit_RejectsSlashInID(t *testing.T) {
	s, _ := newTestSessions(t)

	err := s.Admit(context.Background(), "a/b", "prompt")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not contain a slash")
}

// TestSessionsAdmit_ReusesLiveRoot pins the re-admission branch for a READY
// (in-flight) root left by a previously released session: the existing root is
// adopted as-is — not deleted, not recompiled.
func TestSessionsAdmit_ReusesLiveRoot(t *testing.T) {
	ctx := context.Background()
	s, fabric := newTestSessions(t)

	require.NoError(t, s.Admit(ctx, "s1", "prompt"))
	g, err := s.Reg.GetSession("s1")
	require.NoError(t, err)
	rootID := g.Root()

	require.NoError(t, s.Release("s1"))
	require.NoError(t, s.Admit(ctx, "s1", "next chat"), "re-admission after release must succeed")

	require.Len(t, fabric.IDs(), 1, "a live (READY) root must be adopted, not duplicated")
	_, err = fabric.Task(rootID)
	require.NoError(t, err)
}

// TestSessionsAdmit_HarvestsStaleTerminalRoot pins the stale-root branch: a
// COMPLETED root from a previous session under the same ID must be harvested
// before the new root is compiled, so a same-named stale node can never resolve
// as the new turn's result.
func TestSessionsAdmit_HarvestsStaleTerminalRoot(t *testing.T) {
	ctx := context.Background()
	s, fabric := newTestSessions(t)

	require.NoError(t, s.Admit(ctx, "s1", "old chat"))
	g, err := s.Reg.GetSession("s1")
	require.NoError(t, err)
	staleRoot := g.Root()
	completeTask(t, fabric, staleRoot)
	require.NoError(t, s.Release("s1"))

	require.NoError(t, s.Admit(ctx, "s1", "new chat"))

	g2, err := s.Reg.GetSession("s1")
	require.NoError(t, err)
	fresh, err := fabric.Task(g2.Root())
	require.NoError(t, err)
	require.NotEqual(t, taskfabric.StateCompleted, fresh.State, "stale terminal root must not leak into the new turn")
	require.Len(t, fabric.IDs(), 1, "stale root must be harvested, leaving exactly the fresh root")
}

// TestHarvest pins the harvest boundary: READY and terminal tasks of a
// released session are removed, while in-flight (LEASED) tasks are refused by
// the fabric and left for the reaper.
func TestHarvest(t *testing.T) {
	ctx := context.Background()
	s, fabric := newTestSessions(t)

	// s1: root left READY (not leased) — harvestable.
	require.NoError(t, s.Admit(ctx, "s1", "prompt"))
	// s2: root driven LEASED — in-flight, must survive.
	require.NoError(t, s.Admit(ctx, "s2", "prompt"))
	g2, err := s.Reg.GetSession("s2")
	require.NoError(t, err)
	_, err = fabric.Acquire(g2.Root(), "test-agent", time.Minute)
	require.NoError(t, err)

	require.Equal(t, 1, Harvest(fabric, "s1"), "READY root of a released session is harvestable")
	require.NoError(t, s.Release("s1"))
	require.Equal(t, 0, Harvest(fabric, "s2"), "LEASED root is in-flight and must be refused")
	require.Len(t, fabric.IDs(), 1, "only the in-flight task remains")
}

// TestKeepSet pins the reaper keep predicate: tasks of a LIVE session are
// kept; a session-prefixed task whose session is gone (or any non-session ID)
// is not kept and becomes harvestable once the grace window passes.
func TestKeepSet(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestSessions(t)
	keep := KeepSet(s.Reg)

	require.NoError(t, s.Admit(ctx, "s1", "prompt"))
	g, err := s.Reg.GetSession("s1")
	require.NoError(t, err)

	require.True(t, keep(g.Root()), "live session's task must be kept")
	require.False(t, keep("sess/gone/whatever"), "released session's tasks must not be kept")
	require.False(t, keep("unrelated/task"), "non-session IDs are not kept")
}

// TestReleaseOnAnswerFailure pins the release trigger: only a FAILED terminal
// answer event releases the session — a requeued answer (READY) or a non-answer
// capability must leave the session live.
func TestReleaseOnAnswerFailure(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name         string
		capability   string
		state        taskfabric.TaskState
		wantReleased bool
	}{
		{"failed_answer_releases", agentfabric.AnswerCapability, taskfabric.StateFailed, true},
		{"requeued_answer_keeps_session", agentfabric.AnswerCapability, taskfabric.StateReady, false},
		{"failed_non_answer_keeps_session", agentfabric.PlanCapability, taskfabric.StateFailed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestSessions(t)
			require.NoError(t, s.Admit(ctx, "s1", "prompt"))

			ReleaseOnAnswerFailure(ctx, s.Reg, &ares_events.Event{
				StreamID: "task-1",
				Payload: map[string]any{
					"capability": tc.capability,
					"state":      string(tc.state),
					"session_id": "s1",
				},
			})

			_, err := s.Reg.GetSession("s1")
			if tc.wantReleased {
				require.ErrorIs(t, err, agentfabric.ErrSessionNotFound, "failed terminal answer must release the session")
			} else {
				require.NoError(t, err, "session must stay live")
			}
		})
	}
}

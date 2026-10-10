package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

// staleTerminalAnswer reports whether any harvestable terminal task of the
// session is still in the fabric — exactly what a wait loop's answer scan
// (l2SessionAnswer: sess/<sid>/…/answer#… in a terminal state) would pick up
// as a result. A caller returning from Admit must never be able to observe
// one: the previous turn's tasks are harvested at the admission boundary.
func staleTerminalAnswer(fabric *taskfabric.Fabric, sessionID string) bool {
	prefix := "sess/" + sessionID + "/"
	for _, id := range fabric.IDs() {
		if !strings.HasPrefix(id, prefix) || !strings.Contains(id, "/answer#") {
			continue
		}
		if tk, err := fabric.Task(id); err == nil {
			if tk.State == taskfabric.StateCompleted || tk.State == taskfabric.StateFailed {
				return true
			}
		}
	}
	return false
}

// TestAdmitConcurrentReAdmissionSeesNoStaleAnswer pins the concurrent
// re-admission contract (P1): two Submits racing into the same RELEASED
// session must not let the loser return while the previous turn's terminal
// tasks are still in the fabric. Pre-fix, the loser's InitSession lost with
// ErrSessionAlreadyExists, re-checked the (now-registered) session and
// returned nil — skipping the winner's not-yet-run harvest. Its wait loop
// then scanned the stale COMPLETED answer#0 (reaper grace 30s) and returned
// the PREVIOUS turn's answer as this turn's result.
//
// Each goroutine observes the fabric at its OWN Admit-return moment (not
// after all of them finished — the winner's harvest would mask the loser's
// early return), and the scenario is repeated to dominate the race window.
func TestAdmitConcurrentReAdmissionSeesNoStaleAnswer(t *testing.T) {
	ctx := context.Background()
	const rounds, admitters = 25, 8
	for round := 0; round < rounds; round++ {
		s, fabric := newTestSessions(t)
		sessionID := "s1"

		// Previous turn: admitted, planned, answered, released. The terminal
		// tasks stay in the fabric until the reaper's grace window passes —
		// that is the state a client's "continue the chat" re-submit hits.
		require.NoError(t, s.Admit(ctx, sessionID, "old chat"))
		g, err := s.Reg.GetSession(sessionID)
		require.NoError(t, err)
		completeTask(t, fabric, g.Root())
		staleAnswer := "sess/" + sessionID + "/d1/answer#0"
		require.NoError(t, fabric.Create(&taskfabric.Task{ID: staleAnswer, Capability: agentfabric.AnswerCapability}))
		completeTask(t, fabric, staleAnswer)
		require.True(t, staleTerminalAnswer(fabric, sessionID), "fixture must leave a stale terminal answer")
		require.NoError(t, s.Release(sessionID))

		var (
			wg           sync.WaitGroup
			mu           sync.Mutex
			staleWitness []string
		)
		for i := 0; i < admitters; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				if err := s.Admit(ctx, sessionID, fmt.Sprintf("turn %d", n)); err != nil {
					mu.Lock()
					staleWitness = append(staleWitness, "admit error: "+err.Error())
					mu.Unlock()
					return
				}
				// Read the fabric the moment THIS caller may start waiting
				// for an answer — the exact window the defect exploited.
				if staleTerminalAnswer(fabric, sessionID) {
					mu.Lock()
					staleWitness = append(staleWitness, fmt.Sprintf("caller %d saw stale terminal tasks", n))
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()
		require.Empty(t, staleWitness,
			"round %d: Admit returned to a caller while the previous turn's terminal tasks were still visible: %v",
			round, staleWitness)

		// And the graph converges to exactly one live root.
		g2, err := s.Reg.GetSession(sessionID)
		require.NoError(t, err)
		require.Len(t, fabric.IDs(), 1, "round %d: concurrent re-admission must converge to a single fresh root", round)
		root, err := fabric.Task(g2.Root())
		require.NoError(t, err)
		require.NotEqual(t, taskfabric.StateCompleted, root.State)
	}
}

// TestSessionStalled pins the wait-loop fast-fail predicate (P1): a session
// whose every task is terminal and which never completed an answer can never
// answer — grown nodes retry zero times, so one failed tool cascades into the
// continuation plan node and the answer is never grown. Without this check
// the SDK/serve wait loops spun their full deadline on the dead session.
func TestSessionStalled(t *testing.T) {
	ctx := context.Background()
	s, fabric := newTestSessions(t)

	// Live session with a pending root → not stalled.
	require.NoError(t, s.Admit(ctx, "s1", "prompt"))
	require.False(t, SessionStalled(fabric, "s1", ""), "a live (non-terminal) session is not stalled")

	g, err := s.Reg.GetSession("s1")
	require.NoError(t, err)
	root := g.Root()

	// Root completed, a tool node failed terminally, its dependent plan node
	// cascade-failed: everything terminal, no answer → stalled.
	completeTask(t, fabric, root)
	require.NoError(t, fabric.Create(&taskfabric.Task{
		ID: "sess/s1/d1/tool#0", Capability: "tool/x",
	}))
	epoch, err := fabric.Acquire("sess/s1/d1/tool#0", "test-agent", time.Minute)
	require.NoError(t, err)
	require.NoError(t, fabric.Start("sess/s1/d1/tool#0", "test-agent", epoch))
	require.NoError(t, fabric.Fail("sess/s1/d1/tool#0", "test-agent", epoch, nil))
	require.True(t, SessionStalled(fabric, "s1", ""),
		"all tasks terminal with no answer must read as stalled")

	// A LIVE submission root keeps the session unstalled: its plan quantum
	// is what grows the session's nodes, and a completed session root alone
	// says nothing (pre-fix this false-positived a blocked-LLM session into
	// an immediate "stalled" error).
	require.NoError(t, fabric.Create(&taskfabric.Task{ID: "peer-plan-1", Capability: "ares/plan"}))
	require.False(t, SessionStalled(fabric, "s1", "peer-plan-1"),
		"a live plan task means the session may still grow nodes")

	// A completed plan task restores the stall verdict.
	completeTask(t, fabric, "peer-plan-1")
	require.True(t, SessionStalled(fabric, "s1", "peer-plan-1"))

	// A completed answer does NOT clear the stall verdict — by contract the
	// caller runs its answer scan FIRST and returns the content; the stall
	// check only fires when that scan missed (e.g. an empty-content answer,
	// where spinning to the deadline would be strictly worse).
	require.NoError(t, fabric.Create(&taskfabric.Task{
		ID: "sess/s1/d1/answer#0", Capability: agentfabric.AnswerCapability,
	}))
	completeTask(t, fabric, "sess/s1/d1/answer#0")
	require.True(t, SessionStalled(fabric, "s1", "peer-plan-1"),
		"all-terminal stays stalled; the answer scan is the caller's first check")

	// Unknown / empty sessions never stall (nothing to wait for either —
	// the answer scan's miss plus the timeout bound handle that path).
	require.False(t, SessionStalled(fabric, "nope", ""))
	require.False(t, SessionStalled(fabric, "", ""))
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

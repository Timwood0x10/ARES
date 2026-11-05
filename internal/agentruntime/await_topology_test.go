package agentruntime

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// Register without a compiler subscription: projection is released explicitly by the test.
// Admit would start a background subscriber, making a missing task an uncontrolled transient.
func pendingAwaitSession(t *testing.T, sessionID string) (*Sessions, *agentfabric.L2Graph, string) {
	t.Helper()
	sessions, fabric := newTestSessions(t)
	graph, err := sessions.Reg.InitSession(
		sessionID,
		"delayed answer",
		nil,
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { sessions.ReleaseQuietly(sessionID) })
	_, err = fabric.CompileNode(t.Context(), planprojection.ProjectStep(graph.DAG().StepIndex()[graph.Root()]))
	require.NoError(t, err)
	completeTask(t, fabric, graph.Root())
	answerID := agentfabric.SessionNodeID(sessionID, 1, "answer", 0)
	require.NoError(t, graph.AddToolNode(
		t.Context(),
		answerID,
		"answer",
		map[string]any{"content": "the delayed answer"},
		graph.Root(),
	))
	require.Equal(t, 1, sessions.PendingProjections(sessionID))
	return sessions, graph, answerID
}

func projectAwaitAnswer(
	t *testing.T,
	sessions *Sessions,
	graph *agentfabric.L2Graph,
	answerID string,
) {
	t.Helper()
	fabric := sessions.Fabric
	_, err := fabric.CompileNode(t.Context(), planprojection.ProjectStep(graph.DAG().StepIndex()[answerID]))
	require.NoError(t, err)
	epoch, err := fabric.Acquire(answerID, "test-agent", time.Minute)
	require.NoError(t, err)
	require.NoError(t, fabric.Start(answerID, "test-agent", epoch))
	require.NoError(t, fabric.CompleteWithCheckpoint(
		answerID,
		"test-agent",
		epoch,
		taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
			StepCheckpoint: map[string]any{
				"items": []*models.RecommendItem{{Content: "the delayed answer"}},
			},
		}),
	))
}

func TestAwaitSessionResult_PendingProjection(t *testing.T) {
	for _, name := range []string{"submission-plan", "no-plan-id"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const sessionID = "s-await"
				sessions, graph, answerID := pendingAwaitSession(t, sessionID)
				fabric := sessions.Fabric
				planID := ""
				if name == "submission-plan" {
					var err error
					planID, _, err = NewSubmitter(sessions).Submit(t.Context(), PlanCapability, map[string]any{
						"session_id": sessionID, "input": "delayed answer",
					})
					require.NoError(t, err)
					// The submission plan is separate from the already-completed session root.
					completeTask(t, fabric, planID)
				}
				require.True(t, SessionStalled(fabric, sessionID, planID), "fixture must expose the old verdict")
				_, answered := SessionAnswer(fabric, sessionID)
				require.False(t, answered)
				require.False(t, SessionAnswerFailed(fabric, sessionID))

				opts := AwaitOptions{PollInterval: time.Millisecond, Wait: time.Second}
				// Exercise the SAME public wait with the SAME graph/fabric, changing only topology.
				// This must return a stall, not a deadline; otherwise the fixture proves nothing.
				legacy := AwaitSessionResult(
					context.Background(),
					fabric,
					sessionID,
					planID,
					opts,
				)
				require.ErrorIs(t, legacy.Err, ErrSessionStalled)
				require.True(t, legacy.Stalled)
				opts.Topology = sessions
				done := make(chan AwaitResult, 1)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go func() {
					done <- AwaitSessionResult(
						ctx,
						fabric,
						sessionID,
						planID,
						opts,
					)
				}()
				synctest.Wait()
				for range StallConfirmations + 2 {
					time.Sleep(opts.PollInterval)
					synctest.Wait()
				}
				select {
				case got := <-done:
					t.Fatalf("wait returned before projection was released: %+v", got)
				default:
				}
				projectAwaitAnswer(
					t,
					sessions,
					graph,
					answerID,
				)
				require.Zero(t, sessions.PendingProjections(sessionID))
				got := <-done
				require.NoError(t, got.Err)
				require.Equal(t, "the delayed answer", got.Answer)
			})
		})
	}
}

func TestAwaitSessionResult_PendingProjectionHonorsWaitBoundary(t *testing.T) {
	for _, name := range []string{"wait-budget", "caller-deadline", "caller-cancel"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const sessionID = "s-await-boundary"
				sessions, _, _ := pendingAwaitSession(t, sessionID)
				require.True(t, SessionStalled(sessions.Fabric, sessionID, ""))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				opts := AwaitOptions{PollInterval: time.Millisecond, Wait: 20 * time.Millisecond, Topology: sessions}
				if name == "caller-deadline" {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, 30*time.Millisecond)
					defer stop()
				}
				done := make(chan AwaitResult, 1)
				started := time.Now()
				go func() {
					done <- AwaitSessionResult(
						ctx,
						sessions.Fabric,
						sessionID,
						"",
						opts,
					)
				}()
				synctest.Wait()
				for range StallConfirmations + 2 {
					time.Sleep(opts.PollInterval)
					synctest.Wait()
				}
				select {
				case got := <-done:
					t.Fatalf("wait returned before its boundary: %+v", got)
				default:
				}
				want := context.DeadlineExceeded
				if name == "caller-cancel" {
					want = context.Canceled
					cancel()
				}
				got := <-done
				require.ErrorIs(t, got.Err, want)
				require.False(t, got.Stalled)
				require.Equal(t, 1, sessions.PendingProjections(sessionID))
				switch name {
				case "wait-budget":
					// Exact equality is only defensible because this whole test runs inside
					// synctest.Test: the clock is fake, so elapsed time is the budget itself rather
					// than "at least the budget". Outside a fake clock this would be a flaky
					// assertion, and moving it out is how it becomes one.
					require.Equal(t, opts.Wait, time.Since(started))
				case "caller-deadline":
					require.Equal(
						t,
						30*time.Millisecond,
						time.Since(started),
						"an existing caller deadline takes precedence over opts.Wait",
					)
				}
			})
		})
	}
}

// A missing/live submission plan or an empty session prevents a stall even without topology.
// Pin these prerequisites so an invalid fixture cannot turn a deadline into evidence for RT-02.
func TestAwaitSessionResult_StallPrerequisites(t *testing.T) {
	for _, tc := range []struct {
		name        string
		planID      string
		planState   string
		createPlan  bool
		createRoot  bool
		rootState   string
		wantStalled bool
	}{
		{name: "missing-plan-prevents-stall", planID: "peer-plan", createRoot: true, rootState: "completed"},
		{name: "live-plan-prevents-stall", planID: "peer-plan", createPlan: true, createRoot: true, rootState: "completed"},
		{name: "no-projected-tasks-prevents-stall", planID: "peer-plan", createPlan: true, planState: "completed"},
		{name: "live-session-root-prevents-stall", createRoot: true},
		{name: "empty-plan-id-permits-stall", createRoot: true, rootState: "completed", wantStalled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fabric := taskfabric.NewFabric()
				if tc.createPlan {
					mkAwaitTask(
						t,
						fabric,
						tc.planID,
						tc.planState,
					)
				}
				if tc.createRoot {
					mkAwaitTask(
						t,
						fabric,
						"sess/s-prereq/root",
						tc.rootState,
					)
				}
				got := AwaitSessionResult(
					context.Background(),
					fabric,
					"s-prereq",
					tc.planID,
					AwaitOptions{PollInterval: time.Millisecond, Wait: 20 * time.Millisecond},
				)
				require.Equal(t, tc.wantStalled, got.Stalled)
				want := context.DeadlineExceeded
				if tc.wantStalled {
					want = ErrSessionStalled
				}
				require.ErrorIs(t, got.Err, want)
			})
		})
	}
}

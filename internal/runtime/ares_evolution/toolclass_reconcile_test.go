package evolution

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// newToolClassDAG builds an empty L1 graph — the same shape serve starts from before it injects the
// schemas.
func newToolClassDAG(t *testing.T) *engine.MutableDAG {
	t.Helper()
	dag, err := engine.NewMutableDAG(nil)
	require.NoError(t, err)
	return dag
}

func spec(id string, md map[string]string) ToolClassSpec {
	return ToolClassSpec{NodeID: id, Name: id, AgentType: "tool/" + id, Metadata: md}
}

func reconcile(t *testing.T, dag *engine.MutableDAG, specs []ToolClassSpec, state *ReconcileState, grace int) ToolClassDiff {
	t.Helper()
	diff, err := ReconcileToolClassDAG(context.Background(), dag, specs, state, grace)
	require.NoError(t, err)
	return diff
}

// TestReconcileToolClassDAG_AddsUpdatesAndRemoves covers the three directions in one pass: a tool
// that appeared, one whose declaration changed, and one that is gone (grace 0 removes immediately).
func TestReconcileToolClassDAG_AddsUpdatesAndRemoves(t *testing.T) {
	ctx := context.Background()
	dag := newToolClassDAG(t)
	require.NoError(t, dag.AddNode(ctx, &engine.Step{
		ID: "gone#1", Name: "gone", AgentType: "tool/gone",
		Metadata: map[string]string{"enabled": "true"},
	}))
	require.NoError(t, dag.AddNode(ctx, &engine.Step{
		ID: "kept#1", Name: "kept", AgentType: "tool/kept",
		Metadata: map[string]string{"enabled": "true", "budget": "0", "prior": "0.1"},
	}))

	diff := reconcile(t, dag, []ToolClassSpec{
		spec("kept#1", map[string]string{"enabled": "true", "budget": "5", "prior": "0.1"}),
		spec("new#2", map[string]string{"enabled": "true", "budget": "0", "prior": ""}),
	}, &ReconcileState{}, 0)

	assert.Equal(t, []string{"new#2"}, diff.Added)
	assert.Equal(t, []string{"gone#1"}, diff.Removed)
	assert.Equal(t, []string{"kept#1"}, diff.Updated)

	md, ok := dag.NodeMetadata("kept#1")
	require.True(t, ok)
	assert.Equal(t, "5", md["budget"], "a changed declaration must reach the graph")
	assert.False(t, dag.HasNode("gone#1"), "an undeclared ToolClass must not stay advertised")
	assert.True(t, dag.HasNode("new#2"))
}

// TestReconcileToolClassDAG_IsIdempotent is what makes a timer safe to run: the second pass over an
// unchanged declaration must report nothing, or every tick would touch the graph (and wake every
// subscriber) for no reason.
func TestReconcileToolClassDAG_IsIdempotent(t *testing.T) {
	dag := newToolClassDAG(t)
	specs := []ToolClassSpec{spec("t#1", map[string]string{"enabled": "true", "budget": "0", "prior": ""})}

	first := reconcile(t, dag, specs, &ReconcileState{}, 0)
	require.False(t, first.Empty())

	second := reconcile(t, dag, specs, &ReconcileState{}, 0)
	assert.True(t, second.Empty(), "reconciling an unchanged declaration must change nothing")
}

// TestReconcileToolClassDAG_RemovalWaitsForGrace is the guard against a partial registry read.
//
// An MCP server reconnecting can return fewer tools for a moment; removing a node whose tags said
// enabled="false" would make that tool permissive again (unknown nodes are), so removal waits for
// consecutive misses instead of acting on one.
func TestReconcileToolClassDAG_RemovalWaitsForGrace(t *testing.T) {
	ctx := context.Background()
	dag := newToolClassDAG(t)
	require.NoError(t, dag.AddNode(ctx, &engine.Step{
		ID: "flaky#1", Name: "flaky", AgentType: "tool/flaky",
		Metadata: map[string]string{"enabled": "false"},
	}))
	state := &ReconcileState{}
	other := []ToolClassSpec{spec("other#1", map[string]string{"enabled": "true"})}

	first := reconcile(t, dag, other, state, 3)
	assert.Equal(t, []string{"flaky#1"}, first.Deferred)
	assert.Empty(t, first.Removed)
	assert.True(t, dag.HasNode("flaky#1"), "one miss is not evidence of removal")

	reconcile(t, dag, other, state, 3)
	assert.True(t, dag.HasNode("flaky#1"), "two misses still are not")

	third := reconcile(t, dag, other, state, 3)
	assert.Equal(t, []string{"flaky#1"}, third.Removed)
	assert.False(t, dag.HasNode("flaky#1"), "the third consecutive miss removes it")
}

// TestReconcileToolClassDAG_AppearanceClearsTheGraceWindow: a tool that comes back must not be
// removed later on the strength of misses counted before it returned.
func TestReconcileToolClassDAG_AppearanceClearsTheGraceWindow(t *testing.T) {
	ctx := context.Background()
	dag := newToolClassDAG(t)
	require.NoError(t, dag.AddNode(ctx, &engine.Step{ID: "back#1", Name: "back", AgentType: "tool/back"}))
	state := &ReconcileState{}
	other := []ToolClassSpec{spec("other#1", map[string]string{"enabled": "true"})}

	reconcile(t, dag, other, state, 2)                              // miss 1
	reconcile(t, dag, append(other, spec("back#1", nil)), state, 2) // it is back
	diff := reconcile(t, dag, other, state, 2)                      // miss 1 again, not 2

	assert.Empty(t, diff.Removed, "the counter must restart after the tool reappears")
	assert.True(t, dag.HasNode("back#1"))
}

// TestReconcileToolClassDAG_FailureRollsBack is the transactional claim: a failure part-way through
// must leave the graph exactly as it was.
//
// The failure is real, not injected through a test hook: node "dep#2" depends on "gone#1", so
// removing "gone#1" fails with dependents-remain. Which node the pass removes FIRST depends on map
// iteration, so the assertion is about the OUTCOME — the graph is unchanged — rather than about a
// particular node having been restored. That is the property callers rely on when they log "the
// graph was left unchanged".
func TestReconcileToolClassDAG_FailureRollsBack(t *testing.T) {
	ctx := context.Background()
	dag, err := engine.NewMutableDAG([]*engine.Step{
		{ID: "gone#1", Name: "gone", AgentType: "tool/gone"},
		{ID: "dep#2", Name: "dep", AgentType: "tool/dep", DependsOn: []string{"gone#1"}},
	})
	require.NoError(t, err)

	// Declares only a NEW node, so every existing node is a removal candidate — and removing the
	// one with a dependent fails.
	_, err = ReconcileToolClassDAG(ctx, dag, []ToolClassSpec{spec("fresh#3", map[string]string{"enabled": "true"})}, &ReconcileState{}, 0)
	require.Error(t, err, "the removal of a depended-on node must fail")

	for _, id := range []string{"gone#1", "dep#2"} {
		assert.True(t, dag.HasNode(id), "rollback must leave the previous graph intact (%s)", id)
	}
	assert.False(t, dag.HasNode("fresh#3"), "and drop whatever this pass had added")
}

// TestReconcileToolClassDAG_EmptyDeclarationIsReportedNotApplied: an empty catalogue is ambiguous,
// and wiping the graph would re-enable every tool whose tags disabled it.
func TestReconcileToolClassDAG_EmptyDeclarationIsReportedNotApplied(t *testing.T) {
	ctx := context.Background()
	dag := newToolClassDAG(t)
	require.NoError(t, dag.AddNode(ctx, &engine.Step{
		ID: "disabled#1", Name: "disabled", AgentType: "tool/disabled",
		Metadata: map[string]string{"enabled": "false"},
	}))

	diff, err := ReconcileToolClassDAG(ctx, dag, nil, &ReconcileState{}, 0)
	require.NoError(t, err)

	assert.NotEmpty(t, diff.Note, "the caller must be able to warn about it")
	assert.Empty(t, diff.Removed)
	assert.True(t, dag.HasNode("disabled#1"))
	md, ok := dag.NodeMetadata("disabled#1")
	require.True(t, ok)
	assert.Equal(t, "false", md["enabled"])
}

// TestReconcileToolClassDAG_ConflictingDeclarations: two declarations of one ToolClass that disagree
// would otherwise resolve by map iteration order.
func TestReconcileToolClassDAG_ConflictingDeclarations(t *testing.T) {
	dag := newToolClassDAG(t)
	_, err := ReconcileToolClassDAG(context.Background(), dag, []ToolClassSpec{
		spec("dup#1", map[string]string{"enabled": "true"}),
		spec("dup#1", map[string]string{"enabled": "false"}),
	}, &ReconcileState{}, 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicting declarations")
}

// TestReconcileToolClassDAG_NilDAGIsAnError: returning an empty diff would let a wiring mistake look
// like "nothing to do".
func TestReconcileToolClassDAG_NilDAGIsAnError(t *testing.T) {
	_, err := ReconcileToolClassDAG(context.Background(), nil, []ToolClassSpec{spec("t#1", nil)}, &ReconcileState{}, 0)
	require.Error(t, err)
}

// TestToolClassReconciler_LogsLevelByOutcome: routine changes are Info, while a removal or a skipped
// empty declaration is Warn — a shrinking governance surface is not routine.
func TestToolClassReconciler_LogsLevelByOutcome(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	dag := newToolClassDAG(t)
	specs := []ToolClassSpec{spec("t#1", map[string]string{"enabled": "true"})}
	r := NewToolClassReconciler(dag, func(context.Context) ([]ToolClassSpec, error) { return specs, nil }, time.Hour, logger)

	_, err := r.ReconcileOnce(context.Background())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "toolclass graph reconciled")
	assert.Contains(t, buf.String(), "level=INFO")
	assert.Contains(t, buf.String(), "updated_nodes")

	buf.Reset()
	_, err = r.ReconcileOnce(context.Background())
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "toolclass graph", "an unchanged reconcile must stay silent")

	// Empty declaration → Warn with the note.
	buf.Reset()
	empty := NewToolClassReconciler(dag, func(context.Context) ([]ToolClassSpec, error) { return nil, nil }, time.Hour, logger)
	_, err = empty.ReconcileOnce(context.Background())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "level=WARN")
	assert.Contains(t, buf.String(), "empty declaration")
}

// TestToolClassReconciler_TriggerReconcilesWithoutWaiting: the periodic interval is minutes, so a
// registry change must be able to force a pass immediately.
func TestToolClassReconciler_TriggerReconcilesWithoutWaiting(t *testing.T) {
	dag := newToolClassDAG(t)
	r := NewToolClassReconciler(dag, func(context.Context) ([]ToolClassSpec, error) {
		return []ToolClassSpec{spec("t#1", map[string]string{"enabled": "true"})}, nil
	}, 0, nil) // interval 0: only Trigger drives it

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	r.Trigger()

	require.Eventually(t, func() bool { return dag.HasNode("t#1") }, 2*time.Second, 5*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// TestToolClassReconciler_RunStopsOnCancel covers the timer-driven path.
func TestToolClassReconciler_RunStopsOnCancel(t *testing.T) {
	dag := newToolClassDAG(t)
	r := NewToolClassReconciler(dag, func(context.Context) ([]ToolClassSpec, error) {
		return []ToolClassSpec{spec("t#1", map[string]string{"enabled": "true"})}, nil
	}, 5*time.Millisecond, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var once sync.Once
	go func() {
		r.Run(ctx)
		once.Do(func() { close(done) })
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// TestToolClassReconciler_RunAlignsBeforeTheFirstTick: the loop used to reach its first reconcile
// only after a full interval, so a graph built at boot could stay stale for minutes even though the
// worker was already running. "Started" and "reconciled" must not be different states.
func TestToolClassReconciler_RunAlignsBeforeTheFirstTick(t *testing.T) {
	dag := newToolClassDAG(t)
	r := NewToolClassReconciler(dag, func(context.Context) ([]ToolClassSpec, error) {
		return []ToolClassSpec{spec("t#1", map[string]string{"enabled": "true"})}, nil
	}, time.Hour, nil) // a tick would take an hour; only the immediate pass can satisfy this

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	require.Eventually(t, func() bool { return dag.HasNode("t#1") }, 2*time.Second, 5*time.Millisecond,
		"the first pass must not wait for the interval")
}

// TestToolClassReconciler_UnsupervisedPanicStopsOnlyTheWorker: the comp==nil path has no
// supervisor, so it owns its recover boundary. Without it a panic inside ReconcileOnce would take
// the whole process down rather than the worker — the failure mode this boundary exists to prevent.
func TestToolClassReconciler_UnsupervisedPanicStopsOnlyTheWorker(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := NewToolClassReconciler(newToolClassDAG(t), func(context.Context) ([]ToolClassSpec, error) {
		panic("spec source exploded")
	}, time.Hour, logger)

	done := make(chan struct{})
	go func() {
		r.RunUnsupervised(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker did not stop after panicking")
	}
	assert.Contains(t, buf.String(), "panicked without a supervisor")
}

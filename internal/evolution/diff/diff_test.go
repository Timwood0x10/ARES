package diff

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/evolution/genome"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
	"github.com/Timwood0x10/ares/internal/workflow/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDifferInterface(t *testing.T) {
	var d Differ
	d = NewWorkflowDiffer()
	assert.Equal(t, "workflow", d.Name())

	d = NewKnowledgeDiffer()
	assert.Equal(t, genome.KnowledgeGenomeName, d.Name())

	d = NewSchedulerDiffer()
	assert.Equal(t, genome.SchedulerGenomeName, d.Name())

	d = NewRecoveryDiffer()
	assert.Equal(t, genome.RecoveryGenomeName, d.Name())
}

// ── Registry ───────────────────────────────

func TestDiffRegistry_Register(t *testing.T) {
	r := NewRegistry()
	err := r.Register(NewWorkflowDiffer())
	assert.NoError(t, err)
}

func TestDiffRegistry_Register_Duplicate(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(NewWorkflowDiffer()))
	err := r.Register(NewWorkflowDiffer())
	assert.Error(t, err)
}

func TestDiffRegistry_Register_Nil(t *testing.T) {
	r := NewRegistry()
	err := r.Register(nil)
	assert.Error(t, err)
}

func TestDiffRegistry_Get(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(NewWorkflowDiffer()))

	d, err := r.Get("workflow")
	require.NoError(t, err)
	assert.NotNil(t, d)
}

func TestDiffRegistry_Get_NotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("nonexistent")
	assert.Error(t, err)
}

func TestDiffRegistry_List(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(NewWorkflowDiffer()))
	require.NoError(t, r.Register(NewSchedulerDiffer()))

	names := r.List()
	assert.ElementsMatch(t, []string{"workflow", "scheduler"}, names)
}

func TestDiffRegistry_DiffAll(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(NewWorkflowDiffer()))
	require.NoError(t, r.Register(NewSchedulerDiffer()))

	ctx := context.Background()
	snapshots := map[string]SnapshotPair{
		"workflow":  {Old: oldDAG(t), New: newDAG(t)},
		"scheduler": {Old: graph.NewDefaultScheduler(), New: graph.NewRoundRobinScheduler()},
	}

	patches, err := r.DiffAll(ctx, snapshots)
	require.NoError(t, err)
	assert.Greater(t, len(patches), 0)
}

// ── WorkflowDiffer ─────────────────────────

func TestWorkflowDiffer_Diff(t *testing.T) {
	ctx := context.Background()
	d := NewWorkflowDiffer()

	oldSnap := oldDAG(t)
	newSnap := newDAG(t)

	patches, err := d.Diff(ctx, oldSnap, newSnap)
	require.NoError(t, err)
	assert.Greater(t, len(patches), 0,
		"should detect differences between old and new DAG")

	// Should find InsertNode and AddEdge patches.
	foundInsert := false
	foundAddEdge := false
	for _, p := range patches {
		switch p.Type {
		case patch.PatchInsertNode:
			foundInsert = true
		case patch.PatchAddEdge:
			foundAddEdge = true
		}
	}
	assert.True(t, foundInsert, "should have an insert_node patch")
	assert.True(t, foundAddEdge, "should have an add_edge patch")
}

func TestWorkflowDiffer_Diff_NoChanges(t *testing.T) {
	ctx := context.Background()
	d := NewWorkflowDiffer()

	dag := oldDAG(t)
	patches, err := d.Diff(ctx, dag, dag)
	require.NoError(t, err)
	assert.Len(t, patches, 0, "identical DAGs should produce no patches")
}

// ── KnowledgeDiffer ────────────────────────

func TestKnowledgeDiffer_Diff(t *testing.T) {
	ctx := context.Background()
	d := NewKnowledgeDiffer()

	oldCfg := genome.DefaultKnowledgeGenomeConfig()
	newCfg := oldCfg
	newCfg.MaxResults = 50

	patches, err := d.Diff(ctx, oldCfg, newCfg)
	require.NoError(t, err)
	assert.Len(t, patches, 1)
	assert.Equal(t, patch.PatchChangeBudget, patches[0].Type)
}

func TestKnowledgeDiffer_Diff_NoChanges(t *testing.T) {
	ctx := context.Background()
	d := NewKnowledgeDiffer()

	cfg := genome.DefaultKnowledgeGenomeConfig()
	patches, err := d.Diff(ctx, cfg, cfg)
	require.NoError(t, err)
	assert.Len(t, patches, 0)
}

// ── SchedulerDiffer ────────────────────────

func TestSchedulerDiffer_Diff(t *testing.T) {
	ctx := context.Background()
	d := NewSchedulerDiffer()

	patches, err := d.Diff(ctx, graph.NewDefaultScheduler(), graph.NewRoundRobinScheduler())
	require.NoError(t, err)
	assert.Len(t, patches, 1)
	assert.Equal(t, patch.PatchChangeScheduler, patches[0].Type)
}

func TestSchedulerDiffer_Diff_NoChanges(t *testing.T) {
	ctx := context.Background()
	d := NewSchedulerDiffer()

	patches, err := d.Diff(ctx, graph.NewDefaultScheduler(), graph.NewDefaultScheduler())
	require.NoError(t, err)
	assert.Len(t, patches, 0)
}

// ── RecoveryDiffer ─────────────────────────

func TestRecoveryDiffer_Diff(t *testing.T) {
	ctx := context.Background()
	d := NewRecoveryDiffer()

	oldPolicy := &engine.RecoveryPolicy{Strategy: engine.RecoveryFailFast}
	newPolicy := &engine.RecoveryPolicy{Strategy: engine.RecoveryRetry}

	patches, err := d.Diff(ctx, oldPolicy, newPolicy)
	require.NoError(t, err)
	assert.Len(t, patches, 1)
	assert.Equal(t, patch.PatchChangeRecoveryStrategy, patches[0].Type)
}

func TestRecoveryDiffer_Diff_NoChanges(t *testing.T) {
	ctx := context.Background()
	d := NewRecoveryDiffer()

	policy := &engine.RecoveryPolicy{Strategy: engine.RecoveryRetry, MaxAttempts: 3}
	patches, err := d.Diff(ctx, policy, policy)
	require.NoError(t, err)
	assert.Len(t, patches, 0)
}

// ── Helpers ────────────────────────────────

func oldDAG(t *testing.T) *engine.DAG {
	t.Helper()
	return &engine.DAG{
		Nodes: map[string]*engine.DAGNode{
			"A": {StepID: "A"}, "B": {StepID: "B"}, "C": {StepID: "C"},
		},
		Edges: map[string][]string{
			"A": {"B"}, "B": {"C"},
		},
	}
}

func newDAG(t *testing.T) *engine.DAG {
	t.Helper()
	return &engine.DAG{
		Nodes: map[string]*engine.DAGNode{
			"A": {StepID: "A"}, "B": {StepID: "B"}, "C": {StepID: "C"}, "D": {StepID: "D"},
		},
		Edges: map[string][]string{
			"A": {"B"}, "B": {"C"}, "C": {"D"},
		},
	}
}

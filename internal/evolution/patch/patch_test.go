package patch

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── PatchType ─────────────────────────────────

func TestPatchType_String(t *testing.T) {
	tests := []struct {
		pt   PatchType
		want string
	}{
		{PatchInsertNode, "insert_node"},
		{PatchRemoveNode, "remove_node"},
		{PatchReplaceNode, "replace_node"},
		{PatchAddEdge, "add_edge"},
		{PatchRemoveEdge, "remove_edge"},
		{PatchChangeScheduler, "change_scheduler"},
		{PatchChangePlanner, "change_planner"},
		{PatchChangeReducer, "change_reducer"},
		{PatchChangeBudget, "change_budget"},
		{PatchChangeRecoveryStrategy, "change_recovery_strategy"},
		{PatchChangeMaxRetries, "change_max_retries"},
		{PatchChangeBackoff, "change_backoff"},
		{PatchType(99), "unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.pt.String())
		})
	}
}

// ── RuntimePatch ──────────────────────────────

func TestRuntimePatch_Create(t *testing.T) {
	p := RuntimePatch{
		Type:   PatchInsertNode,
		Target: "validator",
		Value:  "new-step",
		Reason: "chaos detected failure",
		Source: "chaos",
	}
	assert.Equal(t, PatchInsertNode, p.Type)
	assert.Equal(t, "validator", p.Target)
	assert.Equal(t, "new-step", p.Value)
	assert.Equal(t, "chaos detected failure", p.Reason)
	assert.Equal(t, "chaos", p.Source)
	assert.Nil(t, p.Rollback)
}

func TestRuntimePatch_WithRollback(t *testing.T) {
	p := RuntimePatch{
		Type:   PatchInsertNode,
		Target: "validator",
		Value:  "new-step",
		Rollback: &RuntimePatch{
			Type:   PatchRemoveNode,
			Target: "validator",
		},
	}
	require.NotNil(t, p.Rollback)
	assert.Equal(t, PatchRemoveNode, p.Rollback.Type)
	assert.Equal(t, "validator", p.Rollback.Target)
}

// ── PatchSet ──────────────────────────────────

func TestPatchSet_Create(t *testing.T) {
	ps := PatchSet{
		Patches: []RuntimePatch{
			{Type: PatchInsertNode, Target: "a"},
			{Type: PatchInsertNode, Target: "b"},
		},
		Reason: "batch insert",
		Source: "genome",
	}
	assert.Len(t, ps.Patches, 2)
	assert.Equal(t, "batch insert", ps.Reason)
}

// ── mockExecutor ──────────────────────────────

type mockExecutor struct {
	mu       sync.Mutex
	applied  []RuntimePatch
	canFail  bool
	rollback *RuntimePatch
}

func (m *mockExecutor) Apply(_ context.Context, p RuntimePatch) (*RuntimePatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.canFail {
		return nil, fmt.Errorf("mock: apply failed")
	}
	m.applied = append(m.applied, p)
	return m.rollback, nil
}

func (m *mockExecutor) CanApply(_ context.Context, p RuntimePatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.canFail {
		return fmt.Errorf("mock: cannot apply")
	}
	return nil
}

// ── Registry ──────────────────────────────────

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()
	ex := &mockExecutor{}

	err := r.Register("graph", ex)
	assert.NoError(t, err)
}

func TestRegistry_Register_Duplicate(t *testing.T) {
	r := NewRegistry()
	ex := &mockExecutor{}

	require.NoError(t, r.Register("graph", ex))
	err := r.Register("graph", &mockExecutor{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegistry_Register_EmptyTarget(t *testing.T) {
	r := NewRegistry()
	err := r.Register("", &mockExecutor{})
	assert.Error(t, err)
}

func TestRegistry_Register_NilExecutor(t *testing.T) {
	r := NewRegistry()
	err := r.Register("graph", nil)
	assert.Error(t, err)
}

func TestRegistry_Apply(t *testing.T) {
	r := NewRegistry()
	ex := &mockExecutor{rollback: &RuntimePatch{Type: PatchRemoveNode, Target: "v"}}
	require.NoError(t, r.Register("graph", ex))

	err := r.Apply(context.Background(), RuntimePatch{
		Type:   PatchInsertNode,
		Target: "graph",
		Value:  "validator",
	})
	assert.NoError(t, err)
	assert.Len(t, ex.applied, 1)
	assert.Equal(t, PatchInsertNode, ex.applied[0].Type)
}

func TestRegistry_Apply_NoExecutor(t *testing.T) {
	r := NewRegistry()

	err := r.Apply(context.Background(), RuntimePatch{
		Type:   PatchInsertNode,
		Target: "unknown",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no executor registered")
}

func TestRegistry_Apply_FailureWithRollback(t *testing.T) {
	r := NewRegistry()
	primary := &mockExecutor{canFail: true, rollback: &RuntimePatch{Type: PatchRemoveNode, Target: "graph"}}
	require.NoError(t, r.Register("graph", primary))

	err := r.Apply(context.Background(), RuntimePatch{
		Type:   PatchInsertNode,
		Target: "graph",
		Value:  "validator",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "apply failed")
}

func TestRegistry_ApplySet(t *testing.T) {
	r := NewRegistry()
	ex := &mockExecutor{rollback: &RuntimePatch{Type: PatchRemoveNode, Target: "graph"}}
	require.NoError(t, r.Register("graph", ex))

	err := r.ApplySet(context.Background(), PatchSet{
		Patches: []RuntimePatch{
			{Type: PatchInsertNode, Target: "graph", Value: "a"},
			{Type: PatchInsertNode, Target: "graph", Value: "b"},
		},
	})
	assert.NoError(t, err)
	assert.Len(t, ex.applied, 2)
}

func TestRegistry_ApplySet_Empty(t *testing.T) {
	r := NewRegistry()
	err := r.ApplySet(context.Background(), PatchSet{})
	assert.NoError(t, err)
}

func TestRegistry_ApplySet_FailureRollsBack(t *testing.T) {
	r := NewRegistry()
	ex := &mockExecutor{}
	require.NoError(t, r.Register("graph", ex))
	require.NoError(t, r.Register("other", &mockExecutor{canFail: true}))

	err := r.ApplySet(context.Background(), PatchSet{
		Patches: []RuntimePatch{
			{Type: PatchInsertNode, Target: "graph", Value: "a"},
			{Type: PatchInsertNode, Target: "other", Value: "b"}, // will fail
		},
		Reason: "should rollback",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot apply")
}

func TestRegistry_ApplySet_MissingExecutor(t *testing.T) {
	r := NewRegistry()
	ex := &mockExecutor{}
	require.NoError(t, r.Register("graph", ex))

	err := r.ApplySet(context.Background(), PatchSet{
		Patches: []RuntimePatch{
			{Type: PatchInsertNode, Target: "graph", Value: "a"},
			{Type: PatchInsertNode, Target: "nonexistent", Value: "b"},
		},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no executor")
}

// ── Concurrent ────────────────────────────────

func TestRegistry_ConcurrentApply(t *testing.T) {
	r := NewRegistry()
	ex := &mockExecutor{}
	require.NoError(t, r.Register("graph", ex))

	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			_ = r.Apply(context.Background(), RuntimePatch{
				Type:   PatchInsertNode,
				Target: "graph",
				Value:  fmt.Sprintf("node-%d", i),
			})
		}()
	}
	wg.Wait()
	assert.Len(t, ex.applied, n)
}

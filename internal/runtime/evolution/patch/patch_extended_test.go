package patch

import (
	"context"
	"errors"
	"testing"
)

type stubExecutor struct {
	applyErr   error
	applyCount int
	rollback   *RuntimePatch
	name       string
}

func (s *stubExecutor) Name() string { return s.name }

func (s *stubExecutor) Apply(_ context.Context, _ RuntimePatch) (*RuntimePatch, error) {
	s.applyCount++
	if s.applyErr != nil {
		return s.rollback, s.applyErr
	}
	return nil, nil
}

func (s *stubExecutor) CanApply(_ context.Context, _ RuntimePatch) error {
	return nil
}

func (s *stubExecutor) Snapshot(_ context.Context) (any, error) { return nil, nil }

func (s *stubExecutor) Restore(_ context.Context, _ any) error { return nil }

func TestPatchTypeStringAll(t *testing.T) {
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
		{PatchChangeInstruction, "change_instruction"},
		{PatchSetNodeMetadata, "set_node_metadata"},
	}
	for _, tt := range tests {
		if got := tt.pt.String(); got != tt.want {
			t.Errorf("PatchType(%d).String() = %q, want %q", int(tt.pt), got, tt.want)
		}
	}
}

func TestRegistryRegisterAndCanApply(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("t1", &stubExecutor{})

	if !r.CanApply("t1") {
		t.Error("CanApply(t1) = false, want true")
	}
	if r.CanApply("missing") {
		t.Error("CanApply(missing) = true, want false")
	}
}

func TestRegistryRegisterEmptyTarget(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("", &stubExecutor{}); err == nil {
		t.Error("expected error for empty target")
	}
}

func TestRegistryApplyNoExecutor(t *testing.T) {
	r := NewRegistry()
	err := r.Apply(context.Background(), RuntimePatch{Target: "missing"})
	if err == nil {
		t.Error("expected error for missing executor")
	}
}

func TestRegistryApplySuccess(t *testing.T) {
	r := NewRegistry()
	ex := &stubExecutor{}
	_ = r.Register("t1", ex)

	err := r.Apply(context.Background(), RuntimePatch{ID: "p1", Target: "t1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if ex.applyCount != 1 {
		t.Errorf("applyCount = %d, want 1", ex.applyCount)
	}
}

func TestRegistryApplyIdempotent(t *testing.T) {
	r := NewRegistry()
	ex := &stubExecutor{}
	_ = r.Register("t1", ex)

	p := RuntimePatch{ID: "p1", Target: "t1"}
	_ = r.Apply(context.Background(), p)
	_ = r.Apply(context.Background(), p)
	if ex.applyCount != 1 {
		t.Errorf("applyCount = %d, want 1 (idempotent)", ex.applyCount)
	}
}

func TestRegistryApplyExecutorError(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("t1", &stubExecutor{applyErr: errors.New("fail")})
	err := r.Apply(context.Background(), RuntimePatch{ID: "p1", Target: "t1"})
	if err == nil {
		t.Error("expected error from failed executor")
	}
}

func TestRegistryApplyFallback(t *testing.T) {
	r := NewRegistry()
	fb := &stubExecutor{}
	r.SetFallback(fb)

	err := r.Apply(context.Background(), RuntimePatch{ID: "p1", Target: "unknown"})
	if err != nil {
		t.Fatalf("Apply via fallback: %v", err)
	}
	if fb.applyCount != 1 {
		t.Errorf("fallback applyCount = %d, want 1", fb.applyCount)
	}
}

func TestRegistryApplyFallbackError(t *testing.T) {
	r := NewRegistry()
	r.SetFallback(&stubExecutor{applyErr: errors.New("fallback fail")})
	err := r.Apply(context.Background(), RuntimePatch{ID: "p1", Target: "unknown"})
	if err == nil {
		t.Error("expected error from failed fallback")
	}
}

func TestRegistryApplySetEmpty(t *testing.T) {
	r := NewRegistry()
	if err := r.ApplySet(context.Background(), PatchSet{}); err != nil {
		t.Fatalf("ApplySet(empty): %v", err)
	}
}

func TestRegistryApplySetAllSuccess(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("t1", &stubExecutor{})
	_ = r.Register("t2", &stubExecutor{})

	err := r.ApplySet(context.Background(), PatchSet{
		Patches: []RuntimePatch{
			{ID: "p1", Target: "t1"},
			{ID: "p2", Target: "t2"},
		},
	})
	if err != nil {
		t.Fatalf("ApplySet: %v", err)
	}
}

func TestRegistryApplySetPartialFailure(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("t1", &stubExecutor{})
	_ = r.Register("t2", &stubExecutor{applyErr: errors.New("fail")})

	err := r.ApplySet(context.Background(), PatchSet{
		Patches: []RuntimePatch{
			{ID: "p1", Target: "t1"},
			{ID: "p2", Target: "t2"},
		},
	})
	if err == nil {
		t.Error("expected error when second patch fails")
	}
}

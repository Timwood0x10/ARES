// Package compiler tests for MemoryEmitter and InMemoryMemoryStore.
package compiler

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
)

// ── InMemoryMemoryStore tests ───────────────────────────────────────────

func TestInMemoryMemoryStoreSaveAndAll(t *testing.T) {
	store := NewInMemoryMemoryStore()
	if store.Name() != "in-memory" {
		t.Fatalf("expected name %q, got %q", "in-memory", store.Name())
	}

	memories := []distillation.Memory{
		{ID: "m1", Content: "first"},
		{ID: "m2", Content: "second"},
	}
	n, err := store.Save(context.Background(), memories)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 stored, got %d", n)
	}

	all := store.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(all))
	}

	// All returns a copy: mutating it must not affect the store.
	all[0].ID = "mutated"
	if got := store.All()[0].ID; got != "m1" {
		t.Errorf("All() did not return a copy: got %q, want m1", got)
	}

	// Empty input is a no-op.
	if n, err := store.Save(context.Background(), nil); err != nil || n != 0 {
		t.Errorf("Save(nil) = (%d, %v), want (0, nil)", n, err)
	}
}

func TestInMemoryMemoryStoreCancelledContext(t *testing.T) {
	store := NewInMemoryMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.Save(ctx, []distillation.Memory{{ID: "m1"}}); err == nil {
		t.Error("expected error for cancelled context")
	}
}

// ── MemoryEmitter tests ─────────────────────────────────────────────────

func TestMemoryEmitterEmitsNodes(t *testing.T) {
	store := NewInMemoryMemoryStore()
	emitter := NewMemoryEmitter(store)

	sub := &SubGraph{
		Nodes: []*Node{
			{ID: "e1", Type: NodeEntity, Confidence: 0.9, Source: "msg1",
				Attributes: map[string]any{"name": "RuntimePatch fixes the critical error in the system"}},
			{ID: "f1", Type: NodeFact, Confidence: 0.8, Source: "msg1",
				Attributes: map[string]any{
					"subject": "ARES", "predicate": "uses", "object": "RuntimePatch to resolve the error",
				}},
			{ID: "d1", Type: NodeDecision, Confidence: 0.9, Source: "msg2",
				Attributes: map[string]any{"choice": "Adopt Patch to fix the deployment error", "rejection": "hot reload"}},
		},
	}

	n, err := emitter.Emit(context.Background(), sub, "tenant-42", "user-7")
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 memories stored, got %d", n)
	}

	all := store.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 memories in store, got %d", len(all))
	}

	for _, m := range all {
		if m.Content == "" {
			t.Errorf("memory %q has empty content", m.ID)
		}
		tid, _ := m.Metadata["tenant_id"].(string)
		uid, _ := m.Metadata["user_id"].(string)
		nt, _ := m.Metadata["node_type"].(string)
		if tid != "tenant-42" {
			t.Errorf("memory %q tenant_id = %q, want tenant-42", m.ID, tid)
		}
		if uid != "user-7" {
			t.Errorf("memory %q user_id = %q, want user-7", m.ID, uid)
		}
		if nt == "" {
			t.Errorf("memory %q missing node_type metadata", m.ID)
		}
		if m.Importance <= 0 {
			t.Errorf("memory %q importance = %v, want > 0", m.ID, m.Importance)
		}
	}
}

func TestMemoryEmitterEmitsMemoryNode(t *testing.T) {
	// NodeMemory nodes ARE the memory representation and must be emitted.
	store := NewInMemoryMemoryStore()
	emitter := NewMemoryEmitter(store)

	sub := &SubGraph{
		Nodes: []*Node{
			{ID: "mem1", Type: NodeMemory, Source: "msg1",
				Attributes: map[string]any{"name": "Fix the critical error before deployment"}},
		},
	}
	n, err := emitter.Emit(context.Background(), sub, "t", "u")
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 memory node emitted, got %d", n)
	}
}

func TestMemoryEmitterSkipsLowImportance(t *testing.T) {
	store := NewInMemoryMemoryStore()
	emitter := NewMemoryEmitter(store)

	sub := &SubGraph{
		Nodes: []*Node{
			{ID: "good", Type: NodeFact, Source: "msg1",
				Attributes: map[string]any{
					"subject": "X", "predicate": "fixes", "object": "the critical error",
				}},
			{ID: "noise", Type: NodeEntity, Attributes: map[string]any{"name": "x"}},
		},
	}

	n, err := emitter.Emit(context.Background(), sub, "t", "u")
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 memory (low-importance skipped), got %d", n)
	}

	all := store.All()
	if len(all) != 1 || all[0].ID != "good" {
		t.Errorf("expected only 'good' stored, got %+v", all)
	}
}

func TestMemoryEmitterEdgeCases(t *testing.T) {
	store := NewInMemoryMemoryStore()
	emitter := NewMemoryEmitter(store)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		ctx     context.Context
		sub     *SubGraph
		wantN   int
		wantErr bool
	}{
		{name: "nil sub", ctx: context.Background(), sub: nil, wantN: 0, wantErr: false},
		{name: "empty sub", ctx: context.Background(), sub: &SubGraph{}, wantN: 0, wantErr: false},
		{name: "cancelled ctx", ctx: cancelledCtx,
			sub:     &SubGraph{Nodes: []*Node{{ID: "n1", Type: NodeEntity, Attributes: map[string]any{"name": "x"}}}},
			wantN:   0,
			wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := emitter.Emit(tt.ctx, tt.sub, "t", "u")
			if (err != nil) != tt.wantErr {
				t.Errorf("Emit err = %v, wantErr = %v", err, tt.wantErr)
			}
			if n != tt.wantN {
				t.Errorf("Emit n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

func TestMemoryEmitterNilStore(t *testing.T) {
	emitter := NewMemoryEmitter(nil)
	n, err := emitter.Emit(context.Background(),
		&SubGraph{Nodes: []*Node{{ID: "n1", Type: NodeEntity, Attributes: map[string]any{"name": "x"}}}},
		"t", "u")
	if err == nil {
		t.Error("expected error for nil store")
	}
	if n != 0 {
		t.Errorf("expected 0 stored for nil store, got %d", n)
	}
}

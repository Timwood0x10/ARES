// isolation_test.go locks the defensive-copy contract of the in-memory
// context stores:
//
//   - REVIEW 3.3#4 (already fixed in the CRITICAL/MEDIUM batch, locked here):
//     SessionMemory.Set must copy the caller's messages slice.
//   - REVIEW 3.3#5: TaskMemory.Get must copy the nested Context map and the
//     Steps/Results slices — the previous struct-only shallow copy still
//     aliased them, so a caller's nested writes raced with SetContext/AddStep.
package context

import (
	"context"
	"testing"
	"time"
)

func TestSessionMemorySetCopiesCallerSlice(t *testing.T) {
	m := NewSessionMemory(10, time.Minute)
	messages := []Message{{Role: RoleUser, Content: "first"}}

	_ = m.Set(context.Background(), "s1", "u1", messages)

	// Mutate the caller's backing array after Set.
	messages[0].Content = "mutated"

	stored, ok := m.Get(context.Background(), "s1")
	if !ok {
		t.Fatal("expected session to exist")
	}
	if stored.Messages[0].Content != "first" {
		t.Errorf("stored message aliased the caller's slice: got %q, want %q",
			stored.Messages[0].Content, "first")
	}
}

func TestTaskMemoryGetIsolatesNestedState(t *testing.T) {
	m := NewTaskMemory(10, time.Minute)
	_ = m.Set(context.Background(), "t1", "sess", "user", "input")
	_ = m.SetContext(context.Background(), "t1", "cfg", map[string]interface{}{"nested": true})
	_ = m.AddStep(context.Background(), "t1", StepRecord{Name: "step-1"})
	_ = m.AddResult(context.Background(), "t1", ResultRecord{Type: "final"})

	got, ok := m.Get(context.Background(), "t1")
	if !ok {
		t.Fatal("expected task to exist")
	}

	// Corrupt everything reachable through the returned copy.
	got.Context["cfg"] = map[string]interface{}{"hijacked": true}
	got.Context["injected"] = "x"
	got.Steps[0].Name = "hijacked"
	got.Results[0].Type = "hijacked"
	got.Steps = append(got.Steps, StepRecord{Name: "extra"})

	again, ok := m.Get(context.Background(), "t1")
	if !ok {
		t.Fatal("expected task to still exist")
	}
	if cfg, ok := again.Context["cfg"].(map[string]interface{}); !ok || cfg["nested"] != true {
		t.Errorf("Context map aliased the returned copy: cfg = %v", again.Context["cfg"])
	}
	if _, exists := again.Context["injected"]; exists {
		t.Error("Context map aliased the returned copy: injected key visible")
	}
	if len(again.Steps) != 1 || again.Steps[0].Name != "step-1" {
		t.Errorf("Steps slice aliased the returned copy: %+v", again.Steps)
	}
	if len(again.Results) != 1 || again.Results[0].Type != "final" {
		t.Errorf("Results slice aliased the returned copy: %+v", again.Results)
	}
}

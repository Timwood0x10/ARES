package taskfabric

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestCompilePlan_Linear covers a straight A→B→C chain: all tasks are created
// READY and dependencies land verbatim on the fabric tasks.
func TestCompilePlan_Linear(t *testing.T) {
	f := NewFabric()
	ids, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "a", Capability: "code"},
		{ID: "b", Capability: "code", DependsOn: []string{"a"}},
		{ID: "c", Capability: "code", DependsOn: []string{"b"}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("created %d ids, want 3", len(ids))
	}
	b, err := f.Task("b")
	if err != nil {
		t.Fatalf("task b: %v", err)
	}
	if len(b.Dependencies) != 1 || b.Dependencies[0] != "a" {
		t.Fatalf("b deps = %v, want [a]", b.Dependencies)
	}
}

// TestCompilePlan_Diamond covers the fan-out/fan-in shape used by the
// dashboard's plan view (root → {left,right} → join).
func TestCompilePlan_Diamond(t *testing.T) {
	f := NewFabric()
	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "root", Capability: "plan"},
		{ID: "left", Capability: "code", DependsOn: []string{"root"}},
		{ID: "right", Capability: "review", DependsOn: []string{"root"}},
		{ID: "join", Capability: "synth", DependsOn: []string{"left", "right"}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
}

// TestCompilePlan_CycleRejected covers the topological gate: a→b→a must fail
// and create nothing.
func TestCompilePlan_CycleRejected(t *testing.T) {
	f := NewFabric()
	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "a", Capability: "code", DependsOn: []string{"b"}},
		{ID: "b", Capability: "code", DependsOn: []string{"a"}},
	})
	if err == nil {
		t.Fatal("cycle must be rejected")
	}
	for _, id := range []string{"a", "b"} {
		if _, terr := f.Task(id); terr == nil {
			t.Fatalf("task %q must not exist after failed compile", id)
		}
	}
}

// TestCompilePlan_UnknownDependencyRejected covers the closure gate.
func TestCompilePlan_UnknownDependencyRejected(t *testing.T) {
	f := NewFabric()
	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "a", Capability: "code", DependsOn: []string{"ghost"}},
	})
	if err == nil {
		t.Fatal("unknown dependency must be rejected")
	}
}

// TestCompilePlan_DuplicateIDRejected covers the id-uniqueness gate.
func TestCompilePlan_DuplicateIDRejected(t *testing.T) {
	f := NewFabric()
	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "a", Capability: "code"},
		{ID: "a", Capability: "review"},
	})
	if err == nil {
		t.Fatal("duplicate id must be rejected")
	}
}

// TestCompilePlan_AtomicRollback covers the all-or-nothing contract: a batch
// whose second Create fails must leave no trace of the first task.
func TestCompilePlan_AtomicRollback(t *testing.T) {
	f := NewFabric()
	if err := f.Create(&Task{ID: "preexisting"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "ok1", Capability: "code"},
		{ID: "preexisting", Capability: "code"}, // collides → Create fails
		{ID: "never", Capability: "code"},
	})
	if !errors.Is(err, ErrTaskExists) {
		t.Fatalf("want ErrTaskExists, got %v", err)
	}
	if _, terr := f.Task("ok1"); terr == nil {
		t.Fatal("ok1 must be rolled back (all-or-nothing)")
	}
}

// TestCompilePlan_PayloadRidesCheckpoint verifies step payloads surface in the
// task checkpoint envelope for the executor.
func TestCompilePlan_PayloadRidesCheckpoint(t *testing.T) {
	f := NewFabric()
	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "a", Capability: "code", Payload: map[string]any{"k": "v"}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	tk, terr := f.Task("a")
	if terr != nil {
		t.Fatalf("task a: %v", terr)
	}
	if tk.Checkpoint == nil {
		t.Fatal("payload must ride the checkpoint envelope")
	}
}

// TestCompilePlan_DeadlineResolvesToAbsolute pins C3 (plan/0.3.3_task.md): a
// PlanStep's RELATIVE Deadline becomes an absolute Task.Deadline at creation,
// resolved through the fabric clock so ExpireDeadlines and the Acquire guard
// read the same instant. A zero Deadline leaves Task.Deadline zero.
func TestCompilePlan_DeadlineResolvesToAbsolute(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	f := NewFabric().WithClock(func() time.Time { return fixed })
	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "with-deadline", Capability: "code", Deadline: 30 * time.Second},
		{ID: "no-deadline", Capability: "code"},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	withDL, err := f.Task("with-deadline")
	if err != nil {
		t.Fatalf("task with-deadline: %v", err)
	}
	if got, want := withDL.Deadline, fixed.Add(30*time.Second); !got.Equal(want) {
		t.Fatalf("Deadline = %v, want %v (now+30s)", got, want)
	}

	noDL, err := f.Task("no-deadline")
	if err != nil {
		t.Fatalf("task no-deadline: %v", err)
	}
	if !noDL.Deadline.IsZero() {
		t.Fatalf("unset Deadline must stay zero, got %v", noDL.Deadline)
	}
}

// TestCompilePlan_BackoffAndAllowPartialLand pins that the degradation/backoff
// policy carried by a PlanStep reaches the fabric Task verbatim (the fields
// CompilePlan previously set, now covered so a regression surfaces).
func TestCompilePlan_BackoffAndAllowPartialLand(t *testing.T) {
	f := NewFabric()
	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{
			ID:           "a",
			Capability:   "code",
			AllowPartial: true,
			BackoffBase:  time.Second,
			BackoffMax:   10 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	tk, err := f.Task("a")
	if err != nil {
		t.Fatalf("task a: %v", err)
	}
	if !tk.AllowPartial {
		t.Fatal("AllowPartial must land on the task")
	}
	if tk.BackoffBase != time.Second || tk.BackoffMax != 10*time.Second {
		t.Fatalf("backoff = (%v,%v), want (1s,10s)", tk.BackoffBase, tk.BackoffMax)
	}
}

package agentsyscall

import (
	"context"
	"testing"
	"time"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	kctx "github.com/Timwood0x10/ares/internal/kernel/ctx"
)

// TestCreatePlanCarriesDegradationPolicy pins C3 (plan/0.3.3_task.md): the LLM
// create_plan authoring path now threads AllowPartial, the retry backoff and
// the relative deadline onto the compiled fabric Task, so the failure-
// degradation policy is reachable from a real producer — not just hand-built
// PlanSteps in unit tests.
func TestCreatePlanCarriesDegradationPolicy(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fabric := taskfabric.NewFabric().WithClock(func() time.Time { return fixed })
	kernel := NewKernel(nil, fabric, nil, nil)

	ctx := kctx.WithCallerID(context.Background(), "agent-A")
	res, err := kernel.CreatePlan(ctx, CreatePlanArgs{
		Steps: []PlanStepArgs{{
			ID:            "s1",
			Capability:    "ares/plan",
			AllowPartial:  true,
			BackoffBaseMS: 1000,
			BackoffMaxMS:  10000,
			DeadlineMS:    30000,
		}},
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if len(res.TaskIDs) != 1 {
		t.Fatalf("created %d tasks, want 1", len(res.TaskIDs))
	}

	tk, err := fabric.Task(res.TaskIDs[0])
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if !tk.AllowPartial {
		t.Fatal("AllowPartial must reach the task")
	}
	if tk.BackoffBase != time.Second || tk.BackoffMax != 10*time.Second {
		t.Fatalf("backoff = (%v,%v), want (1s,10s)", tk.BackoffBase, tk.BackoffMax)
	}
	if got, want := tk.Deadline, fixed.Add(30*time.Second); !got.Equal(want) {
		t.Fatalf("Deadline = %v, want %v", got, want)
	}
}

// TestCreatePlanDefaultsStayStrict guards the compatibility red line: a step
// that omits the new policy fields compiles to a strict, no-backoff,
// no-deadline task — identical to pre-0.3.3 behaviour.
func TestCreatePlanDefaultsStayStrict(t *testing.T) {
	fabric := taskfabric.NewFabric()
	kernel := NewKernel(nil, fabric, nil, nil)

	ctx := kctx.WithCallerID(context.Background(), "agent-A")
	res, err := kernel.CreatePlan(ctx, CreatePlanArgs{
		Steps: []PlanStepArgs{{ID: "s1", Capability: "ares/plan"}},
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	tk, err := fabric.Task(res.TaskIDs[0])
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if tk.AllowPartial {
		t.Fatal("default must stay strict (AllowPartial=false)")
	}
	if tk.BackoffBase != 0 || tk.BackoffMax != 0 {
		t.Fatalf("default must have no backoff, got (%v,%v)", tk.BackoffBase, tk.BackoffMax)
	}
	if !tk.Deadline.IsZero() {
		t.Fatalf("default must have no deadline, got %v", tk.Deadline)
	}
}

// TestCreatePlanClampsNegativeDurations guards against untrusted LLM output:
// a negative backoff/deadline must clamp to 0 (disabled), never produce a
// past NextAttemptAt or a negative deadline that behaves inconsistently.
func TestCreatePlanClampsNegativeDurations(t *testing.T) {
	fabric := taskfabric.NewFabric()
	kernel := NewKernel(nil, fabric, nil, nil)

	ctx := kctx.WithCallerID(context.Background(), "agent-A")
	res, err := kernel.CreatePlan(ctx, CreatePlanArgs{
		Steps: []PlanStepArgs{{
			ID:            "s1",
			Capability:    "ares/plan",
			BackoffBaseMS: -1000,
			BackoffMaxMS:  -5000,
			DeadlineMS:    -3000,
		}},
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	tk, err := fabric.Task(res.TaskIDs[0])
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if tk.BackoffBase != 0 || tk.BackoffMax != 0 {
		t.Fatalf("negative backoff must clamp to 0, got (%v,%v)", tk.BackoffBase, tk.BackoffMax)
	}
	if !tk.Deadline.IsZero() {
		t.Fatalf("negative deadline must clamp to no-deadline, got %v", tk.Deadline)
	}
}

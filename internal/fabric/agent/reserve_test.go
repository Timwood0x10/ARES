package agentfabric

import (
	"context"
	"testing"
	"time"
)

// TestAdmissibility_ReserveStopsWorkBeforeExhaustion pins the reserve gate: an
// agent with a 40% reserve stops being admissible once less than 40% of its
// budget is left, even though it could technically still afford one more
// quantum. That "could afford but must not" state is the point — the headroom
// is what lets the task it just handed back be finished by a fresher agent
// instead of dying with the near-empty one.
func TestAdmissibility_ReserveStopsWorkBeforeExhaustion(t *testing.T) {
	ctx := context.Background()
	f := NewFabric()
	if _, err := f.Spawn(ctx, SpawnSpec{
		Identity:   "reserved",
		Governance: Governance{TokenBudget: 100, MinRemainingRatio: 0.4},
	}); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	ok, reason, err := f.Admissibility("reserved")
	if err != nil || !ok {
		t.Fatalf("fresh agent: ok=%v reason=%s err=%v; want admissible", ok, reason, err)
	}

	// 50/100 spent: 50 remain, still above the 40-token reserve.
	if err := f.ConsumeResource("reserved", 50, 0); err != nil {
		t.Fatalf("consume 50: %v", err)
	}
	if ok, _, err := f.Admissibility("reserved"); err != nil || !ok {
		t.Fatalf("50%% remaining: must stay admissible (ok=%v err=%v)", ok, err)
	}

	// 70/100 spent: 30 remain, below the reserve.
	if err := f.ConsumeResource("reserved", 20, 0); err != nil {
		t.Fatalf("consume 20: %v", err)
	}
	// Precondition: the HARD budget would still afford a quantum (30 tokens of
	// headroom), so a denial here can only come from the reserve. Without this
	// the test would also pass if the reserve did nothing and the hard gate
	// happened to fire.
	if affordable, err := f.CheckResource("reserved", 1, 1); err != nil || !affordable {
		t.Fatalf("precondition: the hard budget must still afford a quantum (ok=%v err=%v)", affordable, err)
	}
	ok, reason, err = f.Admissibility("reserved")
	if err != nil || ok {
		t.Fatalf("30%% remaining: want inadmissible, got ok=%v err=%v", ok, err)
	}
	if reason != StopBudgetReserved {
		t.Fatalf("reason = %s, want %s", reason, StopBudgetReserved)
	}
}

// TestAdmissibility_DisabledReserveKeepsLegacyBehaviour is the backward-compat
// guard: with no reserve configured, only full exhaustion stops an agent, and
// the stop is attributed to exhaustion rather than to a reserve.
func TestAdmissibility_DisabledReserveKeepsLegacyBehaviour(t *testing.T) {
	ctx := context.Background()
	f := NewFabric()
	if _, err := f.Spawn(ctx, SpawnSpec{
		Identity:   "legacy",
		Governance: Governance{TokenBudget: 100},
	}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := f.ConsumeResource("legacy", 99, 0); err != nil {
		t.Fatalf("consume 99: %v", err)
	}
	if ok, reason, err := f.Admissibility("legacy"); err != nil || !ok {
		t.Fatalf("1 token left, no reserve: want admissible, got ok=%v reason=%s err=%v", ok, reason, err)
	}

	// The 100th token takes it to the floor: now only the hard gate fires.
	if err := f.ConsumeResource("legacy", 1, 0); err != nil {
		t.Fatalf("consume 1: %v", err)
	}
	ok, reason, err := f.Admissibility("legacy")
	if err != nil || ok {
		t.Fatalf("exhausted: want inadmissible, got ok=%v err=%v", ok, err)
	}
	if reason != StopBudgetExhausted {
		t.Fatalf("reason = %s, want %s", reason, StopBudgetExhausted)
	}
}

// TestAdmissibility_DeadlineOutranksBudget pins the precedence: an agent past
// its deadline is reported as deadline_exceeded even when its budget is also
// fine, so the operator is pointed at the real cause.
func TestAdmissibility_DeadlineOutranksBudget(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	f := NewFabric().WithClock(func() time.Time { return now })
	if _, err := f.Spawn(ctx, SpawnSpec{
		Identity:   "expired",
		Governance: Governance{TokenBudget: 100, Deadline: time.Minute},
	}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	now = now.Add(2 * time.Minute)

	ok, reason, err := f.Admissibility("expired")
	if err != nil || ok {
		t.Fatalf("past deadline: want inadmissible, got ok=%v err=%v", ok, err)
	}
	if reason != StopDeadlineExceeded {
		t.Fatalf("reason = %s, want %s", reason, StopDeadlineExceeded)
	}
}

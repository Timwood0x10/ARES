package kernel

import (
	"context"
	"testing"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestBudgetOK_TokenExhaustionStopsQuanta is the P0 gate regression: before
// this batch the token dimension never bound — consumeBudget passed token=0
// and the pre-quantum probe also passed token=0, so an agent could spend
// unbounded tokens. Now a quantum's real spend is recorded (clamped to the
// budget on overshoot) and the gate asks for one nominal token, so an
// exhausted agent is refused further quanta.
func TestBudgetOK_TokenExhaustionStopsQuanta(t *testing.T) {
	ctx := context.Background()

	agents := agentfabric.NewFabric()
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:     "bounded",
		Capabilities: []string{"code"},
		CognitionFactory: func([]string) agentfabric.Cognition {
			return &countingCognition{}
		},
		Governance: agentfabric.Governance{TokenBudget: 100},
	}); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	tracker := NewLoadTracker()
	sched := New(taskfabric.NewFabric(), map[string]CapabilityExecutor{}, tracker)
	sched.WithGovernance(agents)

	// Within budget: the agent is still affordable.
	sched.consumeBudget("bounded", 40)
	if !sched.budgetOK("bounded") {
		t.Fatal("40/100 tokens used: agent must remain affordable")
	}

	// A quantum spends past the budget: consumption clamps to 100 and the
	// gate must now refuse further work.
	sched.consumeBudget("bounded", 80)
	if sched.budgetOK("bounded") {
		t.Fatal("exhausted token budget (100/100) must block further quanta")
	}

	// The recorded usage is clamped to the budget, not left below it.
	tok, _, err := agents.BudgetUsage("bounded")
	if err != nil {
		t.Fatalf("budget usage: %v", err)
	}
	if tok != 100 {
		t.Fatalf("tokenUsed = %d, want clamped 100", tok)
	}
}

// TestBudgetOK_UnlimitedStaysOpen is the zero-budget backward-compat guard:
// an agent without a token budget is never blocked by the new token probe.
func TestBudgetOK_UnlimitedStaysOpen(t *testing.T) {
	ctx := context.Background()
	agents := agentfabric.NewFabric()
	if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
		Identity:     "unlimited",
		Capabilities: []string{"code"},
		CognitionFactory: func([]string) agentfabric.Cognition {
			return &countingCognition{}
		},
	}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	tracker := NewLoadTracker()
	sched := New(taskfabric.NewFabric(), map[string]CapabilityExecutor{}, tracker)
	sched.WithGovernance(agents)

	sched.consumeBudget("unlimited", 1_000_000)
	if !sched.budgetOK("unlimited") {
		t.Fatal("a zero-budget (unlimited) agent must never be blocked")
	}
}

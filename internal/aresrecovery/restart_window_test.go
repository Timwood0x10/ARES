package aresrecovery

import (
	"context"
	"errors"
	"testing"
	"time"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestRestartBudget_WindowGivesALongLivedAgentItsBudgetBack is the P2-6① contract.
//
// The budget exists to stop an agent that is CYCLING. Under lifetime accounting it also
// stopped an agent that had simply been alive for a long time — its fifth death, months
// apart, read exactly like a crash loop — and the refusal looked like governance while it
// was a bookkeeping artefact. With a window, only deaths inside it count.
func TestRestartBudget_WindowGivesALongLivedAgentItsBudgetBack(t *testing.T) {
	_, _, rec, _, now := newRecoveryHarness(t)
	ctx := context.Background()
	caps := []string{"rust"}

	// Exhaust the budget back-to-back: this IS a crash loop and must be refused.
	for i := 0; i < DefaultRestartPolicy().MaxRestarts; i++ {
		if _, err := rec.RestartAgent(ctx, "a1", agentfabric.CognitiveState{}, caps); err != nil {
			t.Fatalf("restart %d (inside the window) must be allowed: %v", i+1, err)
		}
	}
	if _, err := rec.RestartAgent(ctx, "a1", agentfabric.CognitiveState{}, caps); !errors.Is(err, ErrRecoveryExhausted) {
		t.Fatalf("a crash loop must still be refused, got %v", err)
	}

	// The agent survives well past the window: the same history is no longer a crash loop.
	*now = now.Add(DefaultRestartPolicy().RestartWindow + time.Minute)
	if got := rec.RestartCount("a1"); got != 0 {
		t.Fatalf("restarts older than the window must stop counting, got %d", got)
	}
	if _, err := rec.RestartAgent(ctx, "a1", agentfabric.CognitiveState{}, caps); err != nil {
		t.Fatalf("after the window the budget must be available again: %v", err)
	}
}

// TestRestartBudget_ZeroWindowKeepsLifetimeSemantics: the zero value is the pre-fix
// behaviour and must stay that way — a policy that never heard of the window keeps a
// lifetime budget, so nothing changes for a deployment that does not opt in.
func TestRestartBudget_ZeroWindowKeepsLifetimeSemantics(t *testing.T) {
	tasks := taskfabric.NewFabric()
	agents := agentfabric.NewFabric()
	now := time.Now()
	policy := DefaultRestartPolicy()
	policy.RestartWindow = 0

	rec := New(tasks, agents, policy).
		WithClock(func() time.Time { return now }).
		WithSleeper(func(context.Context, time.Duration) error { return nil })
	ctx := context.Background()
	caps := []string{"rust"}

	for i := 0; i < policy.MaxRestarts; i++ {
		if _, err := rec.RestartAgent(ctx, "a1", agentfabric.CognitiveState{}, caps); err != nil {
			t.Fatalf("restart %d: %v", i+1, err)
		}
	}

	// A year later: lifetime accounting still counts every death ever.
	now = now.Add(365 * 24 * time.Hour)
	if got := rec.RestartCount("a1"); got != policy.MaxRestarts {
		t.Fatalf("a zero window must keep counting lifetime restarts, got %d", got)
	}
	if _, err := rec.RestartAgent(ctx, "a1", agentfabric.CognitiveState{}, caps); !errors.Is(err, ErrRecoveryExhausted) {
		t.Fatalf("a zero window must keep the lifetime refusal, got %v", err)
	}
}

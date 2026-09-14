package sdk

import "testing"

// TestWithMaxTokensBridgesToGovernance locks the Phase 7 bridge:
// WithMaxTokens must reach the runtime's governance budget (the only token
// enforcement on the L2 path) instead of dying on the agentConfig. The
// pre-bridge behavior — stored but never read — silently left callers
// unbounded while they believed they had capped the run.
func TestWithMaxTokensBridgesToGovernance(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.gov.TokenBudget != 0 {
		t.Fatalf("fresh runtime governance = %+v, want zero budget", r.gov)
	}

	_ = r.NewAgent("capped", WithMaxTokens(5000))
	if r.gov.TokenBudget != 5000 {
		t.Fatalf("WithMaxTokens(5000) must bridge to gov.TokenBudget, got %d", r.gov.TokenBudget)
	}

	// First positive value wins: a later agent must not tighten (or loosen)
	// the budget that the earlier agent already established.
	_ = r.NewAgent("tighter", WithMaxTokens(100))
	if r.gov.TokenBudget != 5000 {
		t.Fatalf("later WithMaxTokens must not override the established budget, got %d", r.gov.TokenBudget)
	}

	// Zero/unset never bridges.
	r2, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = r2.NewAgent("unbounded", WithMaxTokens(0))
	if r2.gov.TokenBudget != 0 {
		t.Fatalf("WithMaxTokens(0) must stay unbounded, got %d", r2.gov.TokenBudget)
	}
}

// TestWithAgentGovernanceWinsOverBridge locks the precedence: an explicit
// runtime-level WithAgentGovernance is the authoritative budget, and a later
// agent-level WithMaxTokens must not override it.
func TestWithAgentGovernanceWinsOverBridge(t *testing.T) {
	r, err := New(WithAgentGovernance(3000, 10, 0))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.gov.TokenBudget != 3000 {
		t.Fatalf("WithAgentGovernance token budget = %d, want 3000", r.gov.TokenBudget)
	}
	_ = r.NewAgent("late", WithMaxTokens(9999))
	if r.gov.TokenBudget != 3000 {
		t.Fatalf("WithMaxTokens must not override WithAgentGovernance, got %d", r.gov.TokenBudget)
	}
}

// TestWithMaxTokensBridgeConcurrentWithL2Build locks the concurrency
// contract found in code review: NewAgent's bridge writes r.gov under
// govMu while ensureL2 (first Run) reads it — a concurrent bridge and
// first-run pair must be race-clean (run under -race).
func TestWithMaxTokensBridgeConcurrentWithL2Build(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.NewAgent("bridger", WithMaxTokens(1234))
	}()
	// A concurrent snapshot read models ensureL2's once-body racing the
	// bridge (the real ensureL2 needs an LLM; the snapshot is the shared
	// state under test).
	for i := 0; i < 100; i++ {
		_ = r.governanceSnapshot()
	}
	<-done
	if got := r.governanceSnapshot().TokenBudget; got != 1234 {
		t.Fatalf("bridged budget = %d, want 1234", got)
	}
}

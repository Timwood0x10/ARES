package aresrecovery

import "testing"

// TestDefaultRestartPolicy_ReclaimsIdleAgentsByDefault locks the decision that
// reclamation ships ON: the pool leak it fixes (defect A-factory3 / P1-6) is
// silent and unbounded, so a default that keeps every agent forever would keep
// leaking in every deployment that never heard of the knob. An operator turns it
// off by setting the window to zero.
func TestDefaultRestartPolicy_ReclaimsIdleAgentsByDefault(t *testing.T) {
	if got := DefaultRestartPolicy().IdleAgentTTL; got <= 0 {
		t.Fatalf("IdleAgentTTL = %v, want a positive default (reclamation ON); 0 disables it", got)
	}
}

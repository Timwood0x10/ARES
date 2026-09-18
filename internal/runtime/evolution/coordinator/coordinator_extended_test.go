package coordinator

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

func TestDecisionStringValues(t *testing.T) {
	tests := []struct {
		d    Decision
		want string
	}{
		{DecisionApply, "apply"},
		{DecisionReject, "reject"},
		{DecisionDelay, "delay"},
		{DecisionDrop, "drop"},
		{Decision(99), "unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.d.String(); got != tt.want {
			t.Errorf("Decision(%d).String() = %q, want %q", int(tt.d), got, tt.want)
		}
	}
}

func TestDefaultPolicyValues(t *testing.T) {
	p := DefaultPolicy()
	if p.AutoApplyThreshold != 8 {
		t.Errorf("AutoApplyThreshold = %d, want 8", p.AutoApplyThreshold)
	}
	if p.MaxPatchesPerMinute != 4 {
		t.Errorf("MaxPatchesPerMinute = %d, want 4", p.MaxPatchesPerMinute)
	}
	if p.MinFitnessThreshold != 30.0 {
		t.Errorf("MinFitnessThreshold = %v, want 30.0", p.MinFitnessThreshold)
	}
	if p.ApplyFitnessThreshold != 70.0 {
		t.Errorf("ApplyFitnessThreshold = %v, want 70.0", p.ApplyFitnessThreshold)
	}
	if p.SelfHealingEnabled {
		t.Error("SelfHealingEnabled should be false")
	}
	if p.SelfHealingMaxRetries != 3 {
		t.Errorf("SelfHealingMaxRetries = %d, want 3", p.SelfHealingMaxRetries)
	}
}

func TestNewCoordinatorConstructs(t *testing.T) {
	ec := NewEvolutionCoordinator(DefaultPolicy(), patch.NewRegistry())
	if ec == nil {
		t.Fatal("NewEvolutionCoordinator returned nil")
	}
}

func TestCoordinatorEvaluateNoProposals(t *testing.T) {
	ec := NewEvolutionCoordinator(DefaultPolicy(), patch.NewRegistry())
	ec.Evaluate(context.Background()) // void method, just verify no panic
	history := ec.DecisionHistory()
	if len(history) != 0 {
		t.Errorf("DecisionHistory len = %d, want 0 for no proposals", len(history))
	}
}

func TestCoordinatorSetDeployerNil(t *testing.T) {
	ec := NewEvolutionCoordinator(DefaultPolicy(), patch.NewRegistry())
	// SetDeployer(nil) must not panic — the coordinator falls back to
	// direct apply via the patch registry when no deployer is set.
	ec.SetDeployer(nil)
	// Verify the coordinator is still functional after nil deployer.
	ec.Evaluate(context.Background()) // void method, verify no panic
	history := ec.DecisionHistory()
	if len(history) != 0 {
		t.Errorf("DecisionHistory len = %d, want 0", len(history))
	}
}

func TestCoordinatorApplyEmergencyNoExecutor(t *testing.T) {
	ec := NewEvolutionCoordinator(DefaultPolicy(), patch.NewRegistry())
	err := ec.ApplyEmergency(context.Background(), patch.RuntimePatch{
		Type:   patch.PatchChangeBudget,
		Target: "nonexistent",
	})
	if err == nil {
		t.Error("expected error for unregistered target")
	}
}

func TestPatchSourceConstantsNonEmpty(t *testing.T) {
	sources := map[PatchSource]string{
		SourceGA:        "genome",
		SourceChaos:     "chaos",
		SourceAKF:       "akf",
		SourceHuman:     "human",
		SourceLLM:       "llm",
		SourceK8s:       "k8s",
		SourceRule:      "rule",
		SourceCandidate: "candidate",
	}
	seen := make(map[string]bool)
	for src, val := range sources {
		if string(src) == "" {
			t.Errorf("empty PatchSource for %q", val)
		}
		if seen[val] {
			t.Errorf("duplicate PatchSource value: %q", val)
		}
		seen[val] = true
	}
}

func TestPatchProposalFieldsAccessible(t *testing.T) {
	p := PatchProposal{
		Patch:      patch.RuntimePatch{Type: patch.PatchChangeBudget, Target: "test"},
		Source:     SourceGA,
		Reason:     "test",
		Priority:   5,
		Fitness:    80.0,
		RetryCount: 1,
	}
	if p.Source != SourceGA {
		t.Errorf("Source = %q, want %q", p.Source, SourceGA)
	}
	if p.Fitness != 80.0 {
		t.Errorf("Fitness = %v, want 80.0", p.Fitness)
	}
}

func TestHealingAttemptFieldsAccessible(t *testing.T) {
	h := HealingAttempt{
		Target:    "t",
		PatchType: "pt",
		Attempt:   1,
		Success:   true,
	}
	if !h.Success {
		t.Error("Success should be true")
	}
}

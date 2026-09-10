package evolution

import (
	"context"
	"testing"
)

// TestNewFailClosedGuardrails_AlwaysBlocks locks the fail-closed contract
// (REVIEW 2.4#24): a guardrail substituted for a failed construction must
// block EVERY check unconditionally. The previous implementation was just a
// normal guardrail with aggressive thresholds — a healthy-looking first
// cycle (no stagnation yet, no baseline regression) sailed through, which is
// fail-OPEN: the safety net only engaged after the damage pattern it was
// meant to prevent had already accumulated.
func TestNewFailClosedGuardrails_AlwaysBlocks(t *testing.T) {
	g := NewFailClosedGuardrails()
	ctx := context.Background()

	// PreEvolveCheck blocks immediately with the construction-failure code —
	// no population signal required (fully evaluated, healthy best score).
	pre := g.PreEvolveCheck(ctx, 0.95, 1, 100, 0)
	if !pre.ShouldStop {
		t.Fatal("fail-closed guardrail let a healthy PreEvolveCheck pass")
	}
	if len(pre.Events) == 0 || pre.Events[0].ErrorCode != failClosedCode {
		t.Errorf("expected %s event, got %+v", failClosedCode, pre.Events)
	}

	// PostEvolveCheck blocks too, even on a clear improvement.
	post := g.PostEvolveCheck(ctx, 0.99, 1, nil)
	if !post.ShouldStop {
		t.Fatal("fail-closed guardrail let a healthy PostEvolveCheck pass")
	}
	if len(post.Events) == 0 || post.Events[0].ErrorCode != failClosedCode {
		t.Errorf("expected %s event, got %+v", failClosedCode, post.Events)
	}

	// ValidateToolSet rejects even a valid tool set.
	val := g.ValidateToolSet(1, []string{"web_search"})
	if !val.ShouldStop {
		t.Fatal("fail-closed guardrail accepted a tool set")
	}

	// Reset must NOT reopen the net: watch loops call Reset, and a
	// construction failure is only recoverable by reconfiguration.
	g.Reset()
	after := g.PreEvolveCheck(ctx, 0.95, 2, 100, 0)
	if !after.ShouldStop {
		t.Fatal("Reset reopened the fail-closed guardrail")
	}
}

// TestNewEvolutionGuardrails_ValidatesOptions locks the constructor
// validation that makes the fail-closed err-branch reachable: previously
// NewEvolutionGuardrails never returned an error, so the bootstrap fallback
// to NewFailClosedGuardrails was dead code — a misconfigured guardrail was
// silently "fixed" into defaults instead of failing closed.
func TestNewEvolutionGuardrails_ValidatesOptions(t *testing.T) {
	cases := []struct {
		name string
		opt  GuardrailOption
	}{
		{"negative stagnation generations", WithMaxStagnantGenerations(-1)},
		{"lineage share below range", WithMaxLineageShare(-0.1)},
		{"lineage share above range", WithMaxLineageShare(1.5)},
		{"negative max events", func(g *EvolutionGuardrails) { g.MaxEvents = -5 }},
		{"negative max tools", WithMaxToolsEnabled(-1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := NewEvolutionGuardrails(tc.opt)
			if err == nil {
				t.Fatalf("expected validation error, got guardrail %+v", g)
			}
		})
	}

	// Boundary values that mean "disabled", not misconfigured, stay valid.
	for _, tc := range []struct {
		name string
		opt  GuardrailOption
	}{
		{"zero stagnation disables", WithMaxStagnantGenerations(0)},
		{"zero lineage share disables", WithMaxLineageShare(0)},
		{"lineage share at one", WithMaxLineageShare(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewEvolutionGuardrails(tc.opt); err != nil {
				t.Fatalf("valid option rejected: %v", err)
			}
		})
	}
}

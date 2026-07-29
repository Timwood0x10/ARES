package sdk

import (
	"testing"

	ares_bootstrap "github.com/Timwood0x10/ares/internal/ares_bootstrap"
)

// TestEvolutionHotUpdateWiring verifies the evolution hot-update wiring
// contract: evoComponents is populated only when BOTH evolution and knowledge
// are enabled (the wiring needs a live KnowledgeRuntime), and ProvideNewEvolution
// tolerates nil arguments so the SDK's New() can call it directly.
//
// The wiring logic lives in two places: ProvideNewEvolution (constructs the
// components from a KnowledgeRuntime) and New() (gates the call on both
// cfg.evoCfg.Enabled and kw.rt != nil). This test exercises both layers.
func TestEvolutionHotUpdateWiring(t *testing.T) {
	t.Run("nil_runtime_skipped_provide_new_evolution", func(t *testing.T) {
		// ProvideNewEvolution must tolerate all-nil args: the knowledge genome
		// registers with a no-op executor when no runtime is supplied. This is
		// the path New() would take if it ever called ProvideNewEvolution
		// without a live runtime, and it is the contract the SDK relies on to
		// keep bootstrap non-fatal.
		comps, err := ares_bootstrap.ProvideNewEvolution(nil, nil, nil)
		if err != nil {
			t.Fatalf("ProvideNewEvolution(nil,nil,nil) error: %v", err)
		}
		if comps == nil {
			t.Fatal("expected non-nil components even with nil runtime")
		}
		if comps.PatchReg == nil {
			t.Error("expected non-nil PatchReg; knowledge patches must still register")
		}
		if comps.Coordinator == nil {
			t.Error("expected non-nil Coordinator")
		}
	})

	t.Run("wired_with_real_knowledge_runtime", func(t *testing.T) {
		// ProvideNewEvolution with a real KnowledgeRuntime wires the
		// KnowledgePatchExecutor against the live runtime so evolution patches
		// can mutate knowledge config. This is the focused equivalent of the
		// SDK's New() hot-update path without paying for LLM construction.
		rt := newTestKnowledgeRuntime()
		comps, err := ares_bootstrap.ProvideNewEvolution(nil, rt, nil)
		if err != nil {
			t.Fatalf("ProvideNewEvolution with runtime error: %v", err)
		}
		if comps == nil {
			t.Fatal("expected non-nil components")
		}
		if comps.PatchReg == nil {
			t.Error("expected non-nil PatchReg")
		}
		if comps.Coordinator == nil {
			t.Error("expected non-nil Coordinator")
		}
	})

	// The remaining cases exercise the SDK's New() gating logic end-to-end.
	// They use WithOllama (no connection until used) so no live LLM is needed.
	tests := []struct {
		name              string
		evoEnabled        bool
		knowledgeEnabled  bool
		wantEvoComponents bool
	}{
		{
			// Evolution off → evoComponents must be nil regardless of knowledge.
			name:              "disabled_when_evolution_off",
			evoEnabled:        false,
			knowledgeEnabled:  true,
			wantEvoComponents: false,
		},
		{
			// Knowledge off → kw.rt is nil → evoComponents must be nil even
			// when evolution is on (the wiring requires a live runtime).
			name:              "disabled_when_knowledge_off",
			evoEnabled:        true,
			knowledgeEnabled:  false,
			wantEvoComponents: false,
		},
		{
			// Both on → evoComponents must be non-nil. This is the happy path
			// the SDK advertises: evolution patches affect the running engine.
			name:              "wired_when_both_enabled",
			evoEnabled:        true,
			knowledgeEnabled:  true,
			wantEvoComponents: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []Option{WithOllama("llama3.2"), WithTrace(false)}
			if tt.evoEnabled {
				opts = append(opts, WithEvolution())
			}
			if tt.knowledgeEnabled {
				opts = append(opts, WithKnowledge())
			}
			rt, err := New(opts...)
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}
			defer rt.Close()

			got := rt.evoComponents != nil
			if got != tt.wantEvoComponents {
				t.Errorf("evoComponents non-nil = %v, want %v", got, tt.wantEvoComponents)
			}
		})
	}
}

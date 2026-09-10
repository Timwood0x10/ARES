// regression_gate_wiring.go builds the PRODUCTION arena regression gate from
// the evolution gate config: the eval LLM client drives an
// LLMArenaScorer, and the eval suite file supplies the preserved-case list
// (the same suite the G3 eval gate scores in absolute terms — the regression
// gate re-runs it as a candidate-vs-active A/B).
//
// Configuration contract (evolution.gates.*):
//   - regression_enabled unset/false → NO gate (opt-in: each Check costs
//     2×regression_runs LLM scoring rounds);
//   - regression_enabled true but no eval_suite / no eval LLM client →
//     Bootstrap FAILS: an explicitly armed gate must not silently skip
//     (fail closed, same posture as eval_strict).
package ares_bootstrap

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_config"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	evoService "github.com/Timwood0x10/ares/internal/runtime/ares_evolution/service"
	"github.com/Timwood0x10/ares/internal/runtime/eval"
)

// errRegressionGateNotConfigured signals the INTENTIONAL absence of the
// regression gate (regression_enabled=false). The caller tolerates exactly
// this error and skips wiring; any other error from buildRegressionGate
// means an ARMED gate is broken and fails bootstrap.
var errRegressionGateNotConfigured = errors.New("bootstrap: arena regression gate not configured")

// buildRegressionGate constructs the arena regression gate.
//
// M-G2 tri-state semantics (2026-09-10): the gate defaults to AUTO-ARMED —
// when eval_suite and the eval LLM client both exist, the gate is wired
// with no explicit opt-in (a promote chain that has the infrastructure for
// regression checking must use it). The explicit knobs:
//   - regression_enabled: false — documented opt-out (the caller Warn-logs);
//   - regression_enabled: true — armed AND missing prerequisites become
//     bootstrap errors (fail closed, same as an armed G3).
//
// In the auto path (nil), missing prerequisites are honest absence (no
// suite = nothing to regress against) and return the not-configured
// sentinel.
func buildRegressionGate(
	enabled *bool,
	client eval.LLMClient,
	gates ares_config.EvolutionGateConfig,
) (*evolution.ArenaRegressionGate, error) {
	// Explicit opt-out: the caller logs the Warn.
	if enabled != nil && !*enabled {
		return nil, errRegressionGateNotConfigured
	}
	// Auto path with no suite: honest absence.
	if strings.TrimSpace(gates.EvalSuite) == "" {
		if enabled != nil && *enabled {
			return nil, fmt.Errorf("bootstrap: regression gate enabled (evolution.gates.regression_enabled) but no eval_suite is configured — the gate needs the preserved-case suite")
		}
		return nil, errRegressionGateNotConfigured
	}
	if client == nil {
		if enabled != nil && *enabled {
			return nil, fmt.Errorf("bootstrap: regression gate enabled (evolution.gates.regression_enabled) but no eval LLM client is wired")
		}
		// Auto path, suite present but no LLM client: the infrastructure is
		// half-wired. Degrade loudly rather than arming a gate that can only
		// fail closed on every candidate.
		return nil, errRegressionGateNotConfigured
	}
	suite, err := eval.NewLoader().Load(gates.EvalSuite)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: load regression suite %q: %w", gates.EvalSuite, err)
	}
	cases := make([]any, 0, len(suite.TestCases))
	for _, tc := range suite.TestCases {
		if strings.TrimSpace(tc.Input) != "" {
			cases = append(cases, tc.Input)
		}
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("bootstrap: regression suite %q contains no usable cases (empty inputs)", gates.EvalSuite)
	}
	scorer, err := evoService.NewLLMArenaScorer(evoService.LLMArenaScorerConfig{Client: client})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: build arena scorer: %w", err)
	}
	gate, err := evolution.NewArenaRegressionGate(scorer, cases, evolution.ArenaRegressionGateConfig{
		Runs:       gates.RegressionRuns,
		MinWinRate: gates.RegressionMinWinRate,
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: build arena regression gate: %w", err)
	}
	return gate, nil
}

// regression_gate_wiring.go builds the PRODUCTION arena regression gate from
// the evolution gate config: the eval LLM client drives an
// LLMArenaScorer, and the eval suite file supplies the preserved-case list
// (the same suite the G3 eval gate scores in absolute terms — the regression
// gate re-runs it as a candidate-vs-active A/B).
//
// Configuration contract (evolution.gates.regression_enabled, tri-state):
//   - unset (nil, default): AUTO-ARM — the gate arms whenever eval_suite +
//     the eval LLM client exist (each Check costs 2×regression_runs LLM
//     scoring rounds); missing prerequisites are honest absence (sentinel,
//     no gate wired);
//   - false: explicit opt-out — NO gate (the caller Warn-logs);
//   - true: armed AND missing prerequisites become bootstrap errors
//     (fail closed, same posture as eval_strict).
package ares_bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_config"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	evoService "github.com/Timwood0x10/ares/internal/runtime/ares_evolution/service"
	"github.com/Timwood0x10/ares/internal/runtime/eval"
)

// errRegressionGateNotConfigured signals the INTENTIONAL absence of the
// regression gate (explicit regression_enabled=false, or auto mode without
// the suite/client prerequisites). The caller tolerates exactly this error
// and skips wiring; any other error from buildRegressionGate means an ARMED
// gate is broken and fails bootstrap.
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
// regressionGateConfig is the gate's construction input.
//
// A struct rather than positional parameters because the fourth input is OPTIONAL: the
// candidate-replay factory only exists when the process chose to supply it, and a chain of
// positional nils at the call site is how "optional" turns into "nobody remembers which
// argument was which".
type regressionGateConfig struct {
	// Enabled is evolution.gates.regression_enabled (nil = auto).
	Enabled *bool
	// Client is the eval LLM client: the grader, on both execution paths.
	Client eval.LLMClient
	// Gates is the evolution gate block (suite, runs, runtime-replay flag).
	Gates ares_config.EvolutionGateConfig
	// ReplayProvider, when non-nil, is what makes evolution.gates.regression_runtime_replay
	// satisfiable: candidates are then RUN through real per-candidate runtimes instead of
	// role-played by the model.
	//
	// It is a PROVIDER rather than a factory because of a boot-ordering fact: the factory
	// needs the serving tool binder, which cmd/ares wires AFTER the evolution components
	// exist (components at serve.go:108, toolchain at :141). A value handed over at boot
	// would therefore always be nil. The provider is called on first case execution
	// instead, by which time the process is fully wired — and an enabled flag with no
	// provider at all is still refused here, loudly, rather than silently downgraded.
	ReplayProvider func() (*CandidateReplayFactory, error)
}

func buildRegressionGate(cfg regressionGateConfig) (*evolution.ArenaRegressionGate, error) {
	enabled, client, gates := cfg.Enabled, cfg.Client, cfg.Gates
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
	// Cases carry their ground truth into the arena. Passing the bare input (as this
	// wiring used to) discarded the declared checks at exactly the point where they
	// matter: the paired A/B compares two candidates on LLM opinion, so "both answered
	// this factual case wrong and the grader liked both" read as a tie and a candidate
	// that lost the facts could pass. ArenaCase.String() renders only the task text, so
	// the prompts are unchanged.
	cases := make([]any, 0, len(suite.TestCases))
	anchored := 0
	for _, tc := range suite.TestCases {
		if strings.TrimSpace(tc.Input) == "" {
			continue
		}
		if len(tc.Checks) > 0 {
			anchored++
		}
		cases = append(cases, evoService.ArenaCase{
			Input:          tc.Input,
			ExpectedOutput: tc.ExpectedOutput,
			Checks:         tc.Checks,
		})
	}
	if anchored == 0 {
		log.Warn("bootstrap: arena regression suite declares no deterministic checks — candidate vs active is compared on grader opinion alone",
			"suite", gates.EvalSuite, "cases", len(cases))
	} else {
		log.Info("bootstrap: arena regression suite is anchored by deterministic checks",
			"suite", gates.EvalSuite, "anchored_cases", anchored, "cases", len(cases))
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("bootstrap: regression suite %q contains no usable cases (empty inputs)", gates.EvalSuite)
	}
	scorerCfg := evoService.LLMArenaScorerConfig{Client: client}
	var scorer *evoService.LLMArenaScorer
	if gates.RegressionRuntimeReplay {
		if cfg.ReplayProvider == nil {
			// Fail closed, and say WHY it cannot be fixed here: the missing piece is the
			// serving tool binder, which only cmd/ares has. Silently falling back to the
			// LLM scorer would make the flag read as "we are replaying" while nothing was.
			return nil, fmt.Errorf("bootstrap: evolution.gates.regression_runtime_replay is enabled but no candidate-runtime factory provider was supplied — the factory needs the serving tool binder, which cmd/ares wires after bootstrap (see RegressionRuntimeReplay)")
		}
		exec, err := NewRuntimeArenaExecutor(context.Background(), cfg.ReplayProvider, nil)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: build runtime arena executor: %w", err)
		}
		scorer, err = evoService.NewRuntimeBackedArenaScorer(scorerCfg, exec)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: build runtime-backed arena scorer: %w", err)
		}
		log.Info("bootstrap: arena regression RUNS candidates through real per-candidate runtimes",
			"cost", "one real execution per case per candidate (a runtime cannot answer N cases in one call) plus one grading call per case — an order of magnitude above the role-play scorer's two calls",
			"tool_boundary", "each candidate is sandboxed to the evaluation ceiling, narrowed by the candidate's own Params",
			"retained_runtimes", 2,
			"lifecycle", "a runtime is kept only while its candidate is being compared; older ones are closed as the comparison moves on")
	} else {
		scorer, err = evoService.NewLLMArenaScorer(scorerCfg)
		if err != nil {
			return nil, fmt.Errorf("bootstrap: build arena scorer: %w", err)
		}
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

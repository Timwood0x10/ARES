// eval_gate_wiring.go builds the PRODUCTION G3 eval-suite gate: an
// AgentTestRunner whose executor runs regression test cases THROUGH the
// candidate strategy (its prompt template), scored by the registered LLM
// evaluators (llm_judge). The gate used to be
// constructed with a nil runner and an empty suite, making it a permanent
// pass-through while the docs claimed a four-gate pipeline.
//
// Configuration contract (evolution.gates.eval_suite, a file path;
// evolution.gates.eval_strict):
//   - unset → NO G3 gate is built (honest absence, not a fake pass-through);
//     the pipeline degrades to G2 shadow (+ G1 guardrails at the scheduler).
//     With eval_strict the absence itself FAILS bootstrap — an unwired
//     gate must not silently pass every candidate.
//   - set but unloadable → Bootstrap FAILS. An operator who configured a
//     verification gate must not get a silently weaker pipeline from a typo.
package ares_bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/runtime/eval"
)

// errEvalGateNotConfigured signals the INTENTIONAL absence of the G3 gate
// (no registry/client/suite configured and strict mode off). Bootstrap
// tolerates it and runs the pipeline without G3; any other error from
// buildEvalGate means a CONFIGURED gate is broken and fails bootstrap.
var errEvalGateNotConfigured = errors.New("bootstrap: G3 eval gate not configured")

// llmEvalExecutor adapts an eval.LLMClient into an eval.AgentExecutor
// for the G3 gate. The candidate strategy's PromptTemplate is prepended to
// every test-case input, so candidate and active strategies produce genuinely
// different outputs and the judge scores discriminate between them.
//
// Sampling params (temperature/max_tokens) are NOT applied: the bare
// LLMClient.Generate interface carries no sampling options. The prompt
// template is the artifact the GA actually mutates, so it is the honest
// discriminator here. Thread-safe: lifecycle Submit can run concurrently.
type llmEvalExecutor struct {
	client eval.LLMClient

	mu             sync.Mutex
	promptTemplate string
}

func newLLMEvalExecutor(client eval.LLMClient) *llmEvalExecutor {
	return &llmEvalExecutor{client: client}
}

// setCandidate records the candidate whose prompt template the next Execute
// calls run through. Invoked by the gate's beforeRun hook per Submit.
func (e *llmEvalExecutor) setCandidate(cand *mutation.Strategy) {
	if cand == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.promptTemplate = cand.PromptTemplate
}

// Execute implements eval.AgentExecutor.
func (e *llmEvalExecutor) Execute(ctx context.Context, input string) (string, []string, int, error) {
	e.mu.Lock()
	tmpl := e.promptTemplate
	e.mu.Unlock()

	prompt := input
	if tmpl != "" {
		prompt = strings.TrimSpace(tmpl) + "\n\n" + input
	}
	out, err := e.client.Generate(ctx, prompt)
	if err != nil {
		return "", nil, 0, fmt.Errorf("eval executor generate: %w", err)
	}
	return out, nil, 0, nil
}

// buildEvalGate constructs the G3 verify gate from the evaluator registry,
// the eval LLM client, and the YAML-configured suite path.
//
// Semantics (M-G1, 2026-09-10 — defaults aligned with G2/G4's fail-closed
// posture):
//   - No suite configured (eval_suite empty): the gate is intentionally
//     absent — bootstrap logs a loud Warn ("promote gate chain degrades to
//     G1+G2") and runs without G3. An operator who wants absence itself to
//     fail sets evolution.gates.eval_strict.
//   - Suite configured but infrastructure missing (no registry / no LLM
//     client) or unloadable: bootstrap FAILS — an ARMED gate must never
//     silently degrade (this is the same fail-closed posture the arena
//     regression gate and eval_strict have).
//   - Suite configured and loadable: the gate is ALWAYS runtime-strict
//     (infrastructure gaps were rejected at bootstrap; a runtime loss —
//     e.g. evaluators producing no results — must reject, never pass).
//
// eval_strict therefore governs ONLY the absence case (no suite configured
// → fatal vs. degraded-with-Warn); it does NOT relax a built gate.
// defaultEvalSuitePath is the suite the gates use when configuration names none.
//
// It is a REPOSITORY path: present in development and in a source checkout, usually
// absent in a deployed binary. That is exactly why a DEFAULTED suite degrades to "no
// gate" instead of failing startup (see buildEvalGate). Defaulting it at all is
// deliberate — otherwise G3 is never armed unless an operator already knows the knob
// exists, which is how a pipeline silently degrades to G1+G2.
const defaultEvalSuitePath = "test/eval/golden.yaml"

// resolveEvalSuite picks the suite path for both gates and reports whether it came from
// the default rather than from configuration.
//
// The existence check belongs here rather than in the loader, because it decides whether
// anything is armed at all: a default path that is not present means "no suite" — and
// therefore no arena gate either — not an error.
func resolveEvalSuite(configured string) (path string, defaulted bool) {
	if strings.TrimSpace(configured) != "" {
		// An explicit value is used even when it does not exist: a typo must fail
		// bootstrap loudly rather than be silently replaced by the default.
		return configured, false
	}
	if _, statErr := os.Stat(defaultEvalSuitePath); statErr != nil {
		// Not a warning: a deployment without the repository's corpus is the normal
		// case, and the caller already warns that G3 is absent.
		return "", false
	}
	return defaultEvalSuitePath, true
}

// evalGateConfig is everything the G3 gate is built from.
//
// A struct rather than five positional parameters: two of them (a path and a mode flag)
// read alike at a call site, and the list had grown past the parameter limit.
type evalGateConfig struct {
	// SuitePath is the suite to load; empty means "no suite".
	SuitePath string
	// MinScore overrides the gate's own floor when > 0.
	MinScore float64
	// Strict makes an ABSENT suite fatal (it governs only the absence case).
	Strict bool
	// SuiteDefaulted marks SuitePath as the built-in default rather than configuration.
	//
	// It changes how a MISSING piece degrades, because the two cases are not the same
	// promise: an operator who configured a suite asked for a gate and must not get a
	// silently weaker pipeline (typo, missing API key), while a default nobody asked for
	// must never brick startup.
	SuiteDefaulted bool
}

func buildEvalGate(
	registry *eval.EvaluatorRegistry,
	client eval.LLMClient,
	cfg evalGateConfig,
) (*evolution.EvalGate, error) {
	if strings.TrimSpace(cfg.SuitePath) == "" {
		// No suite: intentional absence. The gate chain degrades to G1+G2 —
		// loudly. Only eval_strict turns the absence itself into a bootstrap
		// failure.
		if cfg.Strict {
			// Deliberately NOT wrapping the sentinel: the caller only
			// tolerates errEvalGateNotConfigured, so strict absence must
			// surface as a hard error and fail bootstrap.
			return nil, fmt.Errorf("bootstrap: G3 eval gate is strict (evolution.gates.eval_strict) but no eval_suite is configured")
		}
		return nil, errEvalGateNotConfigured
	}
	// Suite present but infrastructure missing. An EXPLICITLY configured gate must never
	// silently degrade — fail bootstrap (M-G1 fail-closed alignment). The DEFAULT suite,
	// by contrast, is a convenience: a deployment without an eval LLM client must still
	// start, just without G3.
	if registry == nil || client == nil {
		if cfg.SuiteDefaulted {
			log.Warn("bootstrap: default eval suite present but the eval infrastructure is missing — running WITHOUT the G3 gate (set evolution.gates.eval_suite to arm it explicitly, or wire the eval LLM client)",
				"suite", cfg.SuitePath)
			return nil, errEvalGateNotConfigured
		}
		return nil, fmt.Errorf("bootstrap: G3 eval gate armed (eval_suite %q) but eval infrastructure is missing (registry/client) — refusing to run with a silently degraded gate chain", cfg.SuitePath)
	}
	suite, err := eval.NewLoader().Load(cfg.SuitePath)
	if err != nil {
		if cfg.SuiteDefaulted {
			// The default lives in the repository, so a deployed binary may simply not
			// have it. Bricking startup over a file nobody asked for would make the
			// default a liability.
			log.Warn("bootstrap: the default eval suite could not be loaded — running WITHOUT the G3 gate",
				"suite", cfg.SuitePath, "error", err)
			return nil, errEvalGateNotConfigured
		}
		return nil, fmt.Errorf("bootstrap: load eval suite %q (evolution.gates.eval_suite): %w", cfg.SuitePath, err)
	}
	if len(suite.TestCases) == 0 {
		if cfg.SuiteDefaulted {
			log.Warn("bootstrap: the default eval suite declares no test cases — running WITHOUT the G3 gate",
				"suite", cfg.SuitePath)
			return nil, errEvalGateNotConfigured
		}
		return nil, fmt.Errorf("bootstrap: eval suite %q contains no test cases", cfg.SuitePath)
	}

	// Say up front how much of this gate's verdict is VERIFIABLE (P0-1(ii)). A suite
	// with declared checks floors the judge on the cases it fails; a suite without
	// them is scored by the judge alone, and an operator reading gate verdicts should
	// know which of the two they configured — the scores look the same either way.
	anchored := eval.CountAnchoredCases(*suite)
	if anchored == 0 {
		log.Warn("bootstrap: G3 eval suite declares no deterministic checks — the gate's accuracy is judge opinion only",
			"suite", cfg.SuitePath, "cases", len(suite.TestCases))
	} else {
		log.Info("bootstrap: G3 eval suite is anchored by deterministic checks",
			"suite", cfg.SuitePath, "anchored_cases", anchored, "cases", len(suite.TestCases),
			"from_default", cfg.SuiteDefaulted)
	}

	exec := newLLMEvalExecutor(client)
	runner, err := eval.NewAgentTestRunner(exec)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: create eval runner: %w", err)
	}
	runner.SetRegistry(registry)

	runtimeCfg := evolution.DefaultEvalGateConfig()
	if cfg.MinScore > 0 {
		runtimeCfg.MinScore = cfg.MinScore
	}
	// State the ABSOLUTE bar out loud (P2-1).
	//
	// The gate rejects a candidate that REGRESSES against the active one (relative) and one
	// that scores below this floor (absolute) — and only the second catches a whole field
	// drifting down together, which is the concern behind "门只防回归，不防整体漂移". The
	// mechanism was wired but the number never appeared in a log, so an operator could not
	// tell which bar was in force, nor whether the 0.7 they were reading was their own
	// setting or the gate's built-in floor. Those are different promises.
	floorSource := "gate default (evolution.DefaultEvalGateConfig)"
	if cfg.MinScore > 0 {
		floorSource = "evolution.gates.eval_min_score"
	}
	log.Info("bootstrap: G3 eval gate absolute floor",
		"min_score", runtimeCfg.MinScore,
		"source", floorSource,
		"role", "a candidate below this is rejected however the others score — this is the bar that catches a whole field drifting down")
	// Runtime strictness is ALWAYS on for a built gate (M-G1): infrastructure
	// gaps were already rejected at bootstrap (armed-fail-closed above), so
	// the only remaining gap is a runtime loss (e.g. evaluators produce no
	// results) — which must reject, never pass. eval_strict governs ONLY the
	// absence case (no suite configured → fatal vs. degraded-with-Warn).
	runtimeCfg.StrictMode = true
	gate := evolution.NewEvalGate(registry, runner, *suite, runtimeCfg,
		evolution.WithEvalGateBeforeRun(exec.setCandidate),
	)
	return gate, nil
}

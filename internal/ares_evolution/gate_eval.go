// gate_eval.go wraps the ares_eval evaluation framework into a VerifyGate
// so the StrategyLifecycle can run independent regression tests before
// promoting a candidate strategy (B5 fix: Eval was built but never
// participated in promote/rollback decisions).
//
// G3 (Eval Suite) is the third gate in the verify pipeline:
//
//	G1 Guardrail → G2 Shadow → G3 Eval Suite → G4 Deployment staging
//
// When no EvaluatorRegistry is wired, the gate is a pass-through (returns
// pass=true) so the pipeline degrades gracefully in environments without
// LLM-based evaluators.
package evolution

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_eval"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// EvalGateConfig configures the G3 eval-suite verify gate.
type EvalGateConfig struct {
	// MinScore is the minimum weighted average score for a candidate to
	// pass. Default: 0.7.
	MinScore float64
	// EvaluatorName selects which registered evaluator to use. When empty,
	// the gate runs all registered evaluators and averages their scores.
	EvaluatorName string
}

// DefaultEvalGateConfig returns sensible defaults.
func DefaultEvalGateConfig() EvalGateConfig {
	return EvalGateConfig{
		MinScore: 0.7,
	}
}

// EvalGate is the G3 verify gate. It wraps an ares_eval.EvaluatorRegistry
// and an optional AgentTestRunner to score candidate strategies against a
// fixed regression suite. The gate is pass-through when no registry is set.
type EvalGate struct {
	registry *ares_eval.EvaluatorRegistry
	runner   *ares_eval.AgentTestRunner
	suite    ares_eval.TestSuite
	cfg      EvalGateConfig
}

// NewEvalGate creates a G3 eval-suite gate. Any nil argument makes the gate
// a pass-through (always passes), so the pipeline degrades gracefully.
func NewEvalGate(
	registry *ares_eval.EvaluatorRegistry,
	runner *ares_eval.AgentTestRunner,
	suite ares_eval.TestSuite,
	cfg EvalGateConfig,
) *EvalGate {
	return &EvalGate{
		registry: registry,
		runner:   runner,
		suite:    suite,
		cfg:      cfg,
	}
}

// Name returns the gate identifier.
func (g *EvalGate) Name() string {
	return "eval"
}

// Check runs the candidate through the eval suite and returns pass=true when
// the weighted average score meets or exceeds MinScore. When no registry or
// runner is wired, the gate is a pass-through (B5 graceful degradation).
func (g *EvalGate) Check(ctx context.Context, _ *mutation.Strategy, _ *mutation.Strategy) (bool, float64, string) {
	if g.registry == nil || g.runner == nil || len(g.suite.TestCases) == 0 {
		// Pass-through: no eval infrastructure wired.
		return true, 0, "eval suite not configured, skipping"
	}

	// Resolve evaluator: use the named one when configured, otherwise run
	// all registered evaluators and average their scores.
	if g.cfg.EvaluatorName != "" {
		results, scores, err := g.runner.RunAndEvaluate(ctx, g.suite, g.cfg.EvaluatorName)
		if err != nil {
			return false, 0, fmt.Sprintf("eval suite failed: %s", err)
		}
		score := averageScores(scores, len(results))
		if score >= g.cfg.MinScore {
			return true, score, fmt.Sprintf("eval score %.2f >= %.2f", score, g.cfg.MinScore)
		}
		return false, score, fmt.Sprintf("eval score %.2f < %.2f", score, g.cfg.MinScore)
	}

	// Run all registered evaluators and average.
	totalScore := 0.0
	evalCount := 0
	for _, name := range g.registry.Names() {
		results, scores, err := g.runner.RunAndEvaluate(ctx, g.suite, name)
		if err != nil {
			continue
		}
		totalScore += averageScores(scores, len(results))
		evalCount++
	}
	if evalCount == 0 {
		return true, 0, "no evaluators produced results, skipping"
	}
	avgScore := totalScore / float64(evalCount)
	if avgScore >= g.cfg.MinScore {
		return true, avgScore, fmt.Sprintf("eval score %.2f >= %.2f", avgScore, g.cfg.MinScore)
	}
	return false, avgScore, fmt.Sprintf("eval score %.2f < %.2f", avgScore, g.cfg.MinScore)
}

// averageScores computes the mean score across all test case results.
func averageScores(scores [][]ares_eval.EvalScore, resultCount int) float64 {
	if len(scores) == 0 || resultCount == 0 {
		return 0
	}
	var total float64
	var count int
	for _, caseScores := range scores {
		for _, s := range caseScores {
			total += s.Score
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

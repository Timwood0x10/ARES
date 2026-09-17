package evolution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/scoring"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/diff"
	evogenome "github.com/Timwood0x10/ares/internal/runtime/evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// buildActiveStrategyManager creates the active strategy manager.
func buildActiveStrategyManager(cfg SystemConfig) (*ActiveStrategyManager, error) {
	rpc := cfg.RollbackPolicyConfig
	var rbOpts []RollbackOption
	if rpc.DegradationThreshold > 0 {
		rbOpts = append(rbOpts, WithDegradationThreshold(rpc.DegradationThreshold))
	}
	if rpc.WindowSize > 0 {
		rbOpts = append(rbOpts, WithRollbackWindowSize(rpc.WindowSize))
	}
	if rpc.MinSamples > 0 {
		rbOpts = append(rbOpts, WithMinRollbackSamples(rpc.MinSamples))
	}
	rollbackPolicy := NewRollbackPolicy(rbOpts...)

	asmOpts := []ASMOption{}
	if cfg.Guardrails != nil {
		asmOpts = append(asmOpts, WithASMGuardrails(cfg.Guardrails))
	}
	return NewActiveStrategyManager(cfg.StrategyStore, rollbackPolicy, asmOpts...)
}

// buildShadowEvaluator creates the shadow evaluator with optional scorer.
func buildShadowEvaluator(cfg SystemConfig, tiered *scoring.TieredScorer, baseStrategy *mutation.Strategy) *ShadowEvaluator {
	// The tiered scorer caches by strategy hash for the whole generation, so the
	// FIRST comparison populates the cache and every later one is a cache hit
	// returning the identical score. That makes the scorer effectively
	// deterministic even with a temperature>0 LLM behind it, which the previous
	// code only flagged when an explicit seed was configured. Record it here so
	// the warning below reflects what actually happens.
	if tiered != nil && cfg.Scorer != nil {
		cfg.ShadowEvalConfig.DeterministicScorer = true
	}
	shadowEval := NewShadowEvaluator(cfg.ShadowEvalConfig)
	shadowEval.SetActiveStrategy(baseStrategy)
	// Shadow scoring is budget-gated ONLY when an LLM scorer is actually
	// wired (cfg.Scorer != nil ⇔ evolution.llm_scoring enabled). A raw
	// cfg.Scorer here would let every Submit's Prime run minSamples×2 LLM
	// calls with zero accounting against MaxLLMCallsPerGeneration;
	// TieredScorer instead enforces the budget
	// (TryRecordLLMCall), reuses the per-generation score cache, and falls
	// back to the heuristic when the budget is exhausted.
	//
	// Without an LLM scorer the tiered scorer is heuristic-only
	// (ConstantScorer 50): every comparison would be an exact tie, which is
	// meaningless evidence. With cfg.Scorer==nil we therefore do NOT wire the
	// tiered heuristic here. In zero-LLM mode the independent evidence source
	// is the DETERMINISTIC scorer: bootstrap sets
	// DeterministicScorerEnabled so the shadow gate registers, and the serve
	// layer
	// (cmd/ares/peer_mode.go) wires that scorer onto this evaluator once the
	// runtime ExecutionAttribution exists — the attribution is created after
	// NewWiredEvolutionSystem, so it cannot be injected through cfg here.
	// Until that runtime wiring, the scorer is UNSET and the sampler no-ops
	// (the shadow gate stays fail-closed); this also keeps the manual-RecordResult test
	// path (unit + closure) working.
	if cfg.Scorer != nil {
		if tiered != nil {
			shadowEval.SetShadowScorer(func(ctx context.Context, s *mutation.Strategy) float64 {
				score, _, err := tiered.Score(ctx, s)
				if err != nil {
					log.WarnContext(ctx, "shadow scorer failed, treating as score 0", "method", "buildShadowEvaluator", "strategy_id", s.ID, "error", err)
					return 0
				}
				return score
			})
		} else {
			scorer := cfg.Scorer
			shadowEval.SetShadowScorer(func(_ context.Context, s *mutation.Strategy) float64 {
				return scorer(s)
			})
		}
	}
	if cfg.ShadowEvalConfig.DeterministicScorer {
		log.Warn("shadow evaluator: scorer is deterministic — comparisons are identical, MinSamples is satisfied by repetition, not by independent evidence",
			"min_samples", cfg.ShadowEvalConfig.MinSamples,
			"reason", "per-generation score cache and/or fixed LLM seed",
		)
	}
	log.InfoContext(context.Background(), "shadow evaluation enabled", "method", "buildShadowEvaluator",
		"min_samples", cfg.ShadowEvalConfig.MinSamples,
		"min_win_rate", cfg.ShadowEvalConfig.MinWinRate,
		"active_strategy", baseStrategy.ID,
		"budget_gated", cfg.Scorer != nil && tiered != nil,
	)
	return shadowEval
}

// guidanceHintAdapter adapts an evolution.GuidanceProvider to mutation.HintProvider.
type guidanceHintAdapter struct {
	inner GuidanceProvider
}

func (a *guidanceHintAdapter) HintsForTask(ctx context.Context, taskType string, limit int) ([]mutation.EvolutionHint, error) {
	hints, err := a.inner.HintsForTask(ctx, taskType, limit)
	if err != nil {
		return nil, err
	}
	res := make([]mutation.EvolutionHint, len(hints))
	for i, h := range hints {
		res[i] = mutation.EvolutionHint{
			ID:                  h.ID,
			TaskType:            h.TaskType,
			Problem:             h.Problem,
			Solution:            h.Solution,
			Constraints:         h.Constraints,
			FailedPatterns:      h.FailedPatterns,
			PreferredTools:      h.PreferredTools,
			PromptSnippets:      h.PromptSnippets,
			ParamHints:          h.ParamHints,
			Confidence:          h.Confidence,
			SourceExperienceIDs: h.SourceExperienceIDs,
		}
	}
	return res, nil
}

func (a *guidanceHintAdapter) RecordStrategyOutcome(ctx context.Context, outcome mutation.StrategyOutcome) error {
	return a.inner.RecordStrategyOutcome(ctx, StrategyOutcome{
		StrategyID:    outcome.StrategyID,
		TaskType:      outcome.TaskType,
		Success:       outcome.Success,
		Score:         outcome.Score,
		Cost:          outcome.Cost,
		LatencyMs:     outcome.LatencyMs,
		MutationType:  outcome.MutationType,
		ExperienceIDs: outcome.ExperienceIDs,
		Timestamp:     outcome.Timestamp,
	})
}

// wrapGuidanceProvider wraps an evolution GuidanceProvider around a raw mutator
// using mutation.NewExperienceGuidedMutator.
func wrapGuidanceProvider(provider GuidanceProvider, raw *mutation.Mutator) genome.MutatorInterface {
	adaptedProvider := &guidanceHintAdapter{inner: provider}
	guided, err := mutation.NewExperienceGuidedMutator(raw, adaptedProvider)
	if err != nil {
		log.WarnContext(context.Background(), "failed to create ExperienceGuidedMutator, falling back to raw mutator", "method", "wrapGuidanceProvider",
			"error", err)
		return raw
	}
	log.InfoContext(context.Background(), "experience-guided mutation enabled", "method", "wrapGuidanceProvider",
		"provider", fmt.Sprintf("%T", provider),
	)
	return guided
}

// RunIdleEvolution runs N generations of idle evolution on the wired system.
func RunIdleEvolution(ctx context.Context, system *WiredEvolutionSystem, n int) error {
	if system == nil || system.PopAdapter == nil || system.Population == nil {
		return errors.New("system, pop adapter, and population must not be nil")
	}

	for gen := 0; gen < n; gen++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Capture parent snapshot BEFORE evolving so lineage can reference
		// pre-evolution agent scores for ScoreImprovement computation.
		var parentSnapshot []*mutation.Strategy
		if system.Genealogy != nil {
			parentSnapshot, _ = system.Population.Snapshot()
		}

		if err := system.PopAdapter.Run(ctx); err != nil {
			log.WarnContext(ctx, "generation produced guardrail warning, continuing", "method", "RunIdleEvolution", "generation", system.Population.Generation,
				"run_iteration", gen,
				"error", err,
			)
		}

		if system.Genealogy != nil {
			_, err := RecordPopulationLineage(ctx, system.Population, system.Genealogy, parentSnapshot, gen)
			if err != nil {
				log.WarnContext(ctx, "failed to record lineage", "method", "RunIdleEvolution", "generation", system.Population.Generation,
					"run_iteration", gen,
					"error", err,
				)
			}
		}

		// Run reflection cycle to analyze evolution patterns.
		if system.Reflector != nil && system.HypothesisGen != nil {
			history := system.Population.History()
			if len(history) > 0 {
				agents, _ := system.Population.Snapshot()
				ref, err := system.Reflector.Reflect(ctx, history, agents)
				if err != nil {
					log.WarnContext(ctx, "reflection failed, skipping", "method", "RunIdleEvolution", "generation", system.Population.Generation,
						"run_iteration", gen,
						"error", err,
					)
				} else if ref != nil && len(ref.Recommendations) > 0 {
					hyps := system.HypothesisGen.Generate(ctx, ref)
					if len(hyps) > 0 {
						log.InfoContext(ctx, "generated hypotheses from reflection", "method", "RunIdleEvolution", "generation", system.Population.Generation,
							"run_iteration", gen,
							"count", len(hyps),
						)
					}
				}
			}
		}

		// Apply meta-controller tuning to self-adapt evolution hyperparameters.
		if system.MetaCtrl != nil {
			genome.ApplyMetaToPopulation(system.Population, system.MetaCtrl)
		}

		// Diff Engine — compare old/new snapshots, generate patches,
		// and submit to Coordinator for evaluation and application. Every
		// patch is attributed to the current best strategy so the coordinator
		// and runtime can A/B compare it against the active one.
		if system.DiffReg != nil && system.Coordinator != nil && system.GenomeReg != nil {
			strategyID := ""
			if best := system.Population.BestStrategy(); best != nil {
				strategyID = best.ID
			}
			diffPatches, dErr := generateDiffPatches(ctx, system.GenomeReg, system.DiffReg, 3, strategyID)
			if dErr != nil {
				log.WarnContext(ctx, "diff engine failed, continuing", "method", "RunIdleEvolution", "error", dErr)
			} else {
				for _, dp := range diffPatches {
					system.Coordinator.Submit(coordinator.PatchProposal{
						Patch:     dp,
						Source:    coordinator.SourceGA,
						Reason:    "GA: evolution generated structural change",
						Priority:  6,
						Fitness:   system.Population.Stats().BestScore,
						Timestamp: time.Now(),
					})
				}
				if len(diffPatches) > 0 {
					system.Coordinator.Evaluate(ctx)
				}
			}
		}

		// Run the post-generation hook (promotion, report, etc.).
		if system.AfterGeneration != nil {
			if err := system.AfterGeneration(ctx, gen, system); err != nil {
				log.WarnContext(ctx, "AfterGeneration hook failed", "method", "RunIdleEvolution", "generation", system.Population.Generation,
					"run_iteration", gen,
					"error", err,
				)
			}
		}
	}

	// Run the post-run hook for final report generation.
	if system.AfterRun != nil {
		if err := system.AfterRun(ctx, system); err != nil {
			log.WarnContext(ctx, "AfterRun hook failed", "method", "RunIdleEvolution", "error", err)
		}
	}

	return nil
}

// generateDiffPatches mutates each registered genome, snapshots each mutated
// candidate, and diffs the candidate snapshot against the parent snapshot to
// produce RuntimePatches.
//
// Algorithm per genome:
//  1. Snapshot parent (old).
//  2. Mutate → nChildren candidates.
//  3. For each candidate: Snapshot candidate (new), Diff(old, new).
//  4. Collect non-empty patches.
//
// Args:
//   - ctx        - timeout and cancellation context.
//   - genomeReg  - registry of evolvable genomes.
//   - diffReg    - registry of genome-specific differs.
//   - nChildren  - number of mutation candidates per genome (must be > 0).
//   - strategyID - the mutation.Strategy ID these patches are attributed to.
//     Must be non-empty; a patch without a strategy cannot be A/B compared,
//     so generateDiffPatches fails fast rather than emit unattributable
//     patches (deployment pipelines MUST NOT invent one).
//
// Returns:
//   - patches - non-empty RuntimePatches from successful mutations, each stamped
//     with the supplied strategyID.
//   - err     - non-nil if nChildren is invalid or strategyID is empty.
func generateDiffPatches(
	ctx context.Context,
	genomeReg *evogenome.Registry,
	diffReg *diff.Registry,
	nChildren int,
	strategyID string,
) ([]patch.RuntimePatch, error) {
	if nChildren <= 0 {
		return nil, fmt.Errorf("generateDiffPatches: nChildren must be > 0, got %d", nChildren)
	}
	if strategyID == "" {
		return nil, fmt.Errorf("generateDiffPatches: strategyID must not be empty")
	}

	var allPatches []patch.RuntimePatch

	for _, name := range genomeReg.List() {
		g, err := genomeReg.Get(name)
		if err != nil {
			continue
		}

		differ, err := diffReg.Get(name)
		if err != nil {
			continue
		}

		// Step 1: Snapshot parent.
		oldSnap, err := g.Snapshot(ctx)
		if err != nil {
			log.WarnContext(ctx, "parent snapshot failed, skipping", "method", "generateDiffPatches",
				"genome", name, "error", err)
			continue
		}
		if oldSnap == nil {
			continue
		}

		// Step 2: Mutate → nChildren candidates.
		children, err := g.Mutate(ctx, nChildren)
		if err != nil {
			log.WarnContext(ctx, "mutate failed, skipping", "method", "generateDiffPatches",
				"genome", name, "error", err)
			continue
		}

		// Step 3: For each candidate, Snapshot + Diff against parent.
		for _, child := range children {
			newSnap, err := child.Snapshot(ctx)
			if err != nil {
				log.WarnContext(ctx, "child snapshot failed, skipping", "method", "generateDiffPatches",
					"genome", name, "error", err)
				continue
			}
			if newSnap == nil {
				continue
			}

			patches, err := differ.Diff(ctx, oldSnap, newSnap)
			if err != nil {
				log.WarnContext(ctx, "diff failed, skipping", "method", "generateDiffPatches",
					"genome", name, "error", err)
				continue
			}

			for i := range patches {
				patches[i].StrategyID = strategyID
			}
			allPatches = append(allPatches, patches...)
		}
	}

	return allPatches, nil
}

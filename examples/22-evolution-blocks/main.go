// Command 22-evolution-blocks demonstrates composing the evolution system
// from the public api/evolution building blocks — WITHOUT importing any
// internal/ package. This is the integration path for external modules
// and AI assistants that want to assemble their own evolution pipeline.
//
// Flow:
//  1. Define a base Strategy (the seed genotype).
//  2. Build a Mutator from api/evolution (mutates strategy params/prompts).
//  3. Build a Population from api/evolution (GA over the base strategy).
//  4. Run one generation of evolution and inspect the best strategy.
//  5. Build a Promoter from api/evolution and evaluate a candidate's fate.
//
// This example DOES NOT import any internal/ package — every component
// comes from the public api/evolution package and its sub-packages.
//
// Usage:
//
//	go run examples/22-evolution-blocks/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	pubevolution "github.com/Timwood0x10/ares/api/evolution"
)

// exitf logs a formatted message and exits with code 1, canceling the
// context first to avoid the gocritic exitAfterDefer warning.
func exitf(cancel context.CancelFunc, format string, args ...any) {
	cancel()
	log.Printf(format+"\n", args...)
	os.Exit(1)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Define the seed strategy — the genotype that evolution will mutate.
	//    Params are the knobs the mutator may perturb (temperature, top_k, ...).
	base := &pubevolution.Strategy{
		ID:             "base-strategy-001",
		Version:        1,
		PromptTemplate: "You are a helpful assistant. Answer concisely.",
		Params: map[string]any{
			"temperature": 0.7,
			"top_k":       40,
			"max_tokens":  2048,
		},
	}
	fmt.Printf("Seed strategy: id=%s version=%d params=%v\n", base.ID, base.Version, base.Params)

	// 2. Build a Mutator from the public API.
	//    NewMutator(model, cfg) — model is reserved for future LLM-guided mutation;
	//    cfg controls mutation probabilities. A zero cfg uses sensible defaults.
	mutator, err := pubevolution.NewMutator("ollama/llama3.2", pubevolution.MutationConfig{
		ParamMutationProb:  0.4,
		PromptMutationProb: 0.2,
	})
	if err != nil {
		exitf(cancel, "create mutator: %v", err)
	}

	// Mutate the base strategy once to show what a child looks like.
	child, err := mutator.Mutate(ctx, base)
	if err != nil {
		exitf(cancel, "mutate base strategy: %v", err)
	}
	fmt.Printf("Mutated child: id=%s version=%d mutation=%s params=%v\n",
		child.ID, child.Version, child.MutationType, child.Params)

	// 3. Build a Population from the public API.
	//    NewPopulation(base, cfg) — creates a GA population seeded from `base`,
	//    with size/elite/survival/selection knobs. DefaultPopulationConfig()
	//    gives sensible defaults if cfg is zero.
	popCfg := pubevolution.DefaultPopulationConfig()
	popCfg.Size = 10 // smaller population for the demo
	population, err := pubevolution.NewPopulation(base, popCfg)
	if err != nil {
		exitf(cancel, "create population: %v", err)
	}
	fmt.Printf("Population: size=%d generation=%d best_score=%.2f\n",
		population.Size(), population.CurrentGeneration(), population.BestScore())

	// 4. Score the population before evolving.
	//    Evolve rejects agents with unevaluated score (-1). External callers
	//    implement ScorerFunc to plug in their own evaluator (LLM judge,
	//    benchmark harness, success rate counter). Here we use a mock scorer
	//    that rewards lower temperature and higher max_tokens — a simple
	//    proxy for "stable, thorough answers".
	population.ScoreAgents(func(s *pubevolution.Strategy) float64 {
		score := 0.0
		if t, ok := s.Params["temperature"].(float64); ok {
			score += (1.0 - t) // lower temp → higher score
		}
		if m, ok := s.Params["max_tokens"].(float64); ok {
			score += m / 4096.0 // more tokens → higher score (capped at 1.0)
		}
		return score
	})
	fmt.Printf("Scored %d agents, best_score=%.2f\n", population.Size(), population.BestScore())

	// 5. Run one generation of evolution.
	//    Evolve() mutates+crossovers the population, evaluates fitness,
	//    and selects survivors. After one generation, BestStrategy() reflects
	//    the new champion.
	if err := population.Evolve(ctx); err != nil {
		exitf(cancel, "evolve generation 1: %v", err)
	}
	best := population.BestStrategy()
	fmt.Printf("After evolve: generation=%d best_score=%.2f\n",
		population.CurrentGeneration(), population.BestScore())
	if best != nil {
		fmt.Printf("  champion: id=%s version=%d params=%v\n",
			best.ID, best.Version, best.Params)
	}

	// 6. Build a Promoter from the public API.
	//    NewPromoter(criteria) — decides whether a strategy should be promoted
	//    (champion), demoted, or kept based on accumulated evidence.
	promoter := pubevolution.NewPromoter(&pubevolution.PromotionCriteria{
		MinSampleCount:     1, // demo: accept after 1 sample
		MinSuccessRate:     0.5,
		MinConfidence:      0.5,
		ChampionHoldPeriod: 1,
		DemotionThreshold:  0.2,
		MaxChampionTenure:  10,
	})

	// Evaluate the champion's fate with a mock evidence signal.
	decision, err := promoter.Evaluate(ctx, best.ID, 0.9, 0.85)
	if err != nil {
		exitf(cancel, "promoter evaluate: %v", err)
	}
	fmt.Printf("Promoter decision for champion: %s\n", decision)

	// Promote the champion explicitly.
	if err := promoter.Promote(ctx, best.ID); err != nil {
		exitf(cancel, "promote champion: %v", err)
	}
	fmt.Println("Champion promoted.")

	fmt.Println("Evolution blocks integration example completed.")
}

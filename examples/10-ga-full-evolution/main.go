// Command 10-ga-full-evolution demonstrates a comprehensive GA evolution
// pipeline using ONLY the public api/evolution building blocks — no internal/
// imports. This is the AI-assistant-safe version: external modules and AI
// assistants must never import internal/.
//
// Demonstrates (api/evolution coverage):
//  1. Tool selection strategy evolution — optimal tool combinations per task type
//  2. Memory-guided mutation — historical experience biases mutation direction
//  3. Multi-objective fitness — quality + cost + latency combined scoring
//
// Removed vs. the legacy version (requires internal/, not yet public):
//   - Workflow DAG topology evolution (needs coordinator/patch/diff/graph blocks)
//   - Population.Stats / ExportHistory / Strategy.DimensionScores (not in public API)
//
// Run: go run examples/10-ga-full-evolution/main.go
package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	pubevolution "github.com/Timwood0x10/ares/api/evolution"
	pubmutation "github.com/Timwood0x10/ares/api/evolution/mutation"
)

// exitf logs a formatted message and exits with code 1, canceling the
// context first to avoid the gocritic exitAfterDefer warning.
func exitf(cancel context.CancelFunc, format string, args ...any) {
	cancel()
	fmt.Printf(format+"\n", args...)
	os.Exit(1)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Println("═══ GA Full Evolution Demo (public API only) ═══")
	fmt.Println()

	// ── 1. Create base strategy (tools, params, prompt) ──
	//    Score=-1 marks the seed as unevaluated; ScoreAgents will fill it.
	//    Uses the mutation sub-package Strategy because Mutator lives there;
	//    converted to the top-level evolution.Strategy when fed to Population.
	base := &pubmutation.Strategy{
		ID:             "root-strategy",
		Version:        1,
		PromptTemplate: "You are a helpful assistant. Complete the task efficiently.",
		Params: map[string]any{
			"temperature":   0.7,
			"top_k":         40,
			"max_tokens":    4096,
			"tool_selector": "auto", // tool selection strategy: auto/manual/priority
			"search_depth":  3,      // search depth
			"batch_size":    5,      // batch size
		},
	}
	fmt.Printf("Seed strategy: id=%s params=%v\n", base.ID, base.Params)

	// ── 2. Create mutator with param ranges + prompt pool + tool pool ──
	//    Mutator lives in the evolution/mutation sub-package so it can carry
	//    the full MutatorConfig (ranges/pools/probabilities). The top-level
	//    evolution.NewMutator only takes probabilities, not ranges — sub-package
	//    is the right entry point for external callers who need custom ranges.
	mutator, err := pubmutation.NewMutator(pubmutation.MutatorConfig{
		ParamRanges: map[string][]any{
			"temperature":   {0.1, 0.3, 0.5, 0.7, 0.9},
			"top_k":         {10, 20, 40, 60, 80, 100},
			"max_tokens":    {1024, 2048, 4096, 8192},
			"tool_selector": {"auto", "manual", "priority"},
			"search_depth":  {1, 2, 3, 4, 5},
			"batch_size":    {1, 3, 5, 10},
		},
		PromptPool: []string{
			"You are a helpful assistant. Complete the task efficiently.",
			"You are an expert programmer. Write clean, efficient code.",
			"You are a data analyst. Analyze data thoroughly and report findings.",
			"You are a system architect. Design robust and scalable solutions.",
		},
		ToolPool: []string{"search", "read", "write", "exec", "calculate", "code"},
		// Mutation probabilities — tune how aggressive evolution is.
		ParamMutationProb:  0.4,
		PromptMutationProb: 0.2,
	})
	if err != nil {
		exitf(cancel, "create mutator: %v", err)
	}
	fmt.Println("Mutator configured with 6 param ranges, 4 prompts, 6 tools")

	// ── 3. Mutate the base once to preview a child ──
	previewChild, err := mutator.Mutate(ctx, base)
	if err != nil {
		exitf(cancel, "mutate preview: %v", err)
	}
	fmt.Printf("Preview child: id=%s version=%d mutation=%s params=%v\n",
		previewChild.ID, previewChild.Version, previewChild.MutationType, previewChild.Params)

	// ── 4. Create population (GA core engine) ──
	//    Population lives at the top-level evolution package and consumes the
	//    top-level Strategy — convert the mutation.Strategy seed here.
	pubBase := &pubevolution.Strategy{
		ID:             base.ID,
		Version:        base.Version,
		PromptTemplate: base.PromptTemplate,
		Params:         base.Params,
	}
	popCfg := pubevolution.DefaultPopulationConfig()
	popCfg.Size = 20
	popCfg.EliteCount = 3
	popCfg.MutationRate = 0.2
	popCfg.SurvivalRate = 0.6
	popCfg.SelectionStrategy = "tournament"
	popCfg.TournamentSize = 3
	population, err := pubevolution.NewPopulation(pubBase, popCfg)
	if err != nil {
		exitf(cancel, "create population: %v", err)
	}
	fmt.Printf("Population initialized with %d individuals\n", population.Size())

	// ── 5. Memory-guided provider (mock experience) ──
	//    In production this would come from api/experience FeedbackService;
	//    here we mock it to show how historical bias guides mutation scoring.
	hintProvider := &mockHintProvider{
		hints: []evolutionHint{
			{taskType: "code", tool: "search", confidence: 0.85},
			{taskType: "code", tool: "read", confidence: 0.72},
			{taskType: "data", tool: "calculate", confidence: 0.91},
			{taskType: "data", tool: "exec", confidence: 0.65},
		},
	}
	fmt.Println("Memory-guided provider loaded (4 experiences)")

	// ── 6. Run GA evolution (5 generations) ──
	fmt.Println("\n═══ Starting GA Evolution ═══")
	for gen := 0; gen < 5; gen++ {
		// Score every agent with the multi-objective scorer before evolving —
		// Evolve rejects agents with score=-1 (unevaluated).
		population.ScoreAgents(func(s *pubevolution.Strategy) float64 {
			return multiObjectiveScore(s, hintProvider)
		})

		if err := population.Evolve(ctx); err != nil {
			exitf(cancel, "evolve generation %d: %v", gen+1, err)
		}

		best := population.BestStrategy()
		toolSel := "auto"
		if best != nil {
			if v, ok := best.Params["tool_selector"]; ok {
				toolSel = fmt.Sprintf("%v", v)
			}
		}
		fmt.Printf("  Gen %d: best=%.1f, pop=%d, tool=%s\n",
			gen+1, population.BestScore(), population.Size(), toolSel)
	}

	// ── 7. Show evolution results — what GA learned ──
	fmt.Println("\n═══ Evolution Results: What GA Learned ═══")
	best := population.BestStrategy()
	if best != nil {
		fmt.Printf("✅ Tool selection: %v\n", best.Params["tool_selector"])
		fmt.Printf("✅ Search depth: %v\n", best.Params["search_depth"])
		fmt.Printf("✅ Prompt template: %q\n", best.PromptTemplate)
		fmt.Printf("✅ Best score: %.2f\n", population.BestScore())
		fmt.Printf("✅ Generation: %d\n", population.CurrentGeneration())
	}

	// ── 8. Build a Promoter and evaluate the champion's fate ──
	promoter := pubevolution.NewPromoter(&pubevolution.PromotionCriteria{
		MinSampleCount:     1,
		MinSuccessRate:     0.5,
		MinConfidence:      0.5,
		ChampionHoldPeriod: 1,
		DemotionThreshold:  0.2,
		MaxChampionTenure:  10,
	})
	if best != nil {
		decision, err := promoter.Evaluate(ctx, best.ID, population.BestScore(), 0.85)
		if err != nil {
			exitf(cancel, "promoter evaluate: %v", err)
		}
		fmt.Printf("\nPromoter decision for champion: %s\n", decision)
		if err := promoter.Promote(ctx, best.ID); err != nil {
			exitf(cancel, "promote champion: %v", err)
		}
		fmt.Println("Champion promoted.")
	}

	fmt.Println("\n✅ GA full evolution demo completed")
}

// ── Multi-objective scorer ───────────────────────────────────────────

// multiObjectiveScore computes fitness from quality, cost, and latency.
// Memory-guided confidence from the hint provider biases quality upward.
func multiObjectiveScore(s *pubevolution.Strategy, hp *mockHintProvider) float64 {
	quality := scoreQuality(s)
	cost := scoreCost(s)
	latency := scoreLatency(s)

	// Memory-guided confidence bonus — historical evidence biases the score.
	confidence := hp.confidenceForStrategy(s)
	quality += confidence * 5.0

	// Multi-objective aggregation: quality prioritized, cost/latency penalized.
	finalScore := quality*0.6 - cost*0.25 - latency*0.15
	return max(0, finalScore)
}

// scoreQuality estimates strategy quality based on params.
func scoreQuality(s *pubevolution.Strategy) float64 {
	score := 50.0
	if v, ok := s.Params["temperature"]; ok {
		if t := toFloat64(v); t >= 0.5 && t <= 0.8 {
			score += 20
		} else if t < 0.3 || t > 0.9 {
			score -= 10
		}
	}
	if v, ok := s.Params["search_depth"]; ok {
		if d := toInt(v); d >= 3 && d <= 5 {
			score += 15
		} else if d < 2 {
			score -= 10
		}
	}
	if sel, ok := s.Params["tool_selector"]; ok {
		switch fmt.Sprintf("%v", sel) {
		case "priority":
			score += 10
		case "manual":
			score += 5
		}
	}
	return min(100, max(0, score))
}

// scoreCost estimates computational cost of a strategy.
func scoreCost(s *pubevolution.Strategy) float64 {
	cost := 10.0
	if v, ok := s.Params["max_tokens"]; ok {
		cost += float64(toInt(v)) / 500
	}
	if v, ok := s.Params["search_depth"]; ok {
		cost += float64(toInt(v)) * 5
	}
	if v, ok := s.Params["batch_size"]; ok {
		cost += float64(toInt(v)) * 2
	}
	return min(100, cost)
}

// scoreLatency estimates execution latency of a strategy.
func scoreLatency(s *pubevolution.Strategy) float64 {
	latency := 5.0
	if v, ok := s.Params["search_depth"]; ok {
		latency += float64(toInt(v)) * 8
	}
	if v, ok := s.Params["max_tokens"]; ok {
		latency += float64(toInt(v)) / 1000
	}
	return min(100, latency)
}

// ── Memory-guided provider (mock) ────────────────────────────────────

type evolutionHint struct {
	taskType   string
	tool       string
	confidence float64
}

type mockHintProvider struct {
	hints []evolutionHint
}

// confidenceForStrategy returns the highest confidence hint matching the
// strategy's current tool_selector. Zero means no historical evidence.
func (m *mockHintProvider) confidenceForStrategy(s *pubevolution.Strategy) float64 {
	confidence := 0.0
	if sel, ok := s.Params["tool_selector"]; ok {
		for _, h := range m.hints {
			if fmt.Sprintf("%v", sel) == h.tool {
				confidence = max(confidence, h.confidence)
			}
		}
	}
	return confidence
}

// ── Helpers ──────────────────────────────────────────────────────────

// toFloat64 safely converts an any value to float64.
func toFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case string:
		f := 0.0
		_, _ = fmt.Sscanf(val, "%f", &f)
		return f
	default:
		return 0
	}
}

// toInt safely converts an any value to int.
func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		i := 0
		_, _ = fmt.Sscanf(val, "%d", &i)
		return i
	default:
		return 0
	}
}

func init() {
	// Seed the global rand so mutation/crossover vary across runs.
	_ = rand.New(rand.NewSource(time.Now().UnixNano()))
}

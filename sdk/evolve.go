package sdk

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// Evolvable strategy parameter keys. Shared across base strategy creation,
// mutator ranges, scoring, and application so the dimension names stay in
// sync and the linter (goconst) stays quiet.
const (
	paramToolSelector      = "tool_selector"
	paramSearchDepth       = "search_depth"
	paramSchedulerStrategy = "scheduler_strategy"
	paramMemoryThreshold   = "memory_threshold"
	paramRecoveryStrategy  = "recovery_strategy"
)

// Evolve runs an evolution cycle to improve an agent's instruction. It uses the
// LLM to generate variations, evaluates them against the given task, and returns
// the best-evolved instruction.
func (r *Runtime) Evolve(ctx context.Context, agent *Agent, task string) (string, error) {
	if agent == nil {
		return "", fmt.Errorf("evolve: agent is nil")
	}
	if !r.evoEnabled {
		return "", fmt.Errorf("evolution not enabled (use WithEvolution())")
	}

	if r.trace {
		log.Printf("[ares:evolve] evolving agent %q on task: %s", agent.name, task)
	}

	// Create base strategy with meaningful dimensions: tool selection,
	// workflow topology, scheduler strategy, memory retrieval, recovery.
	base := &mutation.Strategy{
		ID:        fmt.Sprintf("sdk-%s", agent.name),
		Version:   1,
		Score:     -1,
		CreatedAt: time.Now(),
		Params: map[string]any{
			paramToolSelector:      "auto",  // auto / manual / priority
			paramSearchDepth:       3,       // 1-5: how deep to search
			paramSchedulerStrategy: "fifo",  // fifo / priority / round_robin
			paramMemoryThreshold:   0.7,     // 0.0-1.0: similarity threshold
			paramRecoveryStrategy:  "retry", // retry / replace / fallback
		},
		PromptTemplate: agent.instruction,
	}

	// Create mutator for meaningful dimensions.
	mutator, err := mutation.NewMutator(
		mutation.WithParamRanges(evolvableParams()),
	)
	if err != nil {
		return "", fmt.Errorf("create mutator: %w", err)
	}

	// Create crossover operator (uses PyGAD-inspired operators).
	crosser, err := genome.NewCrossover(
		genome.WithSeed(42),
		genome.WithCrossoverType(genome.CrossoverUniform),
	)
	if err != nil {
		return "", fmt.Errorf("create crossover: %w", err)
	}

	// Create GA population.
	pop, err := genome.NewPopulation(ctx, base, mutator,
		genome.WithPopulationSize(10),
		genome.WithEliteCount(2),
		genome.WithMutationRate(0.3),
		genome.WithSurvivalRate(0.5),
		genome.WithSelectionStrategy("tournament"),
		genome.WithTournamentSelection(3),
	)
	if err != nil {
		return "", fmt.Errorf("create population: %w", err)
	}

	// Run evolution using actual execution as scorer (no LLM).
	scorer := func(s *mutation.Strategy) float64 {
		return executeAndScore(ctx, r, agent, task, s)
	}

	for gen := 0; gen < 3; gen++ {
		pop.ScoreAgents(scorer)
		if err := pop.Evolve(ctx, mutator, crosser); err != nil {
			return "", fmt.Errorf("evolve generation %d: %w", gen, err)
		}
	}

	// Get the best strategy.
	best := pop.BestStrategy()
	if best == nil {
		return "", fmt.Errorf("evolution produced no viable strategy")
	}

	if r.trace {
		stats := pop.Stats()
		log.Printf("[ares:evolve] GA evolution complete: gen=%d, best=%.1f, avg=%.1f, strategy=%v",
			stats.Generation, stats.BestScore, stats.AvgScore, best.Params)
	}

	// Apply the evolved strategy's params to the agent.
	applyEvolvedParams(agent, best.Params)

	// Return the best-evolved instruction: the base prompt template enriched
	// with the evolved strategy parameters, so callers can apply the evolved
	// configuration to a new agent via WithInstruction.
	return buildEvolvedInstruction(agent.instruction, best), nil
}

// buildEvolvedInstruction composes the base instruction with the evolved
// strategy parameters into a single instruction string. It returns the base
// instruction unchanged when the strategy is nil or carries no parameters.
func buildEvolvedInstruction(base string, s *mutation.Strategy) string {
	if s == nil {
		return base
	}
	params := []struct {
		key string
		v   any
	}{
		{paramToolSelector, s.Params[paramToolSelector]},
		{paramSearchDepth, s.Params[paramSearchDepth]},
		{paramSchedulerStrategy, s.Params[paramSchedulerStrategy]},
		{paramMemoryThreshold, s.Params[paramMemoryThreshold]},
		{paramRecoveryStrategy, s.Params[paramRecoveryStrategy]},
	}
	instruction := base + "\n\nEvolved strategy:"
	for _, p := range params {
		if p.v != nil {
			instruction += fmt.Sprintf("\n- %s: %v", p.key, p.v)
		}
	}
	return instruction
}

// evolvableParams returns the parameter ranges for meaningful evolution dimensions.
func evolvableParams() map[string]mutation.ParamRange {
	return map[string]mutation.ParamRange{
		paramToolSelector:      {Values: []any{"auto", "manual", strategyPriority}},
		paramSearchDepth:       {Values: []any{1, 2, 3, 4, 5}},
		paramSchedulerStrategy: {Values: []any{"fifo", strategyPriority, "round_robin"}},
		paramMemoryThreshold:   {Values: []any{0.3, 0.5, 0.7, 0.9}},
		paramRecoveryStrategy:  {Values: []any{"retry", "replace", "fallback"}},
	}
}

// executeAndScore runs the task with a given strategy and scores based on
// actual execution results: success, latency, and token efficiency.
// No LLM involved — pure execution-based evaluation.
func executeAndScore(ctx context.Context, r *Runtime, agent *Agent, task string, s *mutation.Strategy) float64 {
	evolvedAgent := &Agent{
		name:        agent.name,
		instruction: s.PromptTemplate,
		tools:       applyToolSelector(agent.tools, s.Params),
		runtime:     agent.runtime,
		humanInput:  agent.humanInput,
		maxIter:     agent.maxIter,
		discovery:   agent.discovery,
		toolSource:  agent.toolSource,
		selector:    agent.selector,
	}

	start := time.Now()
	result, err := evolvedAgent.Run(ctx, task)
	duration := time.Since(start)

	if err != nil {
		log.Printf("[ares:evolve] execution failed: %v", err)
		return 10.0
	}

	successBonus := 50.0
	if result != nil && result.Output != "" {
		successBonus = 60.0
	}

	speedScore := 30.0 * (1.0 - min(1.0, duration.Seconds()/30.0))

	efficiencyScore := 10.0
	if result != nil && result.TokenUsage.Total > 0 {
		efficiencyScore = 20.0 * (1.0 - min(1.0, float64(result.TokenUsage.Total)/2000.0))
	}

	return successBonus + speedScore + efficiencyScore
}

// applyToolSelector filters the agent's tool list based on the strategy.
func applyToolSelector(toolList []tools.Tool, params map[string]any) []tools.Tool {
	selector, _ := params[paramToolSelector].(string)
	switch selector {
	case "priority":
		if len(toolList) > 3 {
			return toolList[:3]
		}
		return toolList
	case "manual":
		var filtered []tools.Tool
		for _, t := range toolList {
			if t.Name() == "search" || t.Name() == "read" {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) > 0 {
			return filtered
		}
		return toolList
	default:
		return toolList
	}
}

// applyEvolvedParams applies the evolved strategy params to the agent.
// tool_selector is the only dimension with a direct backing field on Agent
// (the tool list), so it is applied by filtering agent.tools. The remaining
// dimensions (search_depth, scheduler_strategy, memory_threshold,
// recovery_strategy) have no corresponding Agent field yet; they are logged
// and marked TODO so the wiring gap stays visible instead of being silent.
func applyEvolvedParams(agent *Agent, params map[string]any) {
	if v, ok := params[paramToolSelector]; ok {
		if selector, isString := v.(string); isString {
			agent.tools = applyToolSelector(agent.tools, map[string]any{paramToolSelector: selector})
			log.Printf("[ares:evolve] applied tool_selector=%v (%d tools after filtering)", v, len(agent.tools))
		}
	}
	// TODO: wire search_depth/scheduler_strategy/memory_threshold/recovery_strategy
	// into Agent fields (e.g. discovery depth, scheduler, RAG threshold, retry
	// policy) and consume them inside Run; until then they cannot be applied.
	for _, key := range []string{paramSearchDepth, paramSchedulerStrategy, paramMemoryThreshold, paramRecoveryStrategy} {
		if v, ok := params[key]; ok {
			log.Printf("[ares:evolve] TODO: %s=%v not wired to Agent field yet", key, v)
		}
	}
}

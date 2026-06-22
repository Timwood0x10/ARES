// Package evolution provides a high-level API for autonomous genetic algorithm
// based strategy evolution. External users can import this single package to
// create, configure, and run evolution systems without depending on internal packages.
package evolution

import "time"

// Strategy represents an evolved agent decision strategy.
type Strategy struct {
	// ID is the unique identifier of this strategy.
	ID string `json:"id"`

	// Name is the human-readable name of the strategy.
	Name string `json:"name,omitempty"`

	// Version is the version number of this strategy (monotonically increasing).
	Version int `json:"version"`

	// Params holds the configurable parameters of the strategy.
	Params map[string]any `json:"params,omitempty"`

	// ParentID references the parent strategy this was evolved from (empty for root strategies).
	ParentID string `json:"parent_id,omitempty"`

	// PromptTemplate is the behavior prompt template for the agent.
	PromptTemplate string `json:"prompt_template,omitempty"`

	// MutationType records the mutation type that created this strategy.
	MutationType string `json:"mutation_type"`

	// Score is the current evaluation score (-1 = unevaluated).
	Score float64 `json:"score"`

	// CreatedAt is the timestamp when this strategy was created.
	CreatedAt time.Time `json:"created_at"`
}

// StrategyLineage records parent-child relationships in evolution history.
type StrategyLineage struct {
	// ParentID is the ID of the parent (source) strategy.
	ParentID string `json:"parent_id"`

	// ChildID is the ID of the new (mutated) strategy.
	ChildID string `json:"child_id"`

	// MutationType describes what kind of mutation was applied.
	MutationType string `json:"mutation_type"`

	// WinRate achieved by the child strategy in arena testing.
	WinRate float64 `json:"win_rate"`

	// ScoreDelta is the delta between child and parent scores.
	ScoreDelta float64 `json:"score_delta"`

	// Timestamp when this lineage record was created.
	Timestamp int64 `json:"timestamp"`
}

// ScorerFunc evaluates an agent and returns its fitness score.
// Return a score in [0, 100]. Higher = better.
type ScorerFunc func(params map[string]any) float64

// SystemConfig holds configuration for creating an EvolutionSystem via NewService.
type SystemConfig struct {
	// BaseStrategy is the root strategy to evolve from.
	BaseStrategy *Strategy

	// PopulationSize is the number of agents per generation (default 20).
	PopulationSize int

	// EliteCount is how many top strategies to preserve unchanged (default 2).
	EliteCount int

	// SurvivalRate is fraction of top performers to keep [0-1] (default 0.6).
	SurvivalRate float64

	// MutationRate is probability of mutating each offspring [0-1] (default 0.2).
	MutationRate float64

	// MinMutationRate is the floor for adaptive mutation rate (default 0.05).
	MinMutationRate float64

	// MaxMutationRate is the ceiling for adaptive mutation rate (default 0.5).
	MaxMutationRate float64

	// MaxStagnantGenerations triggers bottom-1/3 reset when best score plateaus (default 10).
	MaxStagnantGenerations int

	// DiversityThreshold below which mutation rate is boosted (default 0.15).
	DiversityThreshold float64

	// BreedingPoolRatio limits breeding to the top fraction of survivors (default 0.3).
	BreedingPoolRatio float64

	// Generations is total generations to run (default 15).
	Generations int

	// Seed makes evolution deterministic when > 0 (default 0 = random).
	Seed int64

	// UseDeterministicIDs enables counter-based strategy IDs (default: true when Seed != 0).
	UseDeterministicIDs *bool

	// PromptPool is candidate prompt templates for mutation.
	PromptPool []string

	// EnableWiredMode uses WiredEvolutionSystem (full wiring) vs raw Population.
	EnableWiredMode bool

	// Scorer evaluates agent fitness. When nil, a temperature-proximity scorer is used.
	Scorer ScorerFunc
}

// DefaultConfig returns a sensible default configuration for the evolution system.
//
// Returns:
//
//	*SystemConfig - configuration with default values applied.
func DefaultConfig() *SystemConfig {
	return &SystemConfig{
		PopulationSize:         20,
		EliteCount:             2,
		SurvivalRate:           0.6,
		MutationRate:           0.2,
		MinMutationRate:        0.05,
		MaxMutationRate:        0.5,
		MaxStagnantGenerations: 10,
		DiversityThreshold:     0.15,
		BreedingPoolRatio:      0.3,
		Generations:            15,
		Seed:                   0,
		PromptPool:             []string{},
		EnableWiredMode:        true,
	}
}

// Stats holds population statistics after each evolution generation.
type Stats struct {
	// Generation is the current generation number.
	Generation int

	// Size is the number of agents in the population.
	Size int

	// BestScore is the highest score among all agents.
	BestScore float64

	// AvgScore is the average score across all agents.
	AvgScore float64

	// WorstScore is the lowest score among all agents.
	WorstScore float64
}

// EvolutionResult holds the result of a complete evolution run.
type EvolutionResult struct {
	// BestStrategy is the best strategy found across all generations.
	BestStrategy *Strategy

	// Stats contains per-generation statistics collected during evolution.
	Stats []Stats

	// Lineages contains recorded parent-child relationships from evolution.
	Lineages []StrategyLineage

	// TotalGens is the total number of generations that were executed.
	TotalGens int
}

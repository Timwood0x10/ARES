// Package evolution provides the public API for strategy evolution.
package evolution

import (
	"context"
	"time"
)

// ---------------------------------------------------------------------------
// Strategy types
// ---------------------------------------------------------------------------

// Strategy represents an evolved strategy with parameterization.
// It wraps the internal mutation.Strategy type.
type Strategy struct {
	ID              string
	Name            string
	Version         int
	Score           float64
	ParentID        string
	PromptTemplate  string
	Params          map[string]any
	StrategyMutationType string
}

// Lineage records a parent-child relationship between strategies.
type Lineage struct {
	ParentID         string
	ChildID          string
	MutationType     string
	WinRate          float64
	ScoreImprovement float64
	ParentScore      float64
	ChildScore       float64
}

// ---------------------------------------------------------------------------
// DreamCycle — continuous evolution loop
// ---------------------------------------------------------------------------

// DreamCycleConfig controls the dream cycle behavior.
type DreamCycleConfig struct {
	Enabled              bool
	MinTasksBeforeEvolve int
	MinScoreDrop         float64
	MaxMutations         int
	MinWinRate           float64
	Cooldown             time.Duration
	TaskSampleSize       int
	QuickRejectRuns      int
}

func DefaultDreamCycleConfig() *DreamCycleConfig {
	return &DreamCycleConfig{
		Enabled:              true,
		MinTasksBeforeEvolve: 10,
		MinScoreDrop:         0.15,
		MaxMutations:         3,
		MinWinRate:           0.55,
		Cooldown:             5 * time.Minute,
		TaskSampleSize:       50,
		QuickRejectRuns:      5,
	}
}

// DreamCycle runs mutations when the system is idle or when scores degrade.
type DreamCycle interface {
	Run(ctx context.Context) error
	SetEnabled(enabled bool)
	Enabled() bool
}

// NewDreamCycle creates a dream cycle from internal types.
// The caller must provide a MutationAdapter and a TesterAdapter.
func NewDreamCycle(scheduler any, mutator any, opts ...any) (DreamCycle, error) {
	// This is a facade — users wire internal components via the embedding layer.
	return nil, nil
}

// ---------------------------------------------------------------------------
// Genome (genetic algorithm) API
// ---------------------------------------------------------------------------

// Agent is an individual in the genetic algorithm population.
type Agent struct {
	ID     string
	Score  float64
	Params map[string]any
}

// Population is a collection of agents evolving through generations.
type Population interface {
	Agents() []Agent
	CurrentGeneration() int
	BestScore() float64
	Evolve(ctx context.Context) error
	Snapshot() ([]Agent, int, error)
}

// NewPopulation creates a new GA population.
func NewPopulation(config GenomeConfig) (Population, error) {
	return nil, nil
}

// GenomeConfig controls the GA population.
type GenomeConfig struct {
	Size               int
	EliteCount         int
	MutationRate       float64
	SurvivalRate       float64
	SelectionStrategy  string // "tournament" | "rank" | "roulette" | "random"
	TournamentSize     int
}

// ---------------------------------------------------------------------------
// Mutation subsystem
// ---------------------------------------------------------------------------

// Mutator defines how strategies are mutated.
type Mutator interface {
	Mutate(ctx context.Context, parent *Strategy) (*Strategy, error)
	MutateMany(ctx context.Context, parent *Strategy, n int) ([]*Strategy, error)
}

// NewMutator creates a strategy mutator.
func NewMutator(model string, config MutationConfig) Mutator {
	return nil
}

// MutationConfig controls mutation behavior.
type MutationConfig struct {
	ParamMutationProb float64
	PromptMutationProb float64
}

// ---------------------------------------------------------------------------
// Promotion subsystem
// ---------------------------------------------------------------------------

// PromotionCriteria defines when a strategy is promoted or demoted.
type PromotionCriteria struct {
	MinSampleCount         int
	MinSuccessRate         float64
	MinConfidence          float64
	ChampionHoldPeriod     int
	DemotionThreshold      float64
	MaxChampionTenure      int
}

// Promoter manages strategy lifecycle states.
type Promoter interface {
	Evaluate(ctx context.Context, strategyID string, successRate float64, confidence float64) (string, error)
	Promote(ctx context.Context, strategyID string) error
	Demote(ctx context.Context, strategyID string) error
	Champions() []string
}

// NewPromoter creates a promoter with the given criteria.
func NewPromoter(criteria *PromotionCriteria) Promoter {
	return nil
}

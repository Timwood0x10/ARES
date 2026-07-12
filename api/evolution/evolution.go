// Package evolution provides the public API for strategy evolution, including
// the DreamCycle orchestrator, GA Population, mutation, and promotion subsystems.
package evolution

import (
	"context"
	"fmt"
	"time"

	evolve "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_evolution/experience"
	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	internalmutation "github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/ares_evolution/promotion"

	pubmutation "github.com/Timwood0x10/ares/api/evolution/mutation"
)

const paramKeyTemperature = "temperature"

// ---------------------------------------------------------------------------
// Strategy & Lineage
// ---------------------------------------------------------------------------

type Strategy struct {
	ID             string
	Version        int
	Score          float64
	ParentID       string
	PromptTemplate string
	Params         map[string]any
	MutationType   string
}

type Lineage struct {
	ParentID         string
	ChildID          string
	MutationType     string
	WinRate          float64
	ScoreImprovement float64
}

// ---------------------------------------------------------------------------
// DreamCycle
// ---------------------------------------------------------------------------

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

// CallbackData holds data passed to the dream cycle during evolution triggers.
type CallbackData struct {
	AgentID string
}

func DefaultDreamCycleConfig() DreamCycleConfig {
	return DreamCycleConfig{
		Enabled:              false,
		MinTasksBeforeEvolve: 10,
		MinScoreDrop:         0.15,
		MaxMutations:         3,
		MinWinRate:           0.55,
		Cooldown:             5 * time.Minute,
		TaskSampleSize:       50,
		QuickRejectRuns:      5,
	}
}

type DreamCycle interface {
	Run(ctx context.Context, data CallbackData) error
	SetEnabled(enabled bool)
	IsEnabled() bool
	TaskCount() int64
}

type dreamCycleAdapter struct {
	inner *evolve.DreamCycle
}

func (d *dreamCycleAdapter) Run(ctx context.Context, data CallbackData) error {
	return d.inner.Run(ctx, evolve.CallbackData{AgentID: data.AgentID})
}
func (d *dreamCycleAdapter) SetEnabled(enabled bool) {
	d.inner.SetEnabled(enabled)
}
func (d *dreamCycleAdapter) IsEnabled() bool {
	return d.inner.IsEnabled()
}
func (d *dreamCycleAdapter) TaskCount() int64 {
	return d.inner.TaskCount()
}

func NewDreamCycle(scheduler, mutator any, opts ...any) (DreamCycle, error) {
	// Caller provides wired internal components.
	sched := scheduler.(*evolve.EvolutionScheduler)
	mut := mutator.(evolve.MutatorInterface)
	inner, err := evolve.NewDreamCycle(sched, mut, nil, nil)
	if err != nil {
		return nil, err
	}
	return &dreamCycleAdapter{inner: inner}, nil
}

// ---------------------------------------------------------------------------
// Genome (GA Population)
// ---------------------------------------------------------------------------

type PopulationConfig struct {
	Size              int
	EliteCount        int
	MutationRate      float64
	SurvivalRate      float64
	SelectionStrategy string
	TournamentSize    int
	CrossoverType     string
}

func DefaultPopulationConfig() PopulationConfig {
	return PopulationConfig{
		Size:              20,
		EliteCount:        3,
		MutationRate:      0.2,
		SurvivalRate:      0.6,
		SelectionStrategy: "tournament",
		TournamentSize:    3,
		CrossoverType:     "uniform",
	}
}

type Population interface {
	Agents() []Agent
	Size() int
	CurrentGeneration() int
	BestScore() float64
	Evolve(ctx context.Context) error
}

type Agent struct {
	ID     string
	Score  float64
	Params map[string]any
}

type populationAdapter struct {
	inner *genome.Population
}

func (p *populationAdapter) Agents() []Agent {
	agents, _ := p.inner.Snapshot()
	out := make([]Agent, len(agents))
	for i, a := range agents {
		out[i] = Agent{ID: a.ID, Score: a.Score, Params: a.Params}
	}
	return out
}
func (p *populationAdapter) Size() int              { return p.inner.Size }
func (p *populationAdapter) CurrentGeneration() int { return p.inner.CurrentGeneration() }
func (p *populationAdapter) BestScore() float64     { return p.inner.BestEverScore() }
func (p *populationAdapter) Evolve(ctx context.Context) error {
	// Create a default mutator with basic parameter ranges.
	mut, err := internalmutation.NewMutator(
		internalmutation.WithParamRanges(defaultParamRanges()),
	)
	if err != nil {
		return fmt.Errorf("create mutator: %w", err)
	}
	// Create a default crossover.
	crosser, err := genome.NewCrossover(genome.WithSeed(42))
	if err != nil {
		return fmt.Errorf("create crossover: %w", err)
	}
	return p.inner.Evolve(ctx, mut, crosser)
}

// defaultParamRanges returns basic parameter ranges for public API users.
func defaultParamRanges() map[string]internalmutation.ParamRange {
	return map[string]internalmutation.ParamRange{
		paramKeyTemperature: {Values: []any{0.1, 0.3, 0.5, 0.7, 0.9}},
		"top_k":             {Values: []any{10, 20, 40, 60, 80, 100}},
		"max_tokens":        {Values: []any{1024, 2048, 4096, 8192}},
	}
}

func NewPopulation(base *Strategy, cfg PopulationConfig) (Population, error) {
	// Convert public Strategy to internal mutation.Strategy
	s := &internalmutation.Strategy{
		ID:     base.ID,
		Score:  base.Score,
		Params: base.Params,
	}

	// Build options from config.
	opts := []genome.PopulationOption{
		genome.WithPopulationSize(cfg.Size),
		genome.WithEliteCount(cfg.EliteCount),
		genome.WithMutationRate(cfg.MutationRate),
		genome.WithSurvivalRate(cfg.SurvivalRate),
		genome.WithSelectionStrategy(cfg.SelectionStrategy),
		genome.WithTournamentSelection(cfg.TournamentSize),
	}

	inner, err := genome.NewPopulation(context.Background(), s, nil, opts...)
	if err != nil {
		return nil, err
	}
	return &populationAdapter{inner: inner}, nil
}

// ---------------------------------------------------------------------------
// Mutation
// ---------------------------------------------------------------------------

type MutationConfig struct {
	ParamMutationProb  float64
	PromptMutationProb float64
}

type Mutator interface {
	Mutate(ctx context.Context, parent *Strategy) (*Strategy, error)
}

// NewMutator constructs a public Mutator by wrapping the internal mutation engine.
// The model parameter is reserved for future LLM-guided mutation and may be empty.
// If cfg is zero-valued, sensible defaults are used.
func NewMutator(model string, cfg MutationConfig) (Mutator, error) {
	paramRanges := map[string][]any{
		"temperature":        {0.1, 0.3, 0.5, 0.7, 0.9},
		"top_k":              {10, 20, 40, 80},
		"max_steps":          {5, 10, 15, 20},
		"memory_limit":       {3, 5, 10},
		"conflict_threshold": {0.85, 0.90, 0.95},
	}

	mutCfg := pubmutation.MutatorConfig{
		ParamRanges:        paramRanges,
		ParamMutationProb:  cfg.ParamMutationProb,
		PromptMutationProb: cfg.PromptMutationProb,
	}

	if mutCfg.ParamMutationProb <= 0 {
		mutCfg.ParamMutationProb = 0.3
	}
	if mutCfg.PromptMutationProb <= 0 {
		mutCfg.PromptMutationProb = 0.3
	}

	inner, err := pubmutation.NewMutator(mutCfg)
	if err != nil {
		return nil, fmt.Errorf("new mutator: %w", err)
	}

	return &mutatorAdapter{inner: inner}, nil
}

// mutatorAdapter wraps the public mutation.Mutator to implement the local Mutator interface.
type mutatorAdapter struct {
	inner *pubmutation.Mutator
}

func (a *mutatorAdapter) Mutate(ctx context.Context, parent *Strategy) (*Strategy, error) {
	if parent == nil {
		return nil, fmt.Errorf("parent strategy must not be nil")
	}

	pubStrat := &pubmutation.Strategy{
		ID:             parent.ID,
		Version:        parent.Version,
		Score:          parent.Score,
		ParentID:       parent.ParentID,
		PromptTemplate: parent.PromptTemplate,
		Params:         parent.Params,
	}

	child, err := a.inner.Mutate(ctx, pubStrat)
	if err != nil {
		return nil, fmt.Errorf("mutate: %w", err)
	}

	return &Strategy{
		ID:             child.ID,
		Version:        child.Version,
		Score:          child.Score,
		ParentID:       child.ParentID,
		PromptTemplate: child.PromptTemplate,
		Params:         child.Params,
		MutationType:   string(child.MutationType),
	}, nil
}

// ---------------------------------------------------------------------------
// Promotion
// ---------------------------------------------------------------------------

type PromotionCriteria struct {
	MinSampleCount     int
	MinSuccessRate     float64
	MinConfidence      float64
	ChampionHoldPeriod int
	DemotionThreshold  float64
	MaxChampionTenure  int
}

func DefaultPromotionCriteria() PromotionCriteria {
	return PromotionCriteria{
		MinSampleCount:     100,
		MinSuccessRate:     0.85,
		MinConfidence:      0.70,
		ChampionHoldPeriod: 5,
		DemotionThreshold:  0.30,
		MaxChampionTenure:  20,
	}
}

type Promoter interface {
	Evaluate(ctx context.Context, strategyID string, successRate, confidence float64) (string, error)
	Promote(ctx context.Context, strategyID string) error
	Demote(ctx context.Context, strategyID string) error
}

type promoterAdapter struct {
	inner *promotion.DefaultPromoter
}

func (p *promoterAdapter) Evaluate(ctx context.Context, strategyID string, successRate, confidence float64) (string, error) {
	ev := experience.Evidence{
		StrategyID:  strategyID,
		SuccessRate: successRate,
		Confidence:  confidence,
		ErrorRate:   1.0 - successRate,
		SampleCount: 1,
		LastUpdated: time.Now(),
	}

	state, reason, err := p.inner.Evaluate(ctx, strategyID, ev)
	if err != nil {
		return "", fmt.Errorf("promoter evaluate: %w", err)
	}
	return fmt.Sprintf("%s: %s", state, reason), nil
}
func (p *promoterAdapter) Promote(ctx context.Context, strategyID string) error {
	return p.inner.Promote(ctx, strategyID)
}
func (p *promoterAdapter) Demote(ctx context.Context, strategyID string) error {
	return p.inner.Demote(ctx, strategyID, "demoted by public API")
}

func NewPromoter(criteria *PromotionCriteria) Promoter {
	ic := promotion.DefaultPromotionCriteria()
	if criteria != nil {
		ic.MinSampleCount = criteria.MinSampleCount
		ic.MinSuccessRate = criteria.MinSuccessRate
		ic.MinConfidence = criteria.MinConfidence
		ic.ChampionHoldPeriod = criteria.ChampionHoldPeriod
		ic.DemotionThreshold = criteria.DemotionThreshold
		ic.MaxChampionTenure = criteria.MaxChampionTenure
	}
	return &promoterAdapter{inner: promotion.NewDefaultPromoter(ic)}
}

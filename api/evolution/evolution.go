// Package evolution provides the public API for strategy evolution, including
// the DreamCycle orchestrator, GA Population, mutation, and promotion subsystems.
package evolution

import (
	"context"
	"time"

	evolve "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/ares_evolution/promotion"
)

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
}

func DefaultPopulationConfig() PopulationConfig {
	return PopulationConfig{
		Size:         20,
		EliteCount:   3,
		MutationRate: 0.2,
		SurvivalRate: 0.6,
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
	return p.inner.EvolveOnIdle(ctx, nil, nil)
}

func NewPopulation(base *Strategy, cfg PopulationConfig) (Population, error) {
	// Convert public Strategy to internal mutation.Strategy
	s := &mutation.Strategy{
		ID:     base.ID,
		Score:  base.Score,
		Params: base.Params,
	}
	inner, err := genome.NewPopulation(context.Background(), s, nil,
		genome.WithPopulationSize(cfg.Size),
		genome.WithEliteCount(cfg.EliteCount),
		genome.WithMutationRate(cfg.MutationRate),
		genome.WithSurvivalRate(cfg.SurvivalRate),
	)
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

func NewMutator(model string, cfg MutationConfig) Mutator {
	return nil // returns nil — caller configures internal mutator directly
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
	return "", nil
}
func (p *promoterAdapter) Promote(ctx context.Context, strategyID string) error {
	return p.inner.Promote(ctx, strategyID)
}
func (p *promoterAdapter) Demote(ctx context.Context, strategyID string) error {
	return nil
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

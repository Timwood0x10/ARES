// Package ga provides a public API for genetic algorithm strategy evolution.
// It wraps internal/ares_evolution for use by external modules.
package ga

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// Strategy is a public representation of an evolvable strategy.
type Strategy struct {
	ID             string
	Params         map[string]any
	PromptTemplate string
	Score          float64
	ParentID       string
}

// ParamRange defines candidate values for a mutable parameter.
type ParamRange struct {
	Name   string
	Values []any
}

// PopulationConfig holds GA configuration.
type PopulationConfig struct {
	Size         int
	EliteCount   int
	MutationRate float64
	SurvivalRate float64
}

func DefaultPopulationConfig() PopulationConfig {
	return PopulationConfig{
		Size:         20,
		EliteCount:   3,
		MutationRate: 0.2,
		SurvivalRate: 0.6,
	}
}

// Mutator wraps mutation.Mutator.
type Mutator struct {
	inner *mutation.Mutator
}

// NewMutator creates a mutator with custom param ranges.
func NewMutator(ranges map[string]ParamRange) (*Mutator, error) {
	mr := make(map[string]mutation.ParamRange, len(ranges))
	for k, pr := range ranges {
		mr[k] = mutation.ParamRange{Name: pr.Name, Values: pr.Values}
	}
	inner, err := mutation.NewMutator(mutation.WithParamRanges(mr))
	if err != nil {
		return nil, fmt.Errorf("new mutator: %w", err)
	}
	return &Mutator{inner: inner}, nil
}

// Population wraps genome.Population with a public scorer.
type Population struct {
	inner   *genome.Population
	mut     *Mutator
	crosser *genome.Crossover
	scorer  func(*Strategy) float64
}

// NewPopulation creates a GA population with the given base strategy, mutator,
// and scorer. The scorer receives a public Strategy and returns a fitness score.
func NewPopulation(base *Strategy, cfg PopulationConfig, mut *Mutator, scorer func(*Strategy) float64) (*Population, error) {
	if base == nil {
		return nil, fmt.Errorf("base strategy must not be nil")
	}
	if mut == nil {
		return nil, fmt.Errorf("mutator must not be nil")
	}
	if scorer == nil {
		return nil, fmt.Errorf("scorer must not be nil")
	}

	internalBase := &mutation.Strategy{
		ID:             base.ID,
		Params:         base.Params,
		PromptTemplate: base.PromptTemplate,
		Score:          base.Score,
	}
	if internalBase.ID == "" {
		internalBase.ID = fmt.Sprintf("root-%d", time.Now().UnixNano())
	}

	inner, err := genome.NewPopulation(context.Background(), internalBase, mut.inner,
		genome.WithPopulationSize(cfg.Size),
		genome.WithEliteCount(cfg.EliteCount),
		genome.WithMutationRate(cfg.MutationRate),
		genome.WithSurvivalRate(cfg.SurvivalRate),
	)
	if err != nil {
		return nil, fmt.Errorf("new population: %w", err)
	}

	crosser, err := genome.NewCrossover()
	if err != nil {
		return nil, fmt.Errorf("new crossover: %w", err)
	}

	return &Population{inner: inner, mut: mut, crosser: crosser, scorer: scorer}, nil
}

// Evolve runs one GA generation: score all agents, evolve (selection/crossover/mutation),
// then re-score. Returns the best score after evolution.
func (p *Population) Evolve(ctx context.Context) (float64, error) {
	internalScorer := func(s *mutation.Strategy) float64 {
		pub := &Strategy{
			ID:             s.ID,
			Params:         s.Params,
			PromptTemplate: s.PromptTemplate,
			Score:          s.Score,
			ParentID:       s.ParentID,
		}
		return p.scorer(pub)
	}
	if err := p.inner.EvolveAfterScoring(ctx, internalScorer, p.mut.inner, p.crosser); err != nil {
		return 0, fmt.Errorf("evolve: %w", err)
	}
	return p.inner.BestEverScore(), nil
}

// Best returns the best strategy found so far (deep copy).
func (p *Population) Best() *Strategy {
	s := p.inner.BestStrategy()
	if s == nil {
		return nil
	}
	return &Strategy{
		ID:             s.ID,
		Params:         s.Params,
		PromptTemplate: s.PromptTemplate,
		Score:          s.Score,
		ParentID:       s.ParentID,
	}
}

// All returns all agents in the current population.
func (p *Population) All() []Strategy {
	agents, _ := p.inner.Snapshot()
	out := make([]Strategy, len(agents))
	for i, a := range agents {
		out[i] = Strategy{
			ID:             a.ID,
			Params:         a.Params,
			PromptTemplate: a.PromptTemplate,
			Score:          a.Score,
			ParentID:       a.ParentID,
		}
	}
	return out
}

// Snapshot returns all agents and the current generation number.
func (p *Population) Snapshot() ([]Strategy, int) {
	agents, gen := p.inner.Snapshot()
	out := make([]Strategy, len(agents))
	for i, a := range agents {
		out[i] = Strategy{
			ID:             a.ID,
			Params:         a.Params,
			PromptTemplate: a.PromptTemplate,
			Score:          a.Score,
			ParentID:       a.ParentID,
		}
	}
	return out, gen
}

// CurrentGeneration returns the current generation number.
func (p *Population) CurrentGeneration() int {
	return p.inner.CurrentGeneration()
}

// Size returns the target population size.
func (p *Population) Size() int {
	return p.inner.Size
}

// BestScore returns the best-ever fitness score.
func (p *Population) BestScore() float64 {
	return p.inner.BestEverScore()
}

package main

import (
	"sync"

	apievol "github.com/Timwood0x10/ares/api/ares_evolution"
)

// phaseScorer switches between two scorer functions at a given generation boundary.
// Generations 0..switchGen-1 use the early scorer; switchGen onwards use the late scorer.
// Generation is inferred from total scoring calls: gen = totalCalls / popSize.
type phaseScorer struct {
	early, late func(*apievol.Strategy) float64
	switchGen   int
	popSize     int
	totalCalls  int
	mu          sync.Mutex
}

// newPhaseScorer creates a phaseScorer.
// Args:
//   - early: scorer for early generations (typically deterministic).
//   - late: scorer for late generations (typically LLM-based). Nil means keep using early.
//   - switchGen: first generation index to use late scorer.
//   - popSize: population size per generation for call-to-gen conversion.
//
// Returns:
//   - *phaseScorer: ready to use via AsScorerFunc.
func newPhaseScorer(early func(*apievol.Strategy) float64, late func(*apievol.Strategy) float64, switchGen, popSize int) *phaseScorer {
	if popSize < 1 {
		popSize = 1
	}
	return &phaseScorer{
		early:     early,
		late:      late,
		switchGen: switchGen,
		popSize:   popSize,
	}
}

// Score evaluates a strategy using the active phase's scorer.
func (p *phaseScorer) Score(agent *apievol.Strategy) float64 {
	p.mu.Lock()
	p.totalCalls++
	gen := p.totalCalls / p.popSize
	useLate := p.late != nil && gen >= p.switchGen
	p.mu.Unlock()

	if useLate {
		return p.late(agent)
	}
	return p.early(agent)
}

// AsScorerFunc returns a ScorerFunc that delegates to this phaseScorer.
func (p *phaseScorer) AsScorerFunc() apievol.ScorerFunc {
	return func(agent *apievol.Strategy) float64 {
		return p.Score(agent)
	}
}

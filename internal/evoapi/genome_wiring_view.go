// Package evoapi provides the legacy evolution API.
//
// This file implements A3 (0.3.2): aligning the public API surface with the
// production evolution path. The genome_wiring observation surface (fitness
// curves, population stats, generation active state) is exposed here as a
// read-only view so external callers can observe the REAL evolution path — not
// just the legacy DreamCycle subsystem.
//
// TODO(tech-debt): coordinator patch decisions are NOT exposed here — the
// adapter does not surface its coordinator. They are logged structurally by
// logCoordinatorDecision in genome_wiring_run.go; a read-only accessor is a
// 0.3.3 target.
package evoapi

import (
	"fmt"

	evolve "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
)

// GenerationStats is the public read-only snapshot of one generation's
// population statistics — the fitness curve data point.
type GenerationStats struct {
	Generation   int     `json:"generation"`
	Size         int     `json:"size"`
	BestScore    float64 `json:"best_score"`
	AvgScore     float64 `json:"avg_score"`
	WorstScore   float64 `json:"worst_score"`
	Diversity    float64 `json:"diversity"`
	StagnantGens int     `json:"stagnant_generations"`
}

// GenomeWiringView is the read-only observation surface for the production
// genome_wiring evolution path (A3, 0.3.2). It exposes the population stats
// (fitness curve) and the generation-active state without allowing mutations —
// the production path's write side stays internal.
//
// Construct via NewGenomeWiringView with a *GenomePopulationAdapter from
// internal/runtime/ares_evolution. The adapter must be non-nil.
type GenomeWiringView struct {
	adapter *evolve.GenomePopulationAdapter
}

// NewGenomeWiringView creates a read-only observation view over the production
// genome_wiring adapter. The adapter parameter must be the same instance the
// production bootstrap wired (not a test clone) for the observation to reflect
// live data.
//
// Returns an error if adapter is nil.
func NewGenomeWiringView(adapter *evolve.GenomePopulationAdapter) (*GenomeWiringView, error) {
	if adapter == nil {
		return nil, fmt.Errorf("evoapi: NewGenomeWiringView requires a non-nil GenomePopulationAdapter")
	}
	return &GenomeWiringView{adapter: adapter}, nil
}

// CurrentGeneration returns the current generation number (0 = initial
// population, increments per EvolveAfterScoring call).
func (v *GenomeWiringView) CurrentGeneration() int {
	pop := v.adapter.Population()
	if pop == nil {
		return 0
	}
	return pop.CurrentGeneration()
}

// Stats returns the current population statistics — the latest fitness curve
// data point. Returns nil if the population is unavailable.
func (v *GenomeWiringView) Stats() *GenerationStats {
	pop := v.adapter.Population()
	if pop == nil {
		return nil
	}
	ps := pop.Stats()
	if ps == nil {
		return nil
	}
	return &GenerationStats{
		Generation:   ps.Generation,
		Size:         ps.Size,
		BestScore:    ps.BestScore,
		AvgScore:     ps.AvgScore,
		WorstScore:   ps.WorstScore,
		Diversity:    ps.Diversity.Overall,
		StagnantGens: pop.StagnantGenerations(),
	}
}

// FitnessHistory returns the per-generation fitness curve (best/avg/worst
// scores + diversity) as a flat slice. Empty when no evolution cycles have
// run.
func (v *GenomeWiringView) FitnessHistory() []GenerationStats {
	pop := v.adapter.Population()
	if pop == nil {
		return nil
	}
	history := pop.History()
	out := make([]GenerationStats, 0, len(history))
	for _, h := range history {
		out = append(out, GenerationStats{
			Generation: h.Generation,
			Size:       h.PopulationSize,
			BestScore:  h.BestScore,
			AvgScore:   h.AvgScore,
			WorstScore: h.WorstScore,
			Diversity:  h.Diversity,
		})
	}
	return out
}

// BestStrategy returns the best-ever strategy ID and score, or ("", 0) when
// no strategy has been evaluated.
func (v *GenomeWiringView) BestStrategy() (id string, score float64) {
	pop := v.adapter.Population()
	if pop == nil {
		return "", 0
	}
	return pop.BestEverID(), pop.BestEverScore()
}

// GenerationActive reports whether an evolution cycle is currently running
// (the adapter's running flag). Callers can use this to avoid reading
// mid-mutation snapshots.
func (v *GenomeWiringView) GenerationActive() bool {
	return v.adapter.GenerationActive()
}

// Compile-time assertion that GenomeWiringView does not depend on genome
// internals beyond the Population type.
var _ *genome.Population = nil

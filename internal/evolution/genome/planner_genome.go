//nolint:gosec // GA mutation intentionally uses math/rand (performance, not crypto).
package genome

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"fmt"
	"math/rand"

	"github.com/Timwood0x10/ares/internal/evidence"
)

const (
	// PlannerGenomeName is the registry name for the planner genome.
	PlannerGenomeName = "planner"
)

// PlannerGenomeConfig controls the knowledge planning strategy parameters.
type PlannerGenomeConfig struct {
	// Strategy selects the planning approach: balanced / architecture-first / memory-first.
	Strategy string

	// MaxSources limits the number of knowledge sources per plan.
	MaxSources int

	// MinRelevance sets the minimum relevance threshold for sources [0.0, 1.0].
	MinRelevance float64

	// EvidenceStore provides planning quality evidence for fitness evaluation.
	// May be nil; fitness falls back to a constant when nil.
	EvidenceStore *evidence.MemoryStore
}

// DefaultPlannerGenomeConfig returns sensible default planner parameters.
func DefaultPlannerGenomeConfig() PlannerGenomeConfig {
	return PlannerGenomeConfig{
		Strategy:     plannerBalanced,
		MaxSources:   10,
		MinRelevance: 0.5,
	}
}

// PlannerGenome evolves the knowledge planning strategy.
//
// Mutation changes:
//   - Strategy: balanced / architecture-first / memory-first
//   - MaxSources: 3–30
//   - MinRelevance: 0.1–0.9
type PlannerGenome struct {
	config PlannerGenomeConfig
}

// NewPlannerGenome creates a new PlannerGenome.
func NewPlannerGenome(config PlannerGenomeConfig) *PlannerGenome {
	return &PlannerGenome{config: config}
}

// Name returns the genome identifier.
func (g *PlannerGenome) Name() string { return PlannerGenomeName }

// Config returns the current config. Used by the Diff Engine.
func (g *PlannerGenome) Config() PlannerGenomeConfig { return g.config }

// Mutate generates n candidate genomes with one random parameter change each.
func (g *PlannerGenome) Mutate(_ context.Context, n int) ([]Genome, error) {
	if n <= 0 {
		return nil, nil
	}
	children := make([]Genome, 0, n)
	for i := 0; i < n; i++ {
		child := g.clone()
		switch rand.Intn(3) {
		case 0:
			child.mutateStrategy()
		case 1:
			child.mutateMaxSources()
		case 2:
			child.mutateMinRelevance()
		}
		children = append(children, child)
	}
	return children, nil
}

// Crossover recombines this genome with another to produce a child.
func (g *PlannerGenome) Crossover(_ context.Context, other Genome) (Genome, error) {
	otherPG, ok := other.(*PlannerGenome)
	if !ok {
		return nil, fmt.Errorf("planner: crossover incompatible genome type %T", other)
	}
	child := g.clone()
	if rand.Float64() < 0.5 {
		child.config.Strategy = otherPG.config.Strategy
	}
	if rand.Float64() < 0.5 {
		child.config.MaxSources = otherPG.config.MaxSources
	}
	if rand.Float64() < 0.5 {
		child.config.MinRelevance = otherPG.config.MinRelevance
	}
	return child, nil
}

// Fitness evaluates this genome's quality based on planning evidence.
func (g *PlannerGenome) Fitness(ctx context.Context) (float64, error) {
	if g.config.EvidenceStore == nil {
		return 0.5, nil
	}
	evs, err := g.config.EvidenceStore.Query(ctx, evidence.Filter{
		Source: PlannerGenomeName,
		Limit:  50,
	})
	if err != nil {
		return 0.0, fmt.Errorf("planner: query evidence: %w", err)
	}
	if len(evs) == 0 {
		return 0.5, nil
	}

	// Heuristic: balanced strategy gets baseline, extreme strategies are penalised.
	baseFit := 0.7
	switch g.config.Strategy {
	case plannerBalanced:
		baseFit = 0.8
	case plannerArchFirst:
		baseFit = 0.6
	case plannerMemoryFirst:
		baseFit = 0.6
	}

	// Penalise extreme MaxSources.
	srcPenalty := float64(g.config.MaxSources) / 50.0
	if srcPenalty > 0.3 {
		srcPenalty = 0.3
	}
	fitness := baseFit - srcPenalty

	// Emit fitness evidence.
	if g.config.EvidenceStore != nil {
		_ = g.config.EvidenceStore.Append(ctx, evidence.NewEvidence(
			PlannerGenomeName,
			evidence.KindFitness,
			fitness,
			evidence.WithMetadata("strategy", g.config.Strategy),
			evidence.WithMetadata("max_sources", fmt.Sprintf("%d", g.config.MaxSources)),
		))
	}

	return fitness, nil
}

// Snapshot returns the current config as the serializable state.
func (g *PlannerGenome) Snapshot(_ context.Context) (any, error) {
	return g.config, nil
}

// ── Mutation implementations ─────────────────

func (g *PlannerGenome) mutateStrategy() {
	strategies := []string{plannerBalanced, plannerArchFirst, plannerMemoryFirst}
	g.config.Strategy = strategies[rand.Intn(len(strategies))]
}

func (g *PlannerGenome) mutateMaxSources() {
	delta := rand.Intn(11) - 5 // [-5, 5]
	g.config.MaxSources += delta
	if g.config.MaxSources < 3 {
		g.config.MaxSources = 3
	}
	if g.config.MaxSources > 30 {
		g.config.MaxSources = 30
	}
}

func (g *PlannerGenome) mutateMinRelevance() {
	delta := (rand.Float64() * 0.4) - 0.2 // [-0.2, 0.2]
	g.config.MinRelevance += delta
	if g.config.MinRelevance < 0.1 {
		g.config.MinRelevance = 0.1
	}
	if g.config.MinRelevance > 0.9 {
		g.config.MinRelevance = 0.9
	}
}

func (g *PlannerGenome) clone() *PlannerGenome {
	cfg := g.config
	return &PlannerGenome{config: cfg}
}

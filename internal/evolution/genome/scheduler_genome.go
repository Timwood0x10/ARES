//nolint:gosec // GA mutation intentionally uses math/rand (performance, not crypto).
package genome

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/workflow/graph"
)

// SchedulerFactory creates a Scheduler instance.
type SchedulerFactory func() graph.Scheduler

// schedulerDescriptor describes a schedulder in the candidate pool.
type schedulerDescriptor struct {
	name    string
	factory SchedulerFactory
}

// SchedulerGenome evolves the scheduler selection for graph node scheduling.
//
// Mutation picks a random scheduler from the candidate pool.
// The pool includes: DefaultScheduler, PriorityScheduler, ShortJobScheduler,
// RoundRobinScheduler, and WeightedFairScheduler.
type SchedulerGenome struct {
	current    graph.Scheduler
	candidates []schedulerDescriptor
	config     SchedulerGenomeConfig
}

// SchedulerGenomeConfig controls the scheduler evolution behavior.
type SchedulerGenomeConfig struct {
	// EvidenceStore provides scheduling performance evidence for fitness evaluation.
	// May be nil; fitness falls back to a constant when nil.
	EvidenceStore *evidence.MemoryStore
}

// DefaultSchedulerGenomeConfig returns a sensible default configuration.
func DefaultSchedulerGenomeConfig() SchedulerGenomeConfig {
	return SchedulerGenomeConfig{}
}

// NewSchedulerGenome creates a new SchedulerGenome with the given current scheduler
// and candidate scheduler factories.
func NewSchedulerGenome(current graph.Scheduler, config SchedulerGenomeConfig) *SchedulerGenome {
	return &SchedulerGenome{
		current:    current,
		candidates: defaultSchedulerCandidates(),
		config:     config,
	}
}

// NewSchedulerGenomeWithPool creates a SchedulerGenome with a custom candidate pool.
func NewSchedulerGenomeWithPool(current graph.Scheduler, candidates []SchedulerFactory, config SchedulerGenomeConfig) *SchedulerGenome {
	descs := make([]schedulerDescriptor, len(candidates))
	for i, f := range candidates {
		descs[i] = schedulerDescriptor{name: fmt.Sprintf("custom-%d", i), factory: f}
	}
	return &SchedulerGenome{
		current:    current,
		candidates: descs,
		config:     config,
	}
}

// Name returns the genome identifier.
func (g *SchedulerGenome) Name() string { return SchedulerGenomeName }

// Current returns the current scheduler. Used by the Diff Engine.
func (g *SchedulerGenome) Current() graph.Scheduler { return g.current }

// CandidateNames returns the names of all candidate schedulers.
func (g *SchedulerGenome) CandidateNames() []string {
	names := make([]string, len(g.candidates))
	for i, c := range g.candidates {
		names[i] = c.name
	}
	return names
}

// Mutate generates n candidate genomes by randomly selecting from the scheduler pool.
func (g *SchedulerGenome) Mutate(_ context.Context, n int) ([]Genome, error) {
	if n <= 0 {
		return nil, nil
	}

	children := make([]Genome, 0, n)
	for i := 0; i < n; i++ {
		child := g.clone()
		child.mutateScheduler()
		children = append(children, child)
	}
	return children, nil
}

// Crossover recombines this genome with another to produce a child.
func (g *SchedulerGenome) Crossover(_ context.Context, other Genome) (Genome, error) {
	otherSG, ok := other.(*SchedulerGenome)
	if !ok {
		return nil, fmt.Errorf("scheduler: crossover incompatible genome type %T", other)
	}

	child := g.clone()
	if rand.Float64() < 0.5 {
		child.current = otherSG.current
	}
	return child, nil
}

// Fitness evaluates the scheduler quality based on execution evidence.
func (g *SchedulerGenome) Fitness(ctx context.Context) (float64, error) {
	if g.config.EvidenceStore == nil {
		return 0.5, nil
	}

	evs, err := g.config.EvidenceStore.Query(ctx, evidence.Filter{
		Source: "scheduler",
		Limit:  100,
	})
	if err != nil {
		return 0.0, fmt.Errorf("scheduler: query evidence: %w", err)
	}

	if len(evs) == 0 {
		return 0.5, nil
	}

	var sum float64
	var count int
	for _, ev := range evs {
		if len(ev.Payload) > 0 {
			var v float64
			if err := json.Unmarshal(ev.Payload, &v); err == nil {
				sum += v
				count++
			}
		}
	}
	if count == 0 {
		return 0.5, nil
	}
	fitness := sum / float64(count)

	// Emit fitness evidence.
	_ = g.config.EvidenceStore.Append(ctx, evidence.NewEvidence(
		"scheduler",
		evidence.KindFitness,
		fitness,
		evidence.WithMetadata("type", "scheduler"),
	))

	return fitness, nil
}

// Snapshot returns the current scheduler. Used by the Diff Engine.
func (g *SchedulerGenome) Snapshot(_ context.Context) (any, error) {
	return g.current, nil
}

// ── Mutation implementations ─────────────────

func (g *SchedulerGenome) mutateScheduler() {
	if len(g.candidates) == 0 {
		return
	}
	desc := g.candidates[rand.Intn(len(g.candidates))]
	g.current = desc.factory()
}

// clone creates a deep copy of the SchedulerGenome.
func (g *SchedulerGenome) clone() *SchedulerGenome {
	return &SchedulerGenome{
		current:    g.current,
		candidates: g.candidates,
		config:     g.config,
	}
}

// defaultSchedulerCandidates returns the built-in scheduler pool.
func defaultSchedulerCandidates() []schedulerDescriptor {
	return []schedulerDescriptor{
		{name: "DefaultScheduler", factory: func() graph.Scheduler { return graph.NewDefaultScheduler() }},
		{name: "RoundRobinScheduler", factory: func() graph.Scheduler { return graph.NewRoundRobinScheduler() }},
	}
}

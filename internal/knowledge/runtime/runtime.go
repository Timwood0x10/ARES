// Package runtime provides the KnowledgeRuntime — the core execution engine
// of AKF. It orchestrates the Plan → Load → Link → Reduce → Lazy Graph pipeline.
package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
)

// KnowledgeRuntime is the central execution engine of AKF.
// It orchestrates Plan → Load → Link → Reduce → Graph.
type KnowledgeRuntime struct {
	planner   planner.KnowledgePlanner
	discovery planner.SourceDiscovery
	registry  *provider.ProviderRegistry
	pipeline  *knowledge.KnowledgePipeline
	linkers   []Linker
	reducers  []Reducer
}

// New creates a KnowledgeRuntime with the given components.
func New(
	p planner.KnowledgePlanner,
	d planner.SourceDiscovery,
	reg *provider.ProviderRegistry,
	pipe *knowledge.KnowledgePipeline,
	linkers []Linker,
	reducers []Reducer,
) *KnowledgeRuntime {
	return &KnowledgeRuntime{
		planner:   p,
		discovery: d,
		registry:  reg,
		pipeline:  pipe,
		linkers:   linkers,
		reducers:  reducers,
	}
}

// Config holds optional runtime configuration.
type Config struct {
	MaxConcurrentProviders int  // Max parallel provider loads (default 5)
	LazyLoading            bool // Enable lazy graph mode (default false)
}

// Execute runs the full AKF pipeline: Plan → Load → Link → Reduce → Graph.
func (r *KnowledgeRuntime) Execute(ctx context.Context, goal string, budget knowledge.TokenBudget, cfg *Config) (*knowledge.WorkingGraph, error) {
	if cfg == nil {
		cfg = &Config{MaxConcurrentProviders: 5}
	}
	if cfg.MaxConcurrentProviders <= 0 {
		cfg.MaxConcurrentProviders = 5
	}

	// 1. Plan: generate knowledge requirements.
	plan, err := r.planner.Plan(ctx, goal, budget)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	if len(plan.Requirements) == 0 {
		return nil, fmt.Errorf("plan: no requirements generated for goal %q", goal)
	}

	// 2. Discover: map requirements to providers.
	sources, err := r.discovery.Discover(ctx, plan.Requirements, budget)
	if err != nil {
		return nil, fmt.Errorf("discover: %w", err)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("discover: no providers matched requirements")
	}

	// 3. Load & Pipeline: stream from providers, normalize, resolve, summarize.
	objects, err := r.loadAndProcess(ctx, sources, cfg)
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}

	// 4. Link: generate relations between objects.
	edges, err := r.link(ctx, objects)
	if err != nil {
		return nil, fmt.Errorf("link: %w", err)
	}

	// 5. Reduce: prune and compress to fit budget.
	graph := &knowledge.WorkingGraph{Nodes: objects, Edges: edges}
	graph, err = r.reduce(ctx, graph, budget)
	if err != nil {
		return nil, fmt.Errorf("reduce: %w", err)
	}

	return graph, nil
}

// loadAndProcess streams objects from all selected providers concurrently,
// runs the KnowledgePipeline on each object, and collects results.
func (r *KnowledgeRuntime) loadAndProcess(ctx context.Context, sources []planner.PlannedSource, cfg *Config) (map[string]*knowledge.KnowledgeObject, error) {
	objects := make(map[string]*knowledge.KnowledgeObject)
	var mu sync.Mutex

	sem := make(chan struct{}, cfg.MaxConcurrentProviders)
	var wg sync.WaitGroup
	errCh := make(chan error, len(sources))

	for _, src := range sources {
		prov := r.registry.Get(src.ProviderName)
		if prov == nil {
			log.Warn("provider not found (skipping)", "provider", src.ProviderName)
			continue
		}

		sem <- struct{}{}
		wg.Add(1)

		go func(src planner.PlannedSource, prov provider.GraphProvider) {
			defer func() { <-sem; wg.Done() }()

			intent := knowledge.Intent{
				Goal: src.Requirement.Description,
				Scope: knowledge.Scope{
					MaxObjects: src.MaxResults,
				},
			}

			objCh, errCh := prov.Stream(ctx, intent)
			for obj := range objCh {
				if ctx.Err() != nil {
					return
				}

				// Run through pipeline.
				if r.pipeline != nil {
					var pErr error
					obj, pErr = r.pipeline.Process(ctx, obj)
					if pErr != nil {
						continue
					}
				}

				mu.Lock()
				objects[obj.ID] = obj
				mu.Unlock()
			}

			// Check stream error.
			select {
			case sErr := <-errCh:
				if sErr != nil {
					log.Warn("provider stream error (partial data may remain)", "provider", src.ProviderName, "error", sErr)
				}
			default:
			}
		}(src, prov)
	}

	wg.Wait()
	close(errCh)

	if len(objects) == 0 {
		return nil, fmt.Errorf("load: no objects loaded from any provider")
	}

	return objects, nil
}

// link runs all linkers to generate relations between objects.
func (r *KnowledgeRuntime) link(ctx context.Context, objects map[string]*knowledge.KnowledgeObject) ([]knowledge.Relation, error) {
	if len(r.linkers) == 0 {
		return nil, nil
	}

	objList := make([]*knowledge.KnowledgeObject, 0, len(objects))
	for _, obj := range objects {
		objList = append(objList, obj)
	}

	var allEdges []knowledge.Relation
	for _, l := range r.linkers {
		edges, err := l.Link(ctx, objList)
		if err != nil {
			log.Warn("linker failed (skipping)", "linker", l.Name(), "error", err)
			continue
		}
		allEdges = append(allEdges, edges...)
	}
	return allEdges, nil
}

// reduce runs reducers in sequence to prune and compress the graph.
func (r *KnowledgeRuntime) reduce(ctx context.Context, graph *knowledge.WorkingGraph, budget knowledge.TokenBudget) (*knowledge.WorkingGraph, error) {
	current := graph
	for _, red := range r.reducers {
		var err error
		current, err = red.Reduce(ctx, current, budget)
		if err != nil {
			log.Warn("reducer failed (skipping)", "reducer", red.Name(), "error", err)
			continue
		}
	}
	return current, nil
}

// Package runtime provides the KnowledgeRuntime — the core execution engine
// of AKF. It orchestrates the Plan → Load → Link → Reduce → Lazy Graph pipeline.
package runtime

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/pipeline"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	"golang.org/x/sync/errgroup"
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

	patchRegMu sync.RWMutex
	patchReg   *patch.Registry

	evStore evidence.Store      // optional: unified Evidence Store
	evColl  *evidence.Collector // optional: evidence emitter
}

// New creates a KnowledgeRuntime with the given components.
// If pipe is nil, a default KnowledgePipeline with DefaultNormalizer,
// DefaultEntityMatcher, DefaultValidator, and DefaultSummarizer is created.
func New(
	p planner.KnowledgePlanner,
	d planner.SourceDiscovery,
	reg *provider.ProviderRegistry,
	pipe *knowledge.KnowledgePipeline,
	linkers []Linker,
	reducers []Reducer,
) *KnowledgeRuntime {
	if pipe == nil {
		pipe = knowledge.NewKnowledgePipeline(
			[]knowledge.Normalizer{&pipeline.DefaultNormalizer{MaxRawBytes: 10240}},
			[]knowledge.EntityMatcher{&pipeline.DefaultEntityMatcher{MatchThreshold: 0.6}},
			[]knowledge.Validator{&pipeline.DefaultValidator{}},
			[]knowledge.Summarizer{&pipeline.DefaultSummarizer{MaxSummaryLen: 200}},
		)
	}
	return &KnowledgeRuntime{
		planner:   p,
		discovery: d,
		registry:  reg,
		pipeline:  pipe,
		linkers:   linkers,
		reducers:  reducers,
	}
}

// WithPatchRegistry sets the runtime's patch registry for dynamic knowledge config changes.
func (r *KnowledgeRuntime) WithPatchRegistry(pr *patch.Registry) *KnowledgeRuntime {
	r.patchRegMu.Lock()
	defer r.patchRegMu.Unlock()
	r.patchReg = pr
	return r
}

// WithEvidenceStore sets the runtime's evidence store for emitting AKF insights.
func (r *KnowledgeRuntime) WithEvidenceStore(store evidence.Store) *KnowledgeRuntime {
	r.evStore = store
	if store != nil {
		r.evColl = evidence.NewCollector(store, "akf")
	}
	return r
}

// Pipeline returns the runtime's shared KnowledgePipeline. Producers of
// KnowledgeObjects outside the runtime (e.g. the Conversation Compiler) reuse
// this exact instance so their processing stays consistent with the rest of
// AKG and shares the same entity-resolution candidate pool. It is never nil:
// New guarantees a default pipeline when none is supplied. A nil receiver
// returns nil so callers can guard against an uninitialized runtime.
func (r *KnowledgeRuntime) Pipeline() *knowledge.KnowledgePipeline {
	if r == nil {
		return nil
	}
	return r.pipeline
}

// RegisterProvider adds a GraphProvider to the runtime's discovery registry so
// its objects are included in future Execute calls. Producers that persist
// KnowledgeObjects into a store (e.g. the Conversation Compiler) register a
// store-backed provider here to make that knowledge flow into the AKG
// retrieval path instead of sitting in a dead-end cache. The provider is
// registered under its Name; a duplicate name is rejected by the registry.
func (r *KnowledgeRuntime) RegisterProvider(p provider.GraphProvider) error {
	if r == nil || r.registry == nil {
		return fmt.Errorf("runtime: registry is not configured")
	}
	if p == nil {
		return fmt.Errorf("runtime: provider is nil")
	}
	return r.registry.Register(p)
}

// Config holds optional runtime configuration.
type Config struct {
	MaxConcurrentProviders int  // Max parallel provider loads (default 5)
	LazyLoading            bool // Enable lazy graph mode (default false)
}

// Execute runs the full AKF pipeline: Plan → Load → Link → Reduce → Graph.
func (r *KnowledgeRuntime) Execute(ctx context.Context, goal string, budget knowledge.TokenBudget, cfg *Config) (*knowledge.WorkingGraph, error) {
	if r == nil || r.planner == nil {
		return nil, fmt.Errorf("runtime: planner is not configured")
	}
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

	// If lazy loading is requested, log guidance. Full lazy graph support
	// requires API changes (returns LazyGraph instead of WorkingGraph).
	// TODO: implement lazy graph execution path (expected 2026-08).
	if cfg.LazyLoading {
		log.Info("lazy loading requested but not yet implemented; returning full graph",
			"nodes", len(graph.Nodes),
			"budget", budget.ForGraph)
	}

	// Emit insight evidence to the unified Evidence Store.
	if r.evColl != nil {
		_ = r.evColl.EmitWithMeta(ctx, evidence.KindInsight,
			map[string]any{
				"goal":        goal,
				"node_count":  len(graph.Nodes),
				"edge_count":  len(graph.Edges),
				"budget_used": budget.ForGraph,
			},
			"goal", goal,
		)
	}

	return graph, nil
}

// loadAndProcess streams objects from all selected providers concurrently,
// runs the KnowledgePipeline on each object, and collects results.
// Uses errgroup for goroutine lifecycle management (§4.5: no bare goroutines).
func (r *KnowledgeRuntime) loadAndProcess(ctx context.Context, sources []planner.PlannedSource, cfg *Config) (map[string]*knowledge.KnowledgeObject, error) {
	objects := make(map[string]*knowledge.KnowledgeObject)
	var mu sync.Mutex

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.MaxConcurrentProviders)

	for _, src := range sources {
		if ctx.Err() != nil {
			break
		}

		prov := r.registry.Get(src.ProviderName)
		if prov == nil {
			log.Warn("provider not found (skipping)", "provider", src.ProviderName)
			continue
		}

		src, prov := src, prov // capture loop vars
		g.Go(func() error {
			intent := knowledge.Intent{
				Goal: src.Requirement.Description,
				Scope: knowledge.Scope{
					MaxObjects: src.MaxResults,
				},
			}
			if src.Query != nil && src.Query.Query != "" {
				intent.Goal = src.Query.Query
			}

			objCh, streamErrCh := prov.Stream(ctx, intent)
		loop:
			for {
				select {
				case obj, ok := <-objCh:
					if !ok {
						break loop
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
				case <-ctx.Done():
					// Context cancelled; drain remaining objects so the
					// producer goroutine can exit instead of blocking on
					// the send forever (goroutine leak fix).
					for range objCh {
					}
					break loop
				}
			}

			// Check stream error.
			select {
			case sErr := <-streamErrCh:
				if sErr != nil {
					log.Warn("provider stream error (partial data may remain)", "provider", src.ProviderName, "error", sErr)
				}
			default:
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}

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

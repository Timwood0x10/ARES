package evolution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ares_evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	"golang.org/x/sync/errgroup"
)

// StrategyStore is the interface we need from the evolution system.
// It matches ares_evolution.StrategyStore, avoiding direct import coupling.
type StrategyStore interface {
	GetActive(ctx context.Context) (*ares_evolution.Strategy, error)
	GetHistory(ctx context.Context, id string, n int) ([]*ares_evolution.Strategy, error)
}

// EvolutionProvider wraps an evolution StrategyStore as a GraphProvider.
// It streams active and historical strategies as decision-type KnowledgeObjects,
// enabling the AKF knowledge graph to include evolution context.
type EvolutionProvider struct {
	name  string
	store StrategyStore
	ns    string
}

// New creates an EvolutionProvider.
func New(name string, store StrategyStore) *EvolutionProvider {
	ns := name
	if ns == "" {
		ns = "evolution"
	}
	return &EvolutionProvider{name: name, store: store, ns: ns}
}

// Name returns the provider identifier.
func (p *EvolutionProvider) Name() string { return p.name }

// ProviderType returns the backing data source type for query-planning routing.
func (p *EvolutionProvider) ProviderType() provider.ProviderType { return provider.ProviderEvolution }

// Compile-time guard that EvolutionProvider satisfies TypedProvider.
var _ provider.TypedProvider = (*EvolutionProvider)(nil)

// IntentMatch returns 0.9 for decision/evolution intents, 0.3 otherwise.
func (p *EvolutionProvider) IntentMatch(intent knowledge.Intent) float64 {
	goal := strings.ToLower(intent.Goal)
	if goal == "" {
		return 0.3
	}
	for _, kw := range []string{"decision", "history", "evolution", "strategy",
		"why", "reason", "rationale", "improve", "optimize"} {
		if strings.Contains(goal, kw) {
			return 0.9
		}
	}
	return 0.3
}

// Stream loads active and historical strategies and emits them as KnowledgeObjects.
func (p *EvolutionProvider) Stream(ctx context.Context, intent knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	objCh := make(chan *knowledge.KnowledgeObject, 32)
	errCh := make(chan error, 1)

	// Use errgroup for structured concurrency so the streaming goroutine is
	// ctx-cancelable. The errgroup is not waited on here; callers observe
	// completion via objCh/errCh being closed.
	g, gCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		defer close(objCh)
		defer close(errCh)

		// Check context before doing any work.
		if gCtx.Err() != nil {
			return nil
		}

		limit := intent.Scope.MaxObjects
		if limit <= 0 {
			limit = 20
		}

		// Emit active strategy first.
		active, err := p.store.GetActive(gCtx)
		if err != nil {
			if !errors.Is(err, ares_evolution.ErrNoActiveStrategy) {
				errCh <- fmt.Errorf("evolution provider %q: get active: %w", p.name, err)
				return nil
			}
			active = nil
		}
		if active != nil {
			obj := adapter.FromStrategy(active, p.ns)
			if obj != nil {
				select {
				case objCh <- obj:
				case <-gCtx.Done():
					return nil
				}
				limit--
			}
		}

		if limit <= 0 {
			return nil
		}

		// Emit historical strategies from the active strategy's lineage.
		if active != nil {
			history, hErr := p.store.GetHistory(gCtx, active.ID, limit)
			if hErr == nil {
				for _, s := range history {
					if s.Version == active.Version {
						continue // skip the active one (already emitted)
					}
					obj := adapter.FromStrategy(s, p.ns)
					if obj != nil {
						select {
						case objCh <- obj:
						case <-gCtx.Done():
							return nil
						}
					}
				}
			}
		}
		return nil
	})

	return objCh, errCh
}

// Compile-time interface check.
var _ provider.GraphProvider = (*EvolutionProvider)(nil)

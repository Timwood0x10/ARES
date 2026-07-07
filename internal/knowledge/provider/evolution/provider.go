// Package evolution provides a GraphProvider that reads from the evolution
// system's StrategyStore and streams strategies as KnowledgeObjects.
package evolution

import (
	"context"
	"fmt"
	"strings"

	ares_evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
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

	go func() {
		defer close(objCh)
		defer close(errCh)

		// Check context before doing any work.
		if ctx.Err() != nil {
			return
		}

		limit := intent.Scope.MaxObjects
		if limit <= 0 {
			limit = 20
		}

		// Emit active strategy first.
		active, err := p.store.GetActive(ctx)
		if err != nil {
			errCh <- fmt.Errorf("evolution provider %q: get active: %w", p.name, err)
			return
		}
		if active != nil {
			obj := adapter.FromStrategy(active, p.ns)
			if obj != nil {
				select {
				case objCh <- obj:
				case <-ctx.Done():
					return
				}
				limit--
			}
		}

		if limit <= 0 {
			return
		}

		// Emit historical strategies from the active strategy's lineage.
		if active != nil {
			history, hErr := p.store.GetHistory(ctx, active.ID, limit)
			if hErr == nil {
				for _, s := range history {
					if s.Version == active.Version {
						continue // skip the active one (already emitted)
					}
					obj := adapter.FromStrategy(s, p.ns)
					if obj != nil {
						select {
						case objCh <- obj:
						case <-ctx.Done():
							return
						}
					}
				}
			}
		}
	}()

	return objCh, errCh
}

// Compile-time interface check.
var _ provider.GraphProvider = (*EvolutionProvider)(nil)

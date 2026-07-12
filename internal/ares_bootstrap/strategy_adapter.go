// Package ares_bootstrap wires the runtime components together.
package ares_bootstrap

import (
	"context"

	"github.com/Timwood0x10/ares/internal/agents"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
)

// evolutionStrategySource adapts an evolution.StrategyStore to the
// agents.StrategySource contract, keeping the agents package decoupled from
// the evolution engine internals.
type evolutionStrategySource struct {
	store evolution.StrategyStore
}

// NewStrategySource wraps an evolution StrategyStore as an agents.StrategySource.
// Returns nil when the store is nil so callers can skip injection safely.
func NewStrategySource(store evolution.StrategyStore) agents.StrategySource {
	if store == nil {
		return nil
	}
	return &evolutionStrategySource{store: store}
}

var _ agents.StrategySource = (*evolutionStrategySource)(nil)

// GetActiveStrategy returns the active evolution strategy in the agents runtime view.
func (s *evolutionStrategySource) GetActiveStrategy(ctx context.Context) (*agents.ActiveStrategy, error) {
	st, err := s.store.GetActive(ctx)
	if err != nil {
		return nil, err
	}
	return toActiveStrategy(st), nil
}

// toActiveStrategy converts an evolution strategy into the agents runtime view.
// A nil input yields a nil output (no active strategy deployed yet).
func toActiveStrategy(st *evolution.Strategy) *agents.ActiveStrategy {
	if st == nil {
		return nil
	}
	return &agents.ActiveStrategy{
		ID:     st.ID,
		Prompt: st.PromptTemplate,
		Params: st.Params,
	}
}

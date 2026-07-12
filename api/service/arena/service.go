// Package arena provides the public API for chaos engineering operations.
package arena

import (
	"context"

	internal "github.com/Timwood0x10/ares/internal/ares_arena"
	"github.com/Timwood0x10/ares/internal/evidence"
)

// Service wraps internal/ares_arena.Service for public consumption.
type Service struct {
	inner *internal.Service
}

// New creates a new Arena service with the given injector and event store.
func New(injector *internal.Injector, store internal.EventStore, evStore evidence.Store) *Service {
	inner := internal.NewService(injector, store, evStore)
	return &Service{inner: inner}
}

// SetEvolutionBridge attaches an evolution bridge for chaos→Coordinator integration.
func (s *Service) SetEvolutionBridge(b *internal.EvolutionBridge) {
	s.inner.SetEvolutionBridge(b)
}

// Execute runs a chaos engineering action.
func (s *Service) Execute(ctx context.Context, action Action) Result {
	ia := internal.Action{
		ID:        action.ID,
		Type:      internal.ActionType(action.Type),
		TargetID:  action.TargetID,
		SourceID:  action.SourceID,
		Metadata:  action.Metadata,
		CreatedAt: action.CreatedAt,
	}
	r := s.inner.Execute(ctx, ia)
	return Result{
		Success:  r.Success,
		Action:   toPublicAction(r.Action),
		Error:    r.Error,
		Duration: r.Duration,
	}
}

// History returns the action history.
func (s *Service) History() []Result {
	results := s.inner.History()
	out := make([]Result, len(results))
	for i, r := range results {
		out[i] = Result{
			Success:  r.Success,
			Action:   toPublicAction(r.Action),
			Error:    r.Error,
			Duration: r.Duration,
		}
	}
	return out
}

// Stats returns aggregate statistics.
func (s *Service) Stats() Stats {
	st := s.inner.Stats()
	return Stats{
		TotalActions:      st.TotalActions,
		SuccessfulActions: st.SuccessfulActions,
		FailedActions:     st.FailedActions,
		LastAction:        st.LastAction,
	}
}

// Metrics returns snapshot metrics.
func (s *Service) Metrics() MetricsSnapshot {
	m := s.inner.Metrics()
	stats := make(map[string]ActionMetric, len(m.ActionStats))
	for k, v := range m.ActionStats {
		stats[k] = ActionMetric{
			Total:       v.Total,
			Success:     v.Success,
			Failed:      v.Failed,
			AvgDuration: v.AvgDuration,
		}
	}
	return MetricsSnapshot{
		AvgRecoveryTime:     m.AvgRecoveryTime,
		MinRecoveryTime:     m.MinRecoveryTime,
		MaxRecoveryTime:     m.MaxRecoveryTime,
		LastRecoveryTime:    m.LastRecoveryTime,
		FailoverCount:       m.FailoverCount,
		TotalRecoveries:     m.TotalRecoveries,
		FailedRecoveries:    m.FailedRecoveries,
		DataConsistencyRate: m.DataConsistencyRate,
		ActionStats:         stats,
	}
}

// Reset clears all arena state.
func (s *Service) Reset() {
	s.inner.Reset()
}

func toPublicAction(a internal.Action) Action {
	return Action{
		ID:        a.ID,
		Type:      ActionType(a.Type),
		TargetID:  a.TargetID,
		SourceID:  a.SourceID,
		Metadata:  a.Metadata,
		CreatedAt: a.CreatedAt,
	}
}

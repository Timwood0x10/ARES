// Package arena provides the public API for chaos engineering operations.
package arena

import (
	"context"

	internal "github.com/Timwood0x10/ares/internal/ares_arena"
)

// Service wraps internal/ares_arena.Service for public consumption.
type Service struct {
	inner *internal.Service
}

// New creates a new Arena service with the given injector and event store.
func New(injector *internal.Injector, store internal.EventStore) *Service {
	inner := internal.NewService(injector, store)
	return &Service{inner: inner}
}

// Execute runs a chaos engineering action.
func (s *Service) Execute(ctx context.Context, action internal.Action) internal.Result {
	return s.inner.Execute(ctx, action)
}

// History returns the action history.
func (s *Service) History() []internal.Result {
	return s.inner.History()
}

// Stats returns aggregate statistics.
func (s *Service) Stats() internal.Stats {
	return s.inner.Stats()
}

// Metrics returns snapshot metrics.
func (s *Service) Metrics() internal.MetricsSnapshot {
	return s.inner.Metrics()
}

// Reset clears all arena state.
func (s *Service) Reset() {
	s.inner.Reset()
}

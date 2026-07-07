// Package evolution provides the public API for genetic algorithm evolution.
package evolution

import (
	"context"

	internal "github.com/Timwood0x10/ares/internal/ares_evolution/service"
)

// Config re-exports internal's SystemConfig for public API consistency.
type Config = internal.SystemConfig

// EvolutionResult re-exports internal's evolution result.
type EvolutionResult = internal.EvolutionResult

// Strategy re-exports internal's strategy type.
type Strategy = internal.Strategy

// Stats re-exports internal's stats type.
type Stats = internal.Stats

// Service wraps internal/ares_evolution/service.Service for public consumption.
type Service struct {
	inner *internal.Service
}

// New creates a new evolution service.
func New(cfg *internal.SystemConfig) (*Service, error) {
	inner, err := internal.NewService(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{inner: inner}, nil
}

// Evolve runs evolution for N generations.
func (s *Service) Evolve(ctx context.Context, generations int) (*internal.EvolutionResult, error) {
	return s.inner.Evolve(ctx, generations)
}

// RunIdleEvolution runs N generations and saves the report.
func (s *Service) RunIdleEvolution(ctx context.Context, generations int) error {
	return s.inner.RunIdleEvolution(ctx, generations)
}

// BestStrategy returns the current best strategy.
func (s *Service) BestStrategy() (*internal.Strategy, error) {
	return s.inner.BestStrategy()
}

// Stats returns evolution statistics.
func (s *Service) Stats() (*internal.Stats, error) {
	return s.inner.Stats()
}

// Lineages returns the lineage history.
func (s *Service) Lineages() ([]internal.StrategyLineage, error) {
	return s.inner.Lineages()
}

// SaveBestStrategy persists the best strategy to a file.
func (s *Service) SaveBestStrategy(path string) error {
	return s.inner.SaveBestStrategy(path)
}

// Shutdown gracefully shuts down the evolution system.
func (s *Service) Shutdown() {
	s.inner.Shutdown()
}

// ReportPath returns the configured report path.
func (s *Service) ReportPath() string {
	return s.inner.ReportPath()
}

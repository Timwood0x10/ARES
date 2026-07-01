// Package scoring provides adapters for converting experience data
// into multi-dimensional evidence for strategy scoring.
package scoring

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_evolution/experience"
)

// EvidenceAggregator defines the interface for aggregating execution
// experiences into multi-dimensional evidence statistics.
// This interface is implemented by the experience aggregation layer
// and provides the raw data that EvidenceProvider consumes.
type EvidenceAggregator interface {
	// AggregateByStrategy aggregates execution experiences for a specific strategy.
	//
	// Args:
	//
	//	ctx - operation context.
	//	strategyID - the identifier of the strategy to aggregate.
	//
	// Returns:
	//
	//	Evidence - aggregated statistics including success_rate, latency, error_rate.
	//	error - non-nil if aggregation fails.
	AggregateByStrategy(ctx context.Context, strategyID string) (experience.Evidence, error)

	// AggregateByTaskType aggregates execution experiences across all strategies
	// for a specific task type.
	//
	// Args:
	//
	//	ctx - operation context.
	//	taskType - the type of task to aggregate.
	//
	// Returns:
	//
	//	Evidence - aggregated statistics across all strategies for the task type.
	//	error - non-nil if aggregation fails.
	AggregateByTaskType(ctx context.Context, taskType string) (experience.Evidence, error)
}

// ExperienceToEvidenceAdapter wraps an EvidenceAggregator to implement
// the EvidenceProvider interface. This adapter bridges the gap between
// the aggregation layer and the scoring layer, allowing MemoryAwareScorer
// to consume multi-dimensional evidence.
//
// Usage:
//
//	agg := NewEvidenceAggregator(store)
//	adapter := NewExperienceToEvidenceAdapter(agg)
//	scorer.SetEvidenceProvider(adapter)
type ExperienceToEvidenceAdapter struct {
	aggregator EvidenceAggregator
}

// NewExperienceToEvidenceAdapter creates a new adapter that wraps an
// EvidenceAggregator to implement EvidenceProvider.
//
// Args:
//
//	agg - the evidence aggregator to wrap (must not be nil).
//
// Returns:
//
//	*ExperienceToEvidenceAdapter - the configured adapter.
//	error - non-nil if aggregator is nil.
func NewExperienceToEvidenceAdapter(agg EvidenceAggregator) (*ExperienceToEvidenceAdapter, error) {
	if agg == nil {
		return nil, fmt.Errorf("evidence aggregator must not be nil")
	}
	return &ExperienceToEvidenceAdapter{
		aggregator: agg,
	}, nil
}

// GetEvidence retrieves multi-dimensional evidence for a specific strategy.
// This method delegates to the underlying EvidenceAggregator.
//
// Args:
//
//	ctx - operation context.
//	strategyID - the identifier of the strategy to retrieve evidence for.
//
// Returns:
//
//	Evidence - multi-dimensional aggregated statistics.
//	error - non-nil if retrieval fails.
func (a *ExperienceToEvidenceAdapter) GetEvidence(ctx context.Context, strategyID string) (experience.Evidence, error) {
	if strategyID == "" {
		return experience.Evidence{}, fmt.Errorf("strategy_id must not be empty")
	}
	return a.aggregator.AggregateByStrategy(ctx, strategyID)
}

// GetEvidenceByTaskType retrieves multi-dimensional evidence aggregated
// across all strategies for a specific task type.
// This method delegates to the underlying EvidenceAggregator.
//
// Args:
//
//	ctx - operation context.
//	taskType - the type of task to retrieve evidence for.
//
// Returns:
//
//	Evidence - multi-dimensional aggregated statistics.
//	error - non-nil if retrieval fails.
func (a *ExperienceToEvidenceAdapter) GetEvidenceByTaskType(ctx context.Context, taskType string) (experience.Evidence, error) {
	if taskType == "" {
		return experience.Evidence{}, fmt.Errorf("task_type must not be empty")
	}
	return a.aggregator.AggregateByTaskType(ctx, taskType)
}
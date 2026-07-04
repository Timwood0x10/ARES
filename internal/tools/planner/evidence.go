package planner

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// evidenceAggregator computes aggregate metrics from tool execution evidence.
type evidenceAggregator struct {
	store EvidenceStore
	mu    sync.RWMutex
}

// NewEvidenceAggregator creates an evidence aggregator backed by the given store.
func NewEvidenceAggregator(store EvidenceStore) *evidenceAggregator {
	return &evidenceAggregator{
		store: store,
	}
}

// Record saves a single tool execution result and triggers score update.
//
// Args:
//
//	ctx - cancellation and timeout context.
//	toolName - the tool that was executed.
//	capaName - the capability that was invoked.
//	success - whether execution succeeded.
//	latency - how long execution took.
//	retryCount - number of retries attempted.
//	errorClass - normalized error class if failed ("", "timeout", "invalid_input",
//	  "internal_error", "external_unavailable").
//
// Returns:
//
//	err - error if storage fails.
func (a *evidenceAggregator) Record(
	ctx context.Context,
	toolName string,
	capaName string,
	success bool,
	latency time.Duration,
	retryCount int,
	errorClass string,
) error {
	if toolName == "" {
		return fmt.Errorf("planner: evidence tool name is empty")
	}
	if errorClass == "" && !success {
		errorClass = "internal_error"
	}

	evidence := &ToolEvidence{
		ToolName:       toolName,
		CapabilityName: capaName,
		Success:        success,
		Latency:        latency,
		RetryCount:     retryCount,
		ErrorClass:     errorClass,
		Timestamp:      time.Now(),
	}

	return a.store.Save(ctx, evidence)
}

// AggregateMetrics computes summary statistics per tool and capability.
//
// Returns a map keyed by "toolName:capabilityName" with computed metrics.
func (a *evidenceAggregator) AggregateMetrics(ctx context.Context) (map[string]ToolScore, error) {
	return a.store.Aggregate(ctx, "")
}

// EvidenceScorer extends the basic scorer with evidence-aware ranking.
type evidenceScorer struct {
	aggregator *evidenceAggregator
}

// NewEvidenceScorer creates a scorer that uses evidence for ranking.
func NewEvidenceScorer(store EvidenceStore) ToolScorer {
	return &evidenceScorer{
		aggregator: NewEvidenceAggregator(store),
	}
}

// Score ranks candidates using both static metadata and historical evidence.
//
// Scoring formula:
//
//	baseScore = (1.0 / max(cost, 1)) * 10.0
//	  + (deterministic ? 3.0 : 0.0)
//	  + (composable ? 2.0 : 0.0)
//
//	evidenceScore = successRate * 20.0 - latencyPenalty - failurePenalty
//	  where latencyPenalty = min(latencyMs / 100.0, 5.0)
//	  where failurePenalty = (1.0 - successRate) * 10.0 (if repeated failures)
//
//	penalty = sideEffects ? 5.0 : 0.0
//
//	final = baseScore + evidenceScore - penalty
func (s *evidenceScorer) Score(ctx context.Context, candidates []ToolCandidate, evidence []ToolEvidence) ([]ToolCandidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	// Build evidence aggregates.
	evidenceMap := s.aggregateEvidence(evidence)

	scored := make([]ToolCandidate, len(candidates))
	for i, c := range candidates {
		ev, hasEvidence := evidenceMap[c.ToolName+":"+c.CapabilityName]

		successRate := c.SuccessRate
		avgLatency := c.Latency
		if hasEvidence {
			successRate = ev.successRate
			avgLatency = ev.avgLatency
		}

		baseScore := (1.0 / float64(maxInt(c.Cost, 1))) * 10.0
		if c.Deterministic {
			baseScore += 3.0
		}
		if c.Composable {
			baseScore += 2.0
		}

		evidenceScore := successRate * 20.0
		latencyMs := avgLatency.Milliseconds()
		evidenceScore -= math.Min(float64(latencyMs)/100.0, 5.0)
		// Penalize repeated failures.
		if hasEvidence && ev.failureCount > 0 {
			failureRatio := float64(ev.failureCount) / float64(maxInt(ev.count, 1))
			evidenceScore -= failureRatio * 10.0
		}

		penalty := 0.0
		if c.SideEffects {
			penalty += 5.0
		}

		c.Score = baseScore + evidenceScore - penalty
		scored[i] = c
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	return scored, nil
}

// evidenceAgg holds aggregate metrics.
type evidenceAgg struct {
	successRate  float64
	avgLatency   time.Duration
	count        int
	failureCount int
}

// aggregateEvidence computes aggregate metrics from evidence records.
func (s *evidenceScorer) aggregateEvidence(evidence []ToolEvidence) map[string]evidenceAgg {
	result := make(map[string]evidenceAgg)
	for _, e := range evidence {
		key := e.ToolName + ":" + e.CapabilityName
		agg := result[key]
		agg.count++
		agg.avgLatency += e.Latency
		if e.Success {
			agg.successRate += 1.0
		} else {
			agg.failureCount++
		}
		result[key] = agg
	}
	for key, agg := range result {
		if agg.count > 0 {
			agg.successRate /= float64(agg.count)
			agg.avgLatency /= time.Duration(agg.count)
		}
		result[key] = agg
	}
	return result
}

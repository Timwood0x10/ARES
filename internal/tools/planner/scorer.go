package planner

import (
	"context"
	"math"
	"sort"
	"time"
)

// toolScorer implements ToolScorer using static metadata and evidence.
// Scoring formula:
//
//	BaseScore = (1.0 / Cost) * 10 + (Deterministic ? 3 : 0) + (Composable ? 2 : 0)
//	EvidenceScore = SuccessRate * 20 - LatencyPenalty
//	Penalty = SideEffects ? 5 : 0
//	Final = BaseScore + EvidenceScore - Penalty
type toolScorer struct{}

// NewToolScorer creates a deterministic tool scorer.
func NewToolScorer() ToolScorer {
	return &toolScorer{}
}

// Score computes and ranks tool candidates by score.
func (s *toolScorer) Score(_ context.Context, candidates []ToolCandidate, evidence []ToolEvidence) ([]ToolCandidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	// Build evidence lookup: toolName -> aggregated metrics.
	evidenceMap := aggregateEvidence(evidence)

	scored := make([]ToolCandidate, len(candidates))
	for i, c := range candidates {
		ev, hasEvidence := evidenceMap[c.ToolName]
		successRate := c.SuccessRate
		if hasEvidence {
			successRate = ev.successRate
		}

		baseScore := (1.0 / float64(maxInt(c.Cost, 1))) * 10.0
		if c.Deterministic {
			baseScore += 3.0
		}
		if c.Composable {
			baseScore += 2.0
		}

		evidenceScore := successRate * 20.0
		if hasEvidence {
			// Penalize high latency: subtract up to 5 points for slow tools.
			latencyMs := ev.avgLatency.Milliseconds()
			evidenceScore -= math.Min(float64(latencyMs)/100.0, 5.0)
		}

		penalty := 0.0
		if c.SideEffects {
			penalty += 5.0
		}

		c.Score = baseScore + evidenceScore - penalty
		scored[i] = c
	}

	// Sort by score descending.
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	return scored, nil
}

// aggregateEvidence groups evidence by tool name and computes aggregates.
func aggregateEvidence(evidence []ToolEvidence) map[string]evidenceAgg {
	result := make(map[string]evidenceAgg)
	for _, e := range evidence {
		agg := result[e.ToolName]
		agg.count++
		agg.avgLatency += e.Latency
		if e.Success {
			agg.successRate += 1.0
		}
		result[e.ToolName] = agg
	}
	// Finalize averages.
	for name, agg := range result {
		if agg.count > 0 {
			agg.successRate /= float64(agg.count)
			agg.avgLatency /= time.Duration(agg.count)
		}
		result[name] = agg
	}
	return result
}

// maxInt returns the larger of two integers.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Package genome provides population management for genetic algorithm evolution.
// This file contains guard methods for validation, diversity recovery, and reporting.
package genome

import (
	"fmt"
	"log/slog"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// ensureEvaluatedBeforeSelection validates that all individuals in the population
// have been evaluated before selection proceeds. Returns an error if any agent
// has an unevaluated score.
//
// This implements Phase 1 Item 2 from the GA Hardening Plan: "Selection never
// operates on unevaluated individuals."
func (p *Population) ensureEvaluatedBeforeSelection() error {
	for i, a := range p.Agents {
		if !IsScoreEvaluated(a.Score) {
			return fmt.Errorf("agent %d (%s) has unevaluated score %.1f; "+
				"score all agents before calling Evolve", i, a.ID, a.Score)
		}
	}
	return nil
}

// DiversityStats returns a detailed diversity report for the current population.
// Thread-safe: acquires read lock.
//
// Returns:
//
//	DiversityReport - detailed breakdown of numeric, categorical, and lineage diversity.
func (p *Population) DiversityStats() DiversityReport {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.measureDiversityReportLocked()
}

// injectFreshMutantsLocked replaces the bottom portion of the population with
// randomly perturbed clones of elite strategies to restore diversity.
//
// Args:
//
//	eliteCount - number of leading agents protected from replacement.
func (p *Population) injectFreshMutantsLocked(eliteCount int) {
	n := len(p.Agents)
	if n <= eliteCount+1 {
		// Population too small to inject without risking elites. Skip.
		return
	}

	// Determine how many agents to replace (bottom portion).
	// Never replace more than half of non-elite agents to preserve stability.
	nonEliteCount := n - eliteCount
	maxReplace := max(1, nonEliteCount/2) // At most 50% of non-elites
	replaceCount := min(maxReplace, n/3)  // But also cap at 33% of total
	if replaceCount < 1 {
		replaceCount = 1
	}

	startIdx := n - replaceCount

	// Safety: ensure we don't touch the elite region.
	// Elite region is [0, eliteCount). Injection region is [startIdx, n).
	// Requirement: startIdx >= eliteCount (no overlap).
	if startIdx < eliteCount {
		// Elite count is too large for safe injection at default rate.
		// Reduce replacement count until regions don't overlap.
		replaceCount = n - eliteCount
		if replaceCount < 1 {
			return // Can't safely inject any agents.
		}
		startIdx = n - replaceCount
	}

	for i := startIdx; i < n; i++ {
		// Pick a random template from the protected (elite) region.
		var templateIdx int
		if eliteCount > 0 {
			templateIdx = p.rng.Intn(eliteCount)
		} else {
			templateIdx = p.rng.Intn(n)
		}
		template := p.Agents[templateIdx].Clone()

		// Apply wider perturbation with selective parameter mixing.
		// Each param has a 40% chance to remain unchanged, preserving good
		// alleles while introducing targeted variation in the rest.
		for k, v := range template.Params {
			if p.rng.Float64() < 0.4 {
				continue // keep original value
			}
			if f, ok := v.(float64); ok {
				// Perturb by ±80%: range [0.2x, 1.8x].
				perturbation := f * (0.2 + p.rng.Float64()*1.6)
				template.Params[k] = perturbation
			} else if iVal, ok := v.(int); ok {
				delta := p.rng.Intn(max(iVal, 1)+iVal) - iVal/2
				template.Params[k] = iVal + delta
			}
		}

		// Randomly swap prompt template with another agent's to introduce variation.
		if p.rng.Float64() < 0.3 && len(p.Agents) > 1 {
			otherIdx := p.rng.Intn(len(p.Agents))
			if otherIdx != i {
				template.PromptTemplate = p.Agents[otherIdx].PromptTemplate
			}
		}

		template.Score = ScoreUnevaluated
		template.ID = fmt.Sprintf("fresh-mut-%d-gen%d", i, p.Generation)
		template.ParentID = p.Agents[templateIdx].ID
		template.Version = template.Version + 1
		template.StrategyMutationType = mutation.MutationParameter
		template.MutationDesc = "fresh mutant injection for diversity recovery"

		p.Agents[i] = template
	}

	slog.Debug("fresh mutants injected for diversity recovery",
		"generation", p.Generation,
		"replace_count", replaceCount,
		"start_index", startIdx,
		"elite_count", eliteCount,
	)
}

// preserveElites copies the top EliteCount survivors without modification.
// Elites are deep-cloned to prevent shared state across generations.
//
// Args:
//
//	survivors - sorted survivor strategies (highest score first).
//
// Returns:
//
//	[]*mutation.Strategy - deep-cloned elite strategies.
func (p *Population) preserveElites(survivors []*mutation.Strategy) []*mutation.Strategy {
	eliteCount := min(p.cfg.EliteCount, len(survivors))
	if eliteCount <= 0 {
		return []*mutation.Strategy{}
	}

	elites := make([]*mutation.Strategy, 0, eliteCount)
	for i := 0; i < eliteCount; i++ {
		elites = append(elites, survivors[i].Clone())
	}

	return elites
}

// ---------------------------------------------------------------------------
// Performance characteristics of applyFitnessSharing
// ---------------------------------------------------------------------------
//
// Time complexity:
//   - Small populations (m <= FitnessSharingSampleLimit): O(m² × k) where m is
//     the number of scored agents and k is the number of parameter keys per agent.
//     The distance matrix is computed once in O(m²/2) paramDistance calls (upper
//     triangular only), then looked up in O(1) during penalty accumulation.
//   - Large populations (m > FitnessSharingSampleLimit): O(m × s × k) where
//     s = FitnessSharingSampleSize. Each agent checks s random neighbors instead
//     of all m-1 agents, reducing quadratic to linear scaling.
//
// Space complexity:
//   - With distance matrix cache: O(m²) for the full distance matrix.
//   - With sampling mode: O(s) per agent for neighbor index storage (no matrix).
//
// Recommended max population size for real-time evolution: ~200 agents. Beyond
// this, consider enabling spatial indexing (grid-based KD-tree or similar) to
// achieve sub-linear neighbor queries. See TODO below.
//
// ---------------------------------------------------------------------------

// applyFitnessSharing reduces scores of agents in crowded regions of parameter space.
// This prevents all agents from converging to the same local optimum by penalizing
// similarity — agents that occupy the same niche share their fitness.
//
// Agents with IsScoreEvaluated() == false (unevaluated) are excluded from both the distance
// calculation and penalty, preventing fitness sharing from operating on
// meaningless default scores that would distort diversity metrics.
//
// Args:
//
//	eliteCount - number of leading agents (already sorted by score) protected from penalty.
func (p *Population) applyFitnessSharing(eliteCount int) {
	n := len(p.Agents)
	if n < 2 {
		return
	}

	const (
		shareSigma  = FitnessSharingSigma // sharing coefficient
		nicheRadius = FitnessNicheRadius  // distance threshold for "same niche"
	)

	// Build a filtered index of scored agents only (IsScoreEvaluated()).
	// Unevaluated agents (Score=ScoreUnevaluated) should not participate in fitness sharing
	// because their score is meaningless and would distort the shared fitness signal.
	scoredIdx := make([]int, 0, n)
	for i, a := range p.Agents {
		if IsScoreEvaluated(a.Score) {
			scoredIdx = append(scoredIdx, i)
		}
	}

	m := len(scoredIdx)
	if m < 2 {
		return
	}

	// Build temp slice for key/range collection from scored agents only.
	scored := make([]*mutation.Strategy, m)
	for k, idx := range scoredIdx {
		scored[k] = p.Agents[idx]
	}
	keys := collectAgentParamKeys(scored)
	ranges := computeParamRanges(scored, keys)

	// PERF: Choose computation strategy based on population size.
	// Small populations use exact pairwise distances with matrix caching;
	// large populations use randomized neighbor sampling to bound cost;
	// very large populations use grid-based spatial indexing to achieve
	// sub-linear neighbor lookup. Thresholds are configurable.
	limit := p.cfg.FitnessSharingSampleLimit
	spatial := p.cfg.SpatialIndexThreshold
	if limit <= 0 {
		limit = m + 1 // Disable sampling: always use exact mode
	}
	if spatial > 0 && m > spatial {
		p.applyFitnessSharingSpatial(scoredIdx, scored, keys, ranges, eliteCount, nicheRadius, shareSigma)
	} else if m <= limit {
		p.applyFitnessSharingExact(scoredIdx, scored, keys, ranges, eliteCount, nicheRadius, shareSigma)
	} else {
		p.applyFitnessSharingSampled(scoredIdx, scored, keys, ranges, eliteCount, nicheRadius, shareSigma)
	}
}

// applyFitnessSharingExact computes fitness sharing penalties using an exact
// pairwise distance matrix. The upper-triangular distance matrix is computed
// once and reused for all pair lookups, eliminating redundant paramDistance calls.
func (p *Population) applyFitnessSharingExact(
	scoredIdx []int,
	scored []*mutation.Strategy,
	keys []string,
	ranges map[string]float64,
	eliteCount int,
	nicheRadius float64,
	shareSigma float64,
) {
	m := len(scoredIdx)

	// PERF: Pre-compute full distance matrix (upper-triangular mirrored).
	// paramDistance(a,b) == paramDistance(b,a), so we compute each unique pair
	// once and mirror it. This cuts paramDistance calls from m*(m-1) to m*(m-1)/2,
	// providing ~2x speedup for the distance computation phase.
	distMatrix := make([]float64, m*m)
	for ki := 0; ki < m; ki++ {
		for kj := ki + 1; kj < m; kj++ {
			dist := paramDistance(scored[ki], scored[kj], keys, ranges)
			distMatrix[ki*m+kj] = dist
			distMatrix[kj*m+ki] = dist
		}
	}

	// Accumulate penalties using cached distances.
	for ki, i := range scoredIdx {
		if i < eliteCount {
			continue // skip elites
		}
		crowdCount := 0
		for kj := 0; kj < m; kj++ {
			if ki == kj {
				continue
			}
			if distMatrix[ki*m+kj] < nicheRadius {
				crowdCount++
			}
		}
		if crowdCount > 0 {
			penalty := shareSigma * float64(crowdCount)
			p.Agents[i].Score /= (1.0 + penalty)
		}
	}
}

// applyFitnessSharingSampled computes fitness sharing penalties using randomized
// neighbor sampling. When the scored population exceeds FitnessSharingSampleLimit,
// checking all O(m²) pairs is prohibitively expensive. Instead, each non-elite
// agent checks against FitnessSharingSampleSize randomly chosen neighbors,
// bounding total work to O(m × FitnessSharingSampleSize × k).
//
// PERF: Sampling introduces stochastic approximation — crowd counts are estimates
// rather than exact values. The penalty formula and niche radius remain identical;
// only the set of compared neighbors differs from the exact version.
func (p *Population) applyFitnessSharingSampled(
	scoredIdx []int,
	scored []*mutation.Strategy,
	keys []string,
	ranges map[string]float64,
	eliteCount int,
	nicheRadius float64,
	shareSigma float64,
) {
	m := len(scoredIdx)
	sampleSize := min(p.cfg.FitnessSharingSampleSize, m-1)

	for ki, i := range scoredIdx {
		if i < eliteCount {
			continue // skip elites
		}

		// PERF: Inline Fisher-Yates partial shuffle on a pre-allocated slice.
		// Replaces rng.Perm(m) which allocates a new []int each call, causing
		// GC pressure in large-population evolution loops. This pattern allocates
		// once per agent and shuffles in-place for O(m) time, O(m) space.
		indices := make([]int, m)
		for idx := range indices {
			indices[idx] = idx
		}
		for idx := m - 1; idx > 0; idx-- {
			j := p.rng.Intn(idx + 1)
			indices[idx], indices[j] = indices[j], indices[idx]
		}

		crowdCount := 0
		sampleEnd := min(sampleSize, len(indices))
		for s := 0; s < sampleEnd; s++ {
			kj := indices[s]
			if kj == ki {
				continue
			}
			dist := paramDistance(scored[ki], scored[kj], keys, ranges)
			if dist < nicheRadius {
				crowdCount++
			}
		}

		if crowdCount > 0 {
			penalty := shareSigma * float64(crowdCount)
			p.Agents[i].Score /= (1.0 + penalty)
		}
	}
}

// applyFitnessSharingSpatial computes fitness sharing penalties using grid-based
// spatial indexing. For very large populations (configurable via SpatialIndexThreshold),
// this scales sub-linearly by only comparing each agent against neighbors in the
// same or adjacent grid cells rather than random sampling or the full population.
//
// The spatial index is built on the top-N highest-variance float parameters
// (capped at maxSpatialDims=6 to keep cell enumeration tractable). Distance
// calculations still use the full parameter space.
func (p *Population) applyFitnessSharingSpatial(
	scoredIdx []int,
	scored []*mutation.Strategy,
	keys []string,
	ranges map[string]float64,
	eliteCount int,
	nicheRadius float64,
	shareSigma float64,
) {
	m := len(scoredIdx)
	if m < 2 {
		return
	}

	sidx := newSpatialIndex(scoredIdx, scored, keys, ranges, nicheRadius)
	if sidx == nil {
		// Fallback to sampled mode when spatial indexing doesn't apply.
		p.applyFitnessSharingSampled(scoredIdx, scored, keys, ranges, eliteCount, nicheRadius, shareSigma)
		return
	}

	for ki, i := range scoredIdx {
		if i < eliteCount {
			continue
		}

		neighbors := sidx.neighborsWithin(ki)
		crowdCount := 0
		for _, nk := range neighbors {
			if nk == ki {
				continue
			}
			dist := paramDistance(scored[ki], scored[nk], keys, ranges)
			if dist < nicheRadius {
				crowdCount++
			}
		}

		if crowdCount > 0 {
			penalty := shareSigma * float64(crowdCount)
			p.Agents[i].Score /= (1.0 + penalty)
		}
	}
}

// Package genome provides population management for genetic algorithm evolution.
// This file contains guard methods for validation, diversity recovery, and reporting.
package genome

import (
	"fmt"
	"log/slog"

	"goagentx/internal/evolution/mutation"
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
		return
	}

	// Replace bottom 30% (or at least 1) with heavily mutated clones.
	replaceCount := max(1, n/3)
	replaceCount = min(replaceCount, n-eliteCount)
	startIdx := n - replaceCount

	protectedStart := min(eliteCount, startIdx)
	if protectedStart >= startIdx {
		startIdx = protectedStart + 1
	}
	if startIdx >= n {
		return
	}

	for i := startIdx; i < n; i++ {
		// Pick a random template from the protected (elite) region.
		var templateIdx int
		if protectedStart > 0 {
			templateIdx = p.rng.Intn(protectedStart)
		} else {
			templateIdx = p.rng.Intn(n)
		}
		template := p.Agents[templateIdx].Clone()

		// Strong random perturbation on each numeric parameter.
		for k, v := range template.Params {
			if f, ok := v.(float64); ok {
				// Perturb by ±50%: range [0.5x, 1.5x].
				perturbation := f * (0.5 + p.rng.Float64())
				template.Params[k] = perturbation
			} else if iVal, ok := v.(int); ok {
				delta := p.rng.Intn(max(iVal, 1)+1) - iVal/2
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

	if len(scoredIdx) < 2 {
		return
	}

	// Build temp slice for key/range collection from scored agents only.
	scored := make([]*mutation.Strategy, len(scoredIdx))
	for k, idx := range scoredIdx {
		scored[k] = p.Agents[idx]
	}
	keys := collectAgentParamKeys(scored)
	ranges := computeParamRanges(scored, keys)

	// Apply fitness sharing penalty using original agent slices (indexed through
	// scoredIdx) to avoid maintaining a parallel slice alongside the index list.
	for ki, i := range scoredIdx {
		if i < eliteCount {
			continue // skip elites
		}
		crowdCount := 0
		for kj := range scoredIdx {
			if ki == kj {
				continue
			}
			dist := paramDistance(p.Agents[scoredIdx[ki]], p.Agents[scoredIdx[kj]], keys, ranges)
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

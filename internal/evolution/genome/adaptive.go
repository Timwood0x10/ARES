package genome

import (
	"log/slog"

	"goagentx/internal/evolution/mutation"
)

// computeBestScoreLocked returns the highest score in the current agents.
// Caller must hold at least a read lock.
func (p *Population) computeBestScoreLocked() float64 {
	best := -1.0
	for _, a := range p.Agents {
		if a.Score > best {
			best = a.Score
		}
	}
	return best
}

// measureDiversityLocked computes the average pairwise normalized parameter
// distance across all agents. Only numeric parameters (float64, int, int64, etc.)
// participate in the distance calculation; string or other types are skipped.
// Returns a value in [0, 1] where 0 means all agents have identical parameters
// and 1 means maximum divergence.
// Caller must hold at least a read lock.
func (p *Population) measureDiversityLocked() float64 {
	n := len(p.Agents)
	if n < 2 {
		return 1.0
	}

	allKeys := collectAgentParamKeys(p.Agents)
	if len(allKeys) == 0 {
		return 1.0
	}

	ranges := computeParamRanges(p.Agents, allKeys)

	var totalDist float64
	var pairCount int
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			dist := paramDistance(p.Agents[i], p.Agents[j], allKeys, ranges)
			totalDist += dist
			pairCount++
		}
	}

	if pairCount == 0 {
		return 1.0
	}
	return totalDist / float64(pairCount)
}

// collectAgentParamKeys returns the union of all parameter keys across agents.
func collectAgentParamKeys(agents []*mutation.Strategy) []string {
	seen := make(map[string]struct{})
	for _, a := range agents {
		for k := range a.Params {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	return keys
}

// computeParamRanges returns the range (max - min) for each numeric parameter
// across all agents. A default of 1.0 is used when the range is zero
// (all values identical) to avoid division by zero in distance normalization.
func computeParamRanges(agents []*mutation.Strategy, keys []string) map[string]float64 {
	ranges := make(map[string]float64, len(keys))
	for _, k := range keys {
		var minVal, maxVal float64
		first := true
		for _, a := range agents {
			v, ok := a.Params[k]
			if !ok {
				continue
			}
			f, ok := toFloat64(v)
			if !ok {
				continue
			}
			if first {
				minVal = f
				maxVal = f
				first = false
			} else {
				if f < minVal {
					minVal = f
				}
				if f > maxVal {
					maxVal = f
				}
			}
		}
		if first {
			ranges[k] = 1.0
		} else {
			diff := maxVal - minVal
			if diff < 1e-10 {
				ranges[k] = 1.0
			} else {
				ranges[k] = diff
			}
		}
	}
	return ranges
}

// paramDistance returns the normalized distance between two agents in shared
// parameter space. Non-numeric or missing parameters are skipped.
func paramDistance(a, b *mutation.Strategy, keys []string, ranges map[string]float64) float64 {
	var totalDist float64
	var count int
	for _, k := range keys {
		va, okA := a.Params[k]
		vb, okB := b.Params[k]
		if !okA || !okB {
			continue
		}
		fa, okA := toFloat64(va)
		fb, okB := toFloat64(vb)
		if !okA || !okB {
			continue
		}
		r := ranges[k]
		if r < 1e-10 {
			r = 1.0
		}
		totalDist += absFloat(fa-fb) / r
		count++
	}

	// Categorical distance for PromptTemplate: different template = max distance (1.0).
	if a.PromptTemplate != b.PromptTemplate {
		totalDist += 1.0
		count++
	}

	// Categorical distance for tools: different tool configuration = max distance (1.0).
	if ta, okA := a.Params["tools"].(string); okA {
		if tb, okB := b.Params["tools"].(string); okB && ta != tb {
			totalDist += 1.0
			count++
		}
	}

	if count == 0 {
		return 0.0
	}
	return totalDist / float64(count)
}

// toFloat64 attempts to convert an arbitrary numeric value to float64.
func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case int32:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint64:
		return float64(val), true
	case uint32:
		return float64(val), true
	default:
		return 0, false
	}
}

// absFloat returns the absolute value of a float64.
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// adjustMutationRateLocked updates currentMutationRate based on population diversity.
// Called after each generation to prevent premature convergence.
//
// Key behaviors:
//   - Emergency mode: critically low diversity (<0.05) forces max mutation rate.
//   - Low diversity: aggressive boost proportional to deficit below threshold.
//   - High diversity: gentle decay allowed.
//   - Moderate diversity: maintain current rate (no drift toward base).
//   - Floor protection: never drop below 0.15 unless diversity is genuinely high (>0.3).
func (p *Population) adjustMutationRateLocked() {
	div := p.measureDiversityLocked()

	// Emergency mode: critically low diversity — force maximum exploration.
	if div < 0.05 {
		slog.Warn("emergency mutation rate boost: critically low diversity",
			"diversity", div,
			"generation", p.Generation,
			"mutation_rate", p.cfg.MaxMutationRate,
		)
		p.currentMutationRate = p.cfg.MaxMutationRate
		return
	}

	// Low diversity: aggressive boost proportional to how far below threshold.
	if div < p.cfg.DiversityThreshold {
		deficit := p.cfg.DiversityThreshold - div
		boostFactor := 1.5 + (deficit/p.cfg.DiversityThreshold)*1.0 // range: 1.5x – 2.5x
		p.currentMutationRate = minFloat(p.currentMutationRate*boostFactor, p.cfg.MaxMutationRate)
	} else if div > p.cfg.DiversityThreshold*3 {
		// Very high diversity: allow gentle decay toward floor.
		p.currentMutationRate = maxFloat(p.currentMutationRate*0.95, p.cfg.MinMutationRate)
	} else {
		// Moderate diversity: maintain current rate — only drift down if
		// significantly above base to avoid unnecessary reduction.
		if p.currentMutationRate > p.cfg.MutationRate*2 {
			p.currentMutationRate = maxFloat(p.currentMutationRate*0.98, p.cfg.MutationRate*1.5)
		}
	}

	// Floor: keep minimum at 0.15 unless diversity is genuinely high.
	effectiveMin := p.cfg.MinMutationRate
	if div < 0.3 {
		effectiveMin = maxFloat(0.15, p.cfg.MinMutationRate)
	}
	p.currentMutationRate = clampFloat(p.currentMutationRate, effectiveMin, p.cfg.MaxMutationRate)

	slog.Debug("adaptive mutation rate adjusted",
		"diversity", div,
		"mutation_rate", p.currentMutationRate,
		"effective_min", effectiveMin,
		"generation", p.Generation,
	)
}

// handleStagnationLocked checks for best-score stagnation and resets bottom
// performers if the threshold has been exceeded. Instead of copying top performers
// exactly, it injects strongly perturbed clones to introduce novel genetic material.
func (p *Population) handleStagnationLocked() {
	if p.cfg.MaxStagnantGenerations <= 0 {
		return
	}

	currentBest := p.computeBestScoreLocked()
	if currentBest > p.bestScore {
		p.bestScore = currentBest
		p.stagnantGens = 0
		return
	}

	p.stagnantGens++
	if p.stagnantGens < p.cfg.MaxStagnantGenerations {
		return
	}

	stagnantGens := p.stagnantGens
	resetCount := max(1, len(p.Agents)/3)
	resetCount = min(resetCount, len(p.Agents)-p.cfg.EliteCount)
	if resetCount <= 0 {
		p.stagnantGens = 0
		slog.Warn("stagnation triggered but no agents to reset",
			"population_size", len(p.Agents),
			"elite_count", p.cfg.EliteCount,
		)
		return
	}

	SortByScore(p.Agents)
	startIdx := len(p.Agents) - resetCount

	// Inject random mutations from elites instead of exact copies.
	// Each reset agent is a heavily perturbed clone of a random elite,
	// ensuring genuinely novel individuals enter the population.
	for i := startIdx; i < len(p.Agents); i++ {
		template := p.Agents[p.rng.Intn(startIdx)]
		clone := template.Clone()

		// Apply strong random perturbation to each numeric param.
		for k, v := range clone.Params {
			if f, ok := v.(float64); ok {
				// Perturb by ±40% of original value: 60%–140% range.
				perturbation := f * (0.6 + p.rng.Float64()*0.8)
				clone.Params[k] = perturbation
			} else if iVal, ok := v.(int); ok {
				delta := p.rng.Intn(iVal/2+1) - iVal/4
				clone.Params[k] = iVal + delta
			}
		}

		clone.Score = -1
		p.Agents[i] = clone
	}

	p.stagnantGens = 0
	slog.Warn("stagnation detected, injected random mutants from elites",
		"reset_count", resetCount,
		"stagnant_generations", stagnantGens,
		"generation", p.Generation,
	)
}

// minFloat returns the smaller of two float64 values.
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// maxFloat returns the larger of two float64 values.
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// clampFloat restricts a value to the [min, max] range.
func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

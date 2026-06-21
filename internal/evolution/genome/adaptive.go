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
	if count == 0 {
		return 0.0
	}
	return totalDist / float64(count)
}

// toFloat64 attempts to convert an arbitrary value to float64.
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
// operates on p.currentMutationRate (runtime field) instead of p.cfg.MutationRate
// to preserve the user-configured base rate for drift-back.
func (p *Population) adjustMutationRateLocked() {
	div := p.measureDiversityLocked()
	base := p.cfg.MutationRate

	if div < p.cfg.DiversityThreshold {
		p.currentMutationRate = minFloat(p.currentMutationRate*1.5, p.cfg.MaxMutationRate)
	} else if div > p.cfg.DiversityThreshold*2 && p.currentMutationRate > p.cfg.MinMutationRate {
		p.currentMutationRate = maxFloat(p.currentMutationRate*0.85, p.cfg.MinMutationRate)
	} else {
		if p.currentMutationRate > base {
			p.currentMutationRate = maxFloat(p.currentMutationRate*0.95, base)
		} else if p.currentMutationRate < base {
			p.currentMutationRate = minFloat(p.currentMutationRate*1.05, base)
		}
	}

	p.currentMutationRate = clampFloat(p.currentMutationRate, p.cfg.MinMutationRate, p.cfg.MaxMutationRate)
}

// handleStagnationLocked checks for best-score stagnation and resets bottom
// performers if the threshold has been exceeded.
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
	for i := startIdx; i < len(p.Agents); i++ {
		template := p.Agents[p.rng.Intn(startIdx)]
		p.Agents[i].PromptTemplate = template.PromptTemplate
		p.Agents[i].Params = make(map[string]any, len(template.Params))
		for k, v := range template.Params {
			p.Agents[i].Params[k] = v
		}
		p.Agents[i].Score = -1
	}

	p.stagnantGens = 0
	slog.Warn("stagnation detected, reset bottom performers",
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

package genome

import (
	"log/slog"
	"math"
)

// MetaParams are the evolution hyperparameters that the MetaController
// can self-adapt based on observed performance and diversity metrics.
type MetaParams struct {
	// MutationRate is the probability of mutating offspring [0, 1].
	MutationRate float64

	// SurvivalRate is the fraction of top performers to keep [0, 1].
	SurvivalRate float64

	// EliteCount is the number of best individuals preserved unchanged.
	EliteCount int

	// TournamentSize is the number of competitors per tournament (when using tournament selection).
	TournamentSize int

	// SelectionStrategy is the current parent selection algorithm name.
	SelectionStrategy string

	// BreedingPoolRatio is the fraction of survivors eligible as parents [0, 1].
	BreedingPoolRatio float64
}

// MetaConfig controls the behavior of the meta-controller.
type MetaConfig struct {
	// Enabled enables self-adaptation of evolution parameters.
	Enabled bool

	// AdjustmentRate controls how aggressively parameters are tuned [0, 1].
	// Higher values = faster adaptation but less stability. Default 0.1.
	AdjustmentRate float64

	// TargetDiversity is the ideal population diversity level [0, 1].
	// The controller adjusts mutation rate to maintain this target. Default 0.3.
	TargetDiversity float64

	// MinImprovementRate is the minimum acceptable ratio of generations
	// that should show improvement before adjusting other params.
	MinImprovementRate float64

	// ExplorationBias biases the system toward exploration [0, 1].
	// 0 = pure exploitation, 1 = pure exploration. Default 0.5.
	ExplorationBias float64
}

// DefaultMetaConfig returns sensible defaults for meta-evolution.
func DefaultMetaConfig() MetaConfig {
	return MetaConfig{
		Enabled:            false, // Opt-in
		AdjustmentRate:     0.1,
		TargetDiversity:    0.3,
		MinImprovementRate: 0.3,
		ExplorationBias:    0.5,
	}
}

// MetaController adjusts PopulationConfig parameters based on observed
// evolution metrics (diversity, score improvement, stagnation, etc.).
// This enables the system to self-tune its own evolution hyperparameters.
type MetaController struct {
	cfg        MetaConfig
	generation int
}

// NewMetaController creates a meta-controller for self-adapting evolution.
func NewMetaController(cfg MetaConfig) *MetaController {
	return &MetaController{cfg: cfg}
}

// Tune adjusts the population config based on current evolution state.
// Returns true if any parameter was modified.
func (mc *MetaController) Tune(popCfg *PopulationConfig, report DiversityReport, scoreImprovement float64, stagnantGens int) bool {
	if !mc.cfg.Enabled {
		return false
	}

	mc.generation++
	modified := false
	ar := mc.cfg.AdjustmentRate
	exp := mc.cfg.ExplorationBias

	// 1. Adjust mutation rate based on diversity gap.
	diversityGap := report.Overall - mc.cfg.TargetDiversity
	if math.Abs(diversityGap) > 0.05 {
		if diversityGap < 0 {
			// Below target: increase mutation rate to boost exploration.
			delta := ar * (1 + exp) * (-diversityGap)
			popCfg.MutationRate = clamp(
				popCfg.MutationRate*(1+delta),
				popCfg.MinMutationRate,
				popCfg.MaxMutationRate,
			)
		} else {
			// Above target: slightly reduce mutation rate.
			popCfg.MutationRate = clamp(
				popCfg.MutationRate*(1-ar*diversityGap),
				popCfg.MinMutationRate,
				popCfg.MaxMutationRate,
			)
		}
		modified = true
	}

	// 2. Adjust survival rate based on score improvement trend.
	if scoreImprovement < mc.cfg.MinImprovementRate {
		// Low improvement: be more selective (lower survival rate).
		newRate := popCfg.SurvivalRate * (1 - ar)
		if newRate >= 0.2 {
			popCfg.SurvivalRate = newRate
			modified = true
		}
	} else if scoreImprovement > mc.cfg.MinImprovementRate*2 {
		// High improvement: be more generous (higher survival rate).
		newRate := popCfg.SurvivalRate * (1 + ar)
		if newRate <= 0.9 {
			popCfg.SurvivalRate = newRate
			modified = true
		}
	}

	// 3. Adjust elite count based on stagnation.
	if stagnantGens > 3 {
		// Stagnating: reduce elite count to allow more exploration.
		if popCfg.EliteCount > 1 {
			popCfg.EliteCount--
			modified = true
		}
	} else if stagnantGens == 0 && popCfg.EliteCount < 5 {
		// Improving: increase elite preservation.
		if popCfg.EliteCount < popCfg.Size/4 {
			popCfg.EliteCount++
			modified = true
		}
	}

	// 4. Switch selection strategy based on exploration bias.
	if mc.generation%20 == 0 && mc.generation > 0 {
		newStrategy := mc.selectBestStrategy(popCfg, report)
		if newStrategy != popCfg.SelectionStrategy {
			popCfg.SelectionStrategy = newStrategy
			modified = true
		}
	}

	if modified {
		slog.Debug("meta-evolution: adjusted parameters",
			"mutation_rate", popCfg.MutationRate,
			"survival_rate", popCfg.SurvivalRate,
			"elite_count", popCfg.EliteCount,
			"selection", popCfg.SelectionStrategy,
			"generation", mc.generation,
		)
	}

	return modified
}

// selectBestStrategy chooses the best selection strategy based on current state.
// Higher exploration bias → more stochastic selection; lower → more deterministic.
func (mc *MetaController) selectBestStrategy(popCfg *PopulationConfig, report DiversityReport) string {
	exp := mc.cfg.ExplorationBias
	div := report.Overall

	if div < 0.15 {
		// Low diversity: use rank selection (reduces pressure, allows more diversity).
		return "rank"
	}
	if div > 0.5 && exp < 0.3 {
		// High diversity + low exploration bias: use tournament for pressure.
		return "tournament"
	}
	if exp > 0.7 {
		// High exploration: use SUS for uniform sampling.
		return "sus"
	}
	if exp > 0.4 {
		return "roulette"
	}
	return popCfg.SelectionStrategy // Keep current.
}

// ApplyMetaToPopulation applies the meta-tuned parameters to a running population.
// This is called during the evolution cycle to dynamically adjust parameters.
func ApplyMetaToPopulation(pop *Population, controller *MetaController) bool {
	if controller == nil || !controller.cfg.Enabled {
		return false
	}
	pop.mu.Lock()
	defer pop.mu.Unlock()

	if pop.Generation == pop.cfg.MaxStagnantGenerations {
		// Don't tune on first generation (not enough data).
		return false
	}

	report := pop.measureDiversityReportLocked()

	// Calculate score improvement rate over last few generations.
	scoreImprovement := 0.0
	if len(pop.history) >= 3 {
		recent := pop.history[len(pop.history)-3:]
		improvements := 0
		for i := 1; i < len(recent); i++ {
			if recent[i].BestScore > recent[i-1].BestScore {
				improvements++
			}
		}
		scoreImprovement = float64(improvements) / float64(len(recent)-1)
	}

	modified := controller.Tune(&pop.cfg, report, scoreImprovement, pop.stagnantGens)

	// Sync runtime field from tuned config.
	if modified {
		pop.currentMutationRate = pop.cfg.MutationRate
	}

	return modified
}

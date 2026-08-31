// evolution_lifecycle_config.go maps the evolution YAML blocks (design doc
// ga-runtime-evolution-design-zh.md §7) onto the ares_evolution control-plane
// config structs. Keeping the mapping in one place makes the YAML contract
// auditable against the design doc in a single file.
package ares_bootstrap

import (
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
)

// defaultFloat returns v when positive, otherwise def. YAML float knobs are
// all lower-bounded positive thresholds, so 0 means "unset".
func defaultFloat(v, def float64) float64 {
	if v > 0 {
		return v
	}
	return def
}

// defaultInt returns v when positive, otherwise def.
func defaultInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// lifecycleConfigFromYAML builds the evolution.LifecycleConfig from the
// evolution.{lifecycle,gates} YAML blocks (design doc §7). Zero-value YAML
// fields fall back to DefaultLifecycleConfig so an absent YAML section
// preserves the code defaults.
func lifecycleConfigFromYAML(lc ares_config.EvolutionLifecycleConfig, gc ares_config.EvolutionGateConfig) *evolution.LifecycleConfig {
	cfg := evolution.DefaultLifecycleConfig()
	cfg.FitnessWindow = defaultInt(lc.FitnessWindow, cfg.FitnessWindow)
	cfg.MinSamplesBeforeJudge = defaultInt(lc.MinSamplesBeforeJudge, cfg.MinSamplesBeforeJudge)
	cfg.ColdStartScore = defaultFloat(lc.ColdStartScore, cfg.ColdStartScore)
	cfg.Weights = lifecycleWeightsFromYAML(lc)
	if lc.WatchInterval != "" {
		if d, err := time.ParseDuration(lc.WatchInterval); err == nil && d > 0 {
			cfg.WatchInterval = d
		}
		// Invalid or non-positive watch_interval falls back to the default:
		// a broken YAML knob must never stop the watch loop entirely.
	}
	cfg.BlacklistGenerations = defaultInt(lc.BlacklistGenerations, cfg.BlacklistGenerations)
	cfg.Gates.EvalMinScore = defaultFloat(gc.EvalMinScore, cfg.Gates.EvalMinScore)
	cfg.Gates.RequireManualApproval = gc.RequireManualApproval
	return &cfg
}

// lifecycleWeightsFromYAML maps the flat weight knobs onto FitnessWeights.
// When no weight is set at all, the code defaults apply. A partial spec is
// used as-is (zero = excluded from the aggregate because the aggregator
// normalizes by the weight sum at query time) — silently mixing partial
// specs with defaults would produce a blend the operator did not specify.
func lifecycleWeightsFromYAML(lc ares_config.EvolutionLifecycleConfig) evolution.FitnessWeights {
	if lc.OutcomeWeight == 0 && lc.DimensionEvalWeight == 0 && lc.WorkflowWeight == 0 &&
		lc.SchedulerWeight == 0 && lc.RecoveryWeight == 0 {
		return evolution.DefaultFitnessWeights()
	}
	return evolution.FitnessWeights{
		Outcome:       lc.OutcomeWeight,
		DimensionEval: lc.DimensionEvalWeight,
		Workflow:      lc.WorkflowWeight,
		Scheduler:     lc.SchedulerWeight,
		Recovery:      lc.RecoveryWeight,
	}
}

package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	aresconfig "github.com/Timwood0x10/ares/internal/ares_config"
)

// TestAssembleGAConfig_ForwardsTheExperimentLogPath is the hop that made the whole feature
// dead before: the YAML key existed, the wired system could open it, and nothing carried the
// value from one to the other. A unit test on either end would have passed anyway.
func TestAssembleGAConfig_ForwardsTheExperimentLogPath(t *testing.T) {
	cfg := &aresconfig.Config{}
	cfg.Evolution.ExperimentLogPath = "/var/lib/ares/experiments.jsonl"

	gaCfg := assembleGAConfig(context.Background(), cfg, &Components{}, nil, nil)

	assert.Equal(t, "/var/lib/ares/experiments.jsonl", gaCfg.ExperimentLogPath)
}

// TestAssembleGAConfig_EmptyPathStaysEmpty pins the default: no path means no sealing, so a
// deployment that never configured one keeps its previous behaviour exactly.
func TestAssembleGAConfig_EmptyPathStaysEmpty(t *testing.T) {
	cfg := &aresconfig.Config{}

	gaCfg := assembleGAConfig(context.Background(), cfg, &Components{}, nil, nil)

	assert.Empty(t, gaCfg.ExperimentLogPath)
}

// TestAssembleGAConfig_ForwardsTheObjective is the wiring half of M3: the config layer decides the
// default, but unless it reaches the system the gate is decorative. Mirrors the experiment-log
// forwarding test for the same reason — "the key exists" and "the value arrives" are different
// claims, and only the second one changes behaviour.
func TestAssembleGAConfig_ForwardsTheObjective(t *testing.T) {
	cfg := &aresconfig.Config{}
	cfg.Evolution.Objective = "both"

	gaCfg := assembleGAConfig(context.Background(), cfg, &Components{}, nil, nil)

	assert.Equal(t, "both", gaCfg.Objective)
}

// TestAssembleGAConfig_EmptyObjectiveStaysEmpty: forwarding an empty value must not invent a
// default here. The config layer applies "strategy"; doing it twice would hide a config that
// never ran its defaults.
func TestAssembleGAConfig_EmptyObjectiveStaysEmpty(t *testing.T) {
	cfg := &aresconfig.Config{}

	gaCfg := assembleGAConfig(context.Background(), cfg, &Components{}, nil, nil)

	assert.Empty(t, gaCfg.Objective)
}

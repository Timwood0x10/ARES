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

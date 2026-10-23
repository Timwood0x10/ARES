package ares_config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvolutionObjective_DefaultIsStrategy pins the narrow default: a deployment that never
// heard of this knob must not be able to change live runtime state as a side effect of tuning
// strategies.
func TestEvolutionObjective_DefaultIsStrategy(t *testing.T) {
	cfg := &Config{}
	cfg.setDefaults()
	assert.Equal(t, EvolutionObjectiveStrategy, cfg.Evolution.Objective)
}

// TestEvolutionObjective_Validate covers the two-key rule. objective=runtime|both applies patches
// to a running system, so it is accepted only when allow_live is also set: one typo in `objective`
// must not be enough.
func TestEvolutionObjective_Validate(t *testing.T) {
	cases := []struct {
		name      string
		objective string
		allowLive bool
		wantErr   string
	}{
		{"strategy, live off", EvolutionObjectiveStrategy, false, ""},
		{"strategy, live on", EvolutionObjectiveStrategy, true, ""},
		{"runtime, live off", EvolutionObjectiveRuntime, false, "allow_live"},
		{"runtime, live on", EvolutionObjectiveRuntime, true, ""},
		{"both, live off", EvolutionObjectiveBoth, false, "allow_live"},
		{"both, live on", EvolutionObjectiveBoth, true, ""},
		{"unknown value", "sometimes", true, "strategy|runtime|both"},
		{"empty is an error at this layer", "", true, "empty"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			cfg.setDefaults()
			cfg.Evolution.Enabled = true
			cfg.Evolution.Objective = tc.objective
			cfg.Evolution.RuntimePatches.AllowLive = tc.allowLive

			err := cfg.validateEvolution()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestEvolutionObjective_RuntimeErrorNamesBothKeys: the message has to say what to change, because
// the failure is a deliberate two-key gate rather than a bad value.
func TestEvolutionObjective_RuntimeErrorNamesBothKeys(t *testing.T) {
	cfg := &Config{}
	cfg.setDefaults()
	cfg.Evolution.Enabled = true
	cfg.Evolution.Objective = EvolutionObjectiveRuntime

	err := cfg.validateEvolution()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow_live")
	assert.Contains(t, err.Error(), "objective")
}

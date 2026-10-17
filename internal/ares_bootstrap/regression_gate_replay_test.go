package ares_bootstrap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_config"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/eval"
)

// buildRegressionGateT keeps the positional shape the existing wiring tests were written
// against, so a new OPTIONAL input did not turn into nine mechanical edits that could hide
// a real signature change. The replay factory has its own tests below.
func buildRegressionGateT(enabled *bool, client eval.LLMClient, gates ares_config.EvolutionGateConfig) (*evolution.ArenaRegressionGate, error) {
	return buildRegressionGate(regressionGateConfig{Enabled: enabled, Client: client, Gates: gates})
}

// TestBuildRegressionGate_RuntimeReplayRefusesWithoutAFactory: the flag must fail CLOSED.
//
// A silent fall back to the role-play scorer would be the worst outcome available: the
// operator reads "runtime replay is on" in the config, the logs say the gate armed, and
// every score still comes from a model pretending to be the candidate. The missing piece is
// the serving tool binder, which only cmd/ares has, so the message says where to fix it.
func TestBuildRegressionGate_RuntimeReplayRefusesWithoutAFactory(t *testing.T) {
	path := writeSuiteFile(t, suiteYAML)
	gates := ares_config.EvolutionGateConfig{
		EvalSuite:               path,
		RegressionRuntimeReplay: true,
	}

	_, err := buildRegressionGate(regressionGateConfig{
		Enabled: regressionOn(),
		Client:  stubEvalClient{},
		Gates:   gates,
		// Replay deliberately nil: this process never wired a factory.
	})
	require.Error(t, err, "an enabled replay flag without a factory must not build a working gate")
	assert.Contains(t, err.Error(), "regression_runtime_replay")
	assert.Contains(t, err.Error(), "tool binder", "the error must name the piece that is missing and where it lives")
}

// TestBuildRegressionGate_RuntimeReplayArmsWithAFactory: with the factory supplied, the
// gate builds. The scorer's own tests prove the execution step is the runtime — here the
// contract is that supplying the factory is what makes the flag satisfiable.
func TestBuildRegressionGate_RuntimeReplayArmsWithAFactory(t *testing.T) {
	path := writeSuiteFile(t, suiteYAML)
	factory, err := NewCandidateReplayFactory(CandidateReplayConfig{
		ChatClient: replayChatStub{},
		ToolBinder: replayBinderStub{},
	})
	require.NoError(t, err)

	gate, err := buildRegressionGate(regressionGateConfig{
		Enabled: regressionOn(),
		Client:  stubEvalClient{},
		Gates: ares_config.EvolutionGateConfig{
			EvalSuite:               path,
			RegressionRuntimeReplay: true,
		},
		ReplayProvider: func() (*CandidateReplayFactory, error) { return factory, nil },
	})
	require.NoError(t, err)
	assert.NotNil(t, gate, "a wired factory must be enough to arm the replay gate")
}

// TestBuildRegressionGate_DefaultStaysOnTheRolePlayScorer: the flag's default is OFF, and
// "off" must mean the LLM scorer with no factory required at all — otherwise enabling the
// option would quietly become a prerequisite for the ordinary path.
func TestBuildRegressionGate_DefaultStaysOnTheRolePlayScorer(t *testing.T) {
	path := writeSuiteFile(t, suiteYAML)

	gate, err := buildRegressionGate(regressionGateConfig{
		Enabled: regressionOn(),
		Client:  stubEvalClient{},
		Gates:   ares_config.EvolutionGateConfig{EvalSuite: path},
		// No factory, and the flag is off: this must build exactly as before.
	})
	require.NoError(t, err)
	assert.NotNil(t, gate)
}

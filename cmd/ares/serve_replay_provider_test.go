package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
)

// TestInstallCandidateReplayProvider_SurvivesMinimalSetup pins the crash that `ares serve --llm-url`
// hit on this repository's own documented minimal path: an unconditional assignment to
// Components.Evolution.CandidateReplayProvider, where Evolution is nil when no evolution
// configuration was wired. The process panicked before it started listening.
//
// The assertion is that the call returns without panicking and reports "not installed", because the
// replay gate that consumes the provider is off by default — skipping the install is correct, not a
// degradation.
func TestInstallCandidateReplayProvider_SurvivesMinimalSetup(t *testing.T) {
	assert.NotPanics(t, func() {
		installed := installCandidateReplayProvider(context.Background(), &ares_bootstrap.Components{}, nil, nil)
		assert.False(t, installed, "without an evolution subsystem there is nobody to install it on")
	})
}

// TestInstallCandidateReplayProvider_InstallsWhenEvolutionExists is the positive half: with an
// evolution subsystem present the provider must actually be attached, and calling it must reach the
// factory. Without this, the nil-guard test would also pass for an implementation that never installs
// anything.
func TestInstallCandidateReplayProvider_InstallsWhenEvolutionExists(t *testing.T) {
	comp := &ares_bootstrap.Components{Evolution: &ares_bootstrap.EvolutionComponents{}}

	installed := installCandidateReplayProvider(context.Background(), comp, nil, nil)

	assert.True(t, installed, "with evolution present the provider must be installed")
	assert.NotNil(t, comp.Evolution.CandidateReplayProvider, "and stored where the gate reads it")

	assert.NotPanics(t, func() {
		// The factory itself is allowed to reject nil collaborators — what matters here is that the
		// installed closure is reachable and callable, which is the wiring this test covers.
		_, _ = comp.Evolution.CandidateReplayProvider()
	})
}

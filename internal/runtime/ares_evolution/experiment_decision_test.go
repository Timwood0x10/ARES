package evolution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// TestExperimentDecision_ReferencesItsProposal is M10's second half: the verdict names the
// proposal it decided, so a reader can follow one experiment from "we tried this" to "here is
// what the gates did with it".
//
// The link is carried explicitly (ProposalKey) rather than recomputed, because ReplayKey
// covers the fitness weights in force at scoring time and the lifecycle does not have them —
// recomputing would name a different experiment as soon as the defaults changed.
func TestExperimentDecision_ReferencesItsProposal(t *testing.T) {
	ctx := context.Background()
	pop, err := genome.NewPopulation(ctx,
		&mutation.Strategy{ID: "base", Params: map[string]any{"temperature": 0.7}, Score: 0.9},
		&mockGenomeMutator{})
	require.NoError(t, err)
	pop.ScoreAgents(func(*mutation.Strategy) float64 { return 0.6 })

	logSink := NewMemoryExperimentLog()
	adapter := &GenomePopulationAdapter{pop: pop, experimentLog: logSink}

	// The cycle seals the proposal and remembers its key.
	adapter.sealExperimentRecord(ctx)
	best := pop.BestStrategy()
	require.NotNil(t, best)
	proposed := adapter.proposalKeyFor(best.ID)
	require.NotEmpty(t, proposed, "sealing a proposal must record its key")

	// The lifecycle seals the verdict against that key.
	lifecycle := &StrategyLifecycle{}
	lifecycle.SetExperimentLog(logSink, adapter.proposalKeyFor)
	lifecycle.sealDecision(ctx, best, ExperimentOutcomeAccepted, []GateVerdict{{
		Gate: "lifecycle", Passed: true, Reason: "promoted to active",
	}})

	recs, err := logSink.List(ctx)
	require.NoError(t, err)
	require.Len(t, recs, 2, "a proposal and its decision are two records, never one edited record")

	proposal, decision := recs[0], recs[1]
	assert.Equal(t, ExperimentOutcomePending, proposal.Outcome)
	assert.Empty(t, proposal.ProposalKey, "the proposal is the head of the chain")

	assert.Equal(t, ExperimentOutcomeAccepted, decision.Outcome)
	assert.Equal(t, proposed, decision.ProposalKey, "the verdict must name the proposal it decided")
	assert.Equal(t, proposal.ReplayKey(), decision.ProposalKey,
		"and that name must be the proposal's own identity")
}

// TestExperimentDecision_WithoutAResolverStaysUncorrelated: no resolver means the decision is
// recorded WITHOUT a key rather than paired with a guess.
func TestExperimentDecision_WithoutAResolverStaysUncorrelated(t *testing.T) {
	ctx := context.Background()
	logSink := NewMemoryExperimentLog()
	lifecycle := &StrategyLifecycle{}
	lifecycle.SetExperimentLog(logSink, nil)

	lifecycle.sealDecision(ctx, &mutation.Strategy{ID: "cand-x", Score: 0.5},
		ExperimentOutcomeRejected, []GateVerdict{{Gate: "g", Passed: false, Reason: "r"}})

	recs, err := logSink.List(ctx)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Empty(t, recs[0].ProposalKey, "an unknown proposal is reported as unknown, not invented")
	assert.Equal(t, ExperimentOutcomeRejected, recs[0].Outcome)
}

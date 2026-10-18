package evolution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// experimentFixture builds the same experiment twice, with the maps inserted in DIFFERENT
// orders — Go randomises map iteration, so a fingerprint that depends on insertion order would
// make every replay look like a different experiment.
func experimentFixture(order bool) ExperimentRecord {
	params := map[string]any{"temperature": 0.7, "top_k": 30, "budget": 100}
	fitness := map[string]float64{"success_rate": 0.4, "quality": 0.25, "latency": 0.2}
	evidence := []string{"ev-1", "ev-2"}
	if !order {
		params = map[string]any{"budget": 100, "top_k": 30, "temperature": 0.7}
		fitness = map[string]float64{"latency": 0.2, "quality": 0.25, "success_rate": 0.4}
		evidence = []string{"ev-2", "ev-1"}
	}
	return ExperimentRecord{
		SchemaVersion:  ExperimentRecordSchemaVersion,
		Generation:     7,
		CandidateID:    "cand-7",
		ParentID:       "cand-3",
		MutationType:   "parameter_tune",
		PromptTemplate: "precise",
		Params:         params,
		EvidenceIDs:    evidence,
		FitnessConfig:  fitness,
		Dimensions:     map[string]float64{"success_rate": 0.8},
		Score:          0.62,
		Verification:   []GateVerdict{{Gate: "regression", Passed: true}},
		Accepted:       true,
		At:             time.Unix(1000, 0),
	}
}

// TestGA_ExperimentRecord_Reproducible is M10's replay contract: the same experiment must
// yield the same key regardless of map insertion order or slice order, so a recorded result
// can be re-run and compared instead of argued about.
func TestGA_ExperimentRecord_Reproducible(t *testing.T) {
	a := experimentFixture(true)
	b := experimentFixture(false)

	assert.Equal(t, a.ReplayKey(), b.ReplayKey(),
		"map and slice order must not change the identity of an experiment")
	assert.Equal(t, a.ReplayKey(), a.ReplayKey(), "the key is stable across calls")
	assert.NotEmpty(t, a.ReplayKey())
}

// TestGA_ExperimentRecord_IdentityIgnoresTheOutcome: time and score are what a replay
// COMPARES, so folding them into the identity would make "same experiment, different outcome"
// unrepresentable — the one case a reproducibility record exists to capture.
func TestGA_ExperimentRecord_IdentityIgnoresTheOutcome(t *testing.T) {
	base := experimentFixture(true)

	later := base
	later.At = base.At.Add(48 * time.Hour)
	later.Score = 0.91
	assert.Equal(t, base.ReplayKey(), later.ReplayKey())

	scored := base
	scored.Dimensions = map[string]float64{"success_rate": 0.2}
	assert.Equal(t, base.ReplayKey(), scored.ReplayKey(),
		"dimensions are the outcome, not the setup")
}

// TestGA_ExperimentRecord_SeparatesDifferentSetups pins the other direction: two experiments
// that differ in what DETERMINES the result must not share an identity, or a comparison would
// silently match the wrong pair.
func TestGA_ExperimentRecord_SeparatesDifferentSetups(t *testing.T) {
	base := experimentFixture(true)

	otherConfig := base
	otherConfig.FitnessConfig = map[string]float64{"success_rate": 0.5, "quality": 0.25, "latency": 0.2}
	assert.NotEqual(t, base.ReplayKey(), otherConfig.ReplayKey(), "a different fitness config is a different experiment")

	otherEvidence := base
	otherEvidence.EvidenceIDs = []string{"ev-1"}
	assert.NotEqual(t, base.ReplayKey(), otherEvidence.ReplayKey(), "different evidence is a different experiment")

	otherBehaviour := base
	otherBehaviour.PromptTemplate = "concise"
	assert.NotEqual(t, base.ReplayKey(), otherBehaviour.ReplayKey(), "different behaviour is a different experiment")

	otherParam := base
	otherParam.Params = map[string]any{"temperature": 0.9, "top_k": 30, "budget": 100}
	assert.NotEqual(t, base.ReplayKey(), otherParam.ReplayKey(), "a different parameter is a different experiment")
}

// TestMemoryExperimentLog_IsAppendOnly pins the loop cut (M10): the log is history, so it can
// be appended to and read, and a reader cannot rewrite what it read.
func TestMemoryExperimentLog_IsAppendOnly(t *testing.T) {
	ctx := context.Background()
	log := NewMemoryExperimentLog()

	first := experimentFixture(true)
	second := experimentFixture(true)
	second.CandidateID = "cand-8"

	require.NoError(t, log.Append(ctx, first))
	require.NoError(t, log.Append(ctx, second))

	records, err := log.List(ctx)
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, []string{"cand-7", "cand-8"}, []string{records[0].CandidateID, records[1].CandidateID},
		"append order is the record order")

	// Mutating what List returned must not touch the log — including the MAPS: a shallow copy
	// would still share them, and "what configuration produced this result" is exactly what an
	// append-only log must be able to prove.
	records[0].CandidateID = "tampered"
	records[0].Params["temperature"] = 9.9
	records[0].EvidenceIDs[0] = "tampered-ev"
	again, err := log.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, "cand-7", again[0].CandidateID, "a reader must not be able to rewrite history")
	assert.Equal(t, 0.7, again[0].Params["temperature"], "maps must be copied, not shared with the log")
	assert.Equal(t, "ev-1", again[0].EvidenceIDs[0], "slices must be copied too")

	// An unidentifiable record is refused rather than stored.
	assert.Error(t, log.Append(ctx, ExperimentRecord{}), "a record with no candidate id cannot be replayed")
}

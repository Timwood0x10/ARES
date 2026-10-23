package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// counterValue reads a counter without pulling in prometheus/testutil (which is not a direct
// dependency of this module).
func counterValue(c prometheus.Counter) float64 {
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		return -1
	}
	return m.GetCounter().GetValue()
}

// The counters live on a process-wide registry (construction is idempotent), so every assertion
// below uses a label value no other test uses and checks the DELTA. Asserting an absolute value
// would make these tests depend on what ran before them.
func TestEvolutionMetrics_ExperimentOutcome(t *testing.T) {
	m, err := NewPrometheusMetrics()
	require.NoError(t, err)

	const outcome = "audit_test_accepted"
	before := counterValue(m.EvolutionExperimentsTotal.WithLabelValues(outcome))
	m.RecordEvolutionExperiment(outcome)
	m.RecordEvolutionExperiment(outcome)
	assert.Equal(t, before+2, counterValue(m.EvolutionExperimentsTotal.WithLabelValues(outcome)))
}

// TestEvolutionMetrics_SealErrorIsCountedSeparately: a failing sink must be visible as errors
// alongside the attempts, otherwise "0 records" reads as "nothing happened" rather than "every
// write failed".
func TestEvolutionMetrics_SealErrorIsCountedSeparately(t *testing.T) {
	m, err := NewPrometheusMetrics()
	require.NoError(t, err)

	const reason = "audit_test_append"
	before := counterValue(m.EvolutionExperimentSealErrorsTotal.WithLabelValues(reason))
	m.RecordEvolutionExperimentSealError(reason)
	assert.Equal(t, before+1, counterValue(m.EvolutionExperimentSealErrorsTotal.WithLabelValues(reason)))
}

// TestEvolutionMetrics_GateDecisionCountsBothOutcomes: a gate that never rejects and a gate that
// never runs look identical unless passes are counted too.
func TestEvolutionMetrics_GateDecisionCountsBothOutcomes(t *testing.T) {
	m, err := NewPrometheusMetrics()
	require.NoError(t, err)

	const gate = "audit_test_gate"
	passBefore := counterValue(m.EvolutionGateDecisionsTotal.WithLabelValues(gate, "pass"))
	rejectBefore := counterValue(m.EvolutionGateDecisionsTotal.WithLabelValues(gate, "reject"))

	m.RecordEvolutionGateDecision(gate, "pass")
	m.RecordEvolutionGateDecision(gate, "reject")
	m.RecordEvolutionGateDecision(gate, "reject")

	assert.Equal(t, passBefore+1, counterValue(m.EvolutionGateDecisionsTotal.WithLabelValues(gate, "pass")))
	assert.Equal(t, rejectBefore+2, counterValue(m.EvolutionGateDecisionsTotal.WithLabelValues(gate, "reject")))
}

// TestEvolutionMetrics_RuntimePatchResult keeps "ran" and "changed something" apart.
func TestEvolutionMetrics_RuntimePatchResult(t *testing.T) {
	m, err := NewPrometheusMetrics()
	require.NoError(t, err)

	appliedBefore := counterValue(m.EvolutionRuntimePatchesTotal.WithLabelValues("applied"))
	failedBefore := counterValue(m.EvolutionRuntimePatchesTotal.WithLabelValues("failed"))

	m.RecordEvolutionRuntimePatch("applied")
	m.RecordEvolutionRuntimePatch("failed")

	assert.Equal(t, appliedBefore+1, counterValue(m.EvolutionRuntimePatchesTotal.WithLabelValues("applied")))
	assert.Equal(t, failedBefore+1, counterValue(m.EvolutionRuntimePatchesTotal.WithLabelValues("failed")))
}

// TestEvolutionMetrics_NilReceiverIsANoOp keeps the recording calls safe on a deployment that never
// constructed metrics.
func TestEvolutionMetrics_NilReceiverIsANoOp(t *testing.T) {
	var m *PrometheusMetrics
	assert.NotPanics(t, func() {
		m.RecordEvolutionExperiment("accepted")
		m.RecordEvolutionExperimentSealError("append")
		m.RecordEvolutionGateDecision("gate", "pass")
		m.RecordEvolutionRuntimePatch("applied")
	})
}

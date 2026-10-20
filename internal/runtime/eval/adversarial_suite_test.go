package eval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// TestAdversarialSuite_DeclaresItsContract pins the gap-contract corpus itself:
// it must load, and every case must declare the ending it is engineered to
// produce. The expectation travels in the case's Metadata, which is what the
// end-to-end replay reads — a case that quietly lost its expectation would
// otherwise be skipped rather than failed, and the suite would pass by doing
// nothing.
func TestAdversarialSuite_DeclaresItsContract(t *testing.T) {
	suite, err := NewLoader().Load("../../../test/eval/adversarial.yaml")
	require.NoError(t, err)
	require.Len(t, suite.TestCases, 3, "the contract needs a gap case, a truncated case and a control")

	byID := make(map[string]TestCase, len(suite.TestCases))
	for _, tc := range suite.TestCases {
		byID[tc.ID] = tc
	}

	gap, ok := byID["adv-gap-001"]
	require.True(t, ok, "the gap case must exist")
	assert.Equal(t, ares_events.AnswerBodySourceGap, gap.Metadata["expected_body_source"])
	assert.Contains(t, gap.Tags, "gap")

	truncated, ok := byID["adv-trunc-001"]
	require.True(t, ok, "the truncated case must exist")
	assert.Equal(t, true, truncated.Metadata["expected_truncated"])
	assert.Contains(t, truncated.Tags, "truncated")

	// The control is not optional: without it, an implementation that scored
	// EVERY case low would satisfy the other two assertions.
	control, ok := byID["adv-control-001"]
	require.True(t, ok, "the control case must exist")
	assert.Equal(t, ares_events.AnswerBodySourceSupplied, control.Metadata["expected_body_source"])
	assert.Contains(t, control.Tags, "control")
	assert.NotEmpty(t, control.ExpectedOutput, "the control carries a golden reference for the judge")
}

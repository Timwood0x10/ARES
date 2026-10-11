package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestStepFile_InterruptYAML verifies that the YAML author-facing
// StepFile.Interrupt field correctly parses an interrupt block and that
// convertStep maps it onto engine.Step.Interrupt.
func TestStepFile_InterruptYAML(t *testing.T) {
	yamlData := `
steps:
  - id: deploy
    agent_type: tool/shell
    input: "deploy production"
    interrupt:
      message: "approve before deploy"
`
	var wf WorkflowFile
	err := yaml.Unmarshal([]byte(yamlData), &wf)
	require.NoError(t, err)
	require.Len(t, wf.Steps, 1)

	sf := wf.Steps[0]
	require.NotNil(t, sf.Interrupt)
	assert.Equal(t, "approve before deploy", sf.Interrupt.Message)

	step, err := convertStep(sf)
	require.NoError(t, err)
	require.NotNil(t, step.Interrupt)
	assert.Equal(t, "approve before deploy", step.Interrupt.Message)
}

// TestStepFile_NoInterruptYAML verifies that a step without an interrupt
// field produces a nil Interrupt on the engine.Step.
func TestStepFile_NoInterruptYAML(t *testing.T) {
	yamlData := `
steps:
  - id: safe
    agent_type: tool/shell
    input: "safe operation"
`
	var wf WorkflowFile
	err := yaml.Unmarshal([]byte(yamlData), &wf)
	require.NoError(t, err)
	require.Len(t, wf.Steps, 1)

	sf := wf.Steps[0]
	assert.Nil(t, sf.Interrupt)

	step, err := convertStep(sf)
	require.NoError(t, err)
	assert.Nil(t, step.Interrupt)
}

// TestStepFile_InterruptWithPayload verifies that the interrupt payload is
// carried through from YAML to the engine step.
func TestStepFile_InterruptWithPayload(t *testing.T) {
	yamlData := `
steps:
  - id: risky
    agent_type: tool/shell
    input: "risky operation"
    interrupt:
      message: "approve risky op"
      payload:
        risk_level: high
        max_cost: 1000
`
	var wf WorkflowFile
	err := yaml.Unmarshal([]byte(yamlData), &wf)
	require.NoError(t, err)
	require.Len(t, wf.Steps, 1)

	step, err := convertStep(wf.Steps[0])
	require.NoError(t, err)
	require.NotNil(t, step.Interrupt)
	assert.Equal(t, "approve risky op", step.Interrupt.Message)
	assert.Equal(t, "high", step.Interrupt.Payload["risk_level"])
}

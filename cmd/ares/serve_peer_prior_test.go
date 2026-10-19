package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestBuildToolClassDAG_CarriesADeclaredPrior pins the only prior this build can honestly
// produce: the one the tool author declares.
//
// It is not a measurement. Nothing observes per-tool outcomes today, and a number invented here
// would reach the model's prompt looking exactly like a measured one.
func TestBuildToolClassDAG_CarriesADeclaredPrior(t *testing.T) {
	dag, err := buildToolClassDAG([]core_tools.ToolSchema{
		{Name: "grep", Description: "search", Tags: map[string]string{l1MetaPriorTag: "0.8"}},
		{Name: "read", Description: "read"},
	})
	require.NoError(t, err)

	var priors map[string]string
	for _, step := range dag.Steps() {
		switch step.Name {
		case "grep":
			priors = step.Metadata
		case "read":
			assert.Empty(t, step.Metadata[l1MetaPrior],
				"a tool that declares no prior must keep it empty rather than inherit a default")
		}
	}
	require.NotNil(t, priors)
	assert.Equal(t, "0.8", priors[l1MetaPrior])
}

// TestBuildToolClassDAG_CarriesDeclaredGateFields: enabled and budget are declarable for the same
// reason prior is — otherwise they are values with no producer, and a reader cannot tell
// "permissive by default" from "nobody ever set this".
//
// The declarations are also the only route that SURVIVES A RESTART: the graph is rebuilt from
// the schemas at every boot, so a runtime writer's edits would be discarded with it.
func TestBuildToolClassDAG_CarriesDeclaredGateFields(t *testing.T) {
	dag, err := buildToolClassDAG([]core_tools.ToolSchema{
		{Name: "legacy", Tags: map[string]string{l1MetaEnabledTag: "false", l1MetaBudgetTag: "50"}},
		{Name: "plain"},
	})
	require.NoError(t, err)

	byName := map[string]map[string]string{}
	for _, step := range dag.Steps() {
		byName[step.Name] = step.Metadata
	}
	assert.Equal(t, "false", byName["legacy"][l1MetaEnabled])
	assert.Equal(t, "50", byName["legacy"][l1MetaBudget])

	// Undeclared keeps the permissive default, and it is visible as the default rather than as
	// an empty string that the planner would have to interpret.
	assert.Equal(t, "true", byName["plain"][l1MetaEnabled])
	assert.Equal(t, "0", byName["plain"][l1MetaBudget])
}

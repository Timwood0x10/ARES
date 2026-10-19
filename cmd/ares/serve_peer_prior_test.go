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

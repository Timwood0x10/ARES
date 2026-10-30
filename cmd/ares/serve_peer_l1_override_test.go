package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_config"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestToolClassSpecs_AppliesOperatorOverrides pins the merge rule the plan states: schema
// declaration (or its default) first, explicitly configured field second, everything else untouched.
//
// Field-by-field matters more than it looks: a whole-entry replacement would let `budget: 8` silently
// reset `prior` and `enabled` to their zero values, which reads as "the tool was disabled" the next
// time the planner runs.
func TestToolClassSpecs_AppliesOperatorOverrides(t *testing.T) {
	// Order-independent: restore whatever the wiring left behind.
	t.Cleanup(func() { setToolClassOverrides(nil) })

	budget := 8
	maxAttempts := 1
	setToolClassOverrides(map[string]ares_config.L1ToolOverride{
		"rg": {Budget: &budget, MaxAttempts: &maxAttempts, TaskTimeout: "30s"},
	})

	specs := toolClassSpecs([]core_tools.ToolSchema{
		{Name: "rg", Description: "search", Tags: map[string]string{
			l1MetaEnabledTag: "true",
			l1MetaBudgetTag:  "0", // schema says unlimited; the operator overrides it
			l1MetaPriorTag:   "prefer rg for code search",
		}},
		{Name: "git", Description: "vcs"},
	})
	require.Len(t, specs, 2)

	rg := specs[0].Metadata
	assert.Equal(t, "8", rg[l1MetaBudget], "the configured budget must win over the schema default")
	assert.Equal(t, "1", rg[l1MetaMaxAttempts])
	assert.Equal(t, "30s", rg[l1MetaTaskTimeout])
	assert.Equal(t, "true", rg[l1MetaEnabled], "an untouched field keeps the schema declaration")
	assert.Equal(t, "prefer rg for code search", rg[l1MetaPrior], "and so does prior")

	git := specs[1].Metadata
	assert.NotContains(t, git, l1MetaMaxAttempts, "a tool nobody configured must not gain a retry policy")
	assert.NotContains(t, git, l1MetaTaskTimeout)
}

// TestToolClassSpecs_ExplicitFalseIsNotAbsent: the declaration has to distinguish "the operator said
// no" from "the operator said nothing", which is why the config fields are pointers.
func TestToolClassSpecs_ExplicitFalseIsNotAbsent(t *testing.T) {
	t.Cleanup(func() { setToolClassOverrides(nil) })

	disabled := false
	setToolClassOverrides(map[string]ares_config.L1ToolOverride{"rg": {Enabled: &disabled}})

	specs := toolClassSpecs([]core_tools.ToolSchema{{Name: "rg"}})
	require.Len(t, specs, 1)
	assert.Equal(t, "false", specs[0].Metadata[l1MetaEnabled])
}

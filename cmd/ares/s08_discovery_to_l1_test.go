package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/tools/discovery"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestS08_DiscoveryToL1OnlyPresentCommandsBecomeCallable is the S08 closure: a host that has one of
// the allowlisted commands and not the other must produce a catalogue containing exactly the one
// that was found — and a declaration in kernel.l1.tools must not conjure the missing one into
// existence.
//
// The lookup and exec are faked on purpose: the point is the path from discovery to the catalogue,
// not whether this machine happens to have a particular binary.
func TestS08_DiscoveryToL1OnlyPresentCommandsBecomeCallable(t *testing.T) {
	t.Cleanup(func() { setToolClassOverrides(nil) })

	discoverer := discovery.NewDiscoverer(
		[]string{"present-tool", "missing-tool"},
		discovery.WithLookup(func(name string) (string, error) {
			if name == "present-tool" {
				return "/usr/bin/present-tool", nil
			}
			return "", errors.New("executable file not found in $PATH")
		}),
		discovery.WithExec(func(context.Context, string, []string) ([]byte, error) {
			return []byte("present-tool - searches the notes"), nil
		}),
	)

	toolsFound, err := discoverer.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, toolsFound, 1, "only the installed command may be discovered")

	registry := core_tools.NewRegistry()
	for _, tool := range toolsFound {
		require.NoError(t, registry.Register(tool))
	}

	// The operator declares both, which is reasonable: the same file is deployed to hosts that have
	// different software installed.
	enabled := true
	setToolClassOverrides(map[string]ares_config.L1ToolOverride{
		"present-tool": {Enabled: &enabled},
		"missing-tool": {Enabled: &enabled},
	})

	schemas := registry.GetSchemas()
	specs := toolClassSpecs(schemas)

	require.Len(t, specs, 1, "a declaration must not add a tool the host does not have")
	assert.Equal(t, "present-tool", specs[0].Name)
	assert.NotContains(t, specs[0].Metadata[l1MetaEnabled], "false",
		"the declaration for the found tool still applies")

	// The record has to be checkable without inventing a second store: the catalogue node IS the
	// record, so its identity and description must match what the registry registered.
	found := toolsFound[0]
	assert.Equal(t, found.Name(), specs[0].Name)
	assert.Equal(t, "tool/"+found.Name(), specs[0].AgentType,
		"the node must execute as the tool it describes")
	assert.Equal(t, found.Description(), specs[0].Description,
		"the description the model reads must be the one discovery reported")
	assert.Equal(t, core_tools.ToolClassID(found.Name(), core_tools.ToolArgShape(core_tools.ToolSchema{
		Name:       found.Name(),
		Parameters: found.Parameters(),
	})), specs[0].NodeID,
		"and the node id must be derivable from the same schema the registry exposes")

	// The unmatched entry is reported, not silently accepted as working.
	assert.Equal(t, []string{"missing-tool"}, unmatchedToolClassOverrides(schemas),
		"the unmatched declaration has to be visible: otherwise the operator sees a working config")
}

package sub

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// taggableTool is the smallest tool that can carry tags, which is how an MCP tool arrives: the
// bridge never binds it by hand, it lands in the registry with its own tag map.
type taggableTool struct {
	name string
	tags map[string]string
}

func (t taggableTool) Name() string                      { return t.name }
func (t taggableTool) Description() string               { return "test tool" }
func (t taggableTool) Category() core.ToolCategory       { return core.CategoryCore }
func (t taggableTool) Capabilities() []core.Capability   { return nil }
func (t taggableTool) Parameters() *core.ParameterSchema { return nil }
func (t taggableTool) Tags() map[string]string           { return t.tags }
func (t taggableTool) Execute(context.Context, map[string]any) (core.Result, error) {
	return core.Result{}, nil
}

// idempotentTool additionally implements the code-level declaration.
type idempotentTool struct {
	taggableTool
	idempotent bool
}

func (t idempotentTool) IsIdempotent() bool { return t.idempotent }

// TestIsToolIdempotent_ReadsTheToolsOwnDeclaration is the half that makes retry safety reachable
// for tools the runtime never binds by hand.
//
// Before this, IsToolIdempotent only looked at BindTool/BindIdempotentTool, so every MCP tool
// answered false — including pure reads — and an ambiguous failure killed the task.
func TestIsToolIdempotent_ReadsTheToolsOwnDeclaration(t *testing.T) {
	cases := []struct {
		name string
		tool core.Tool
		want bool
	}{
		{"idempotent tag", taggableTool{name: "read", tags: map[string]string{"idempotent": "true"}}, true},
		{"tag spelled as 1", taggableTool{name: "read", tags: map[string]string{"idempotent": "1"}}, true},
		{"no effect declared", taggableTool{name: "read", tags: map[string]string{"side_effects": "false"}}, true},
		{"state-mutating tool", taggableTool{name: "write", tags: map[string]string{"side_effects": "true"}}, false},
		{"mutates_state false", taggableTool{name: "read", tags: map[string]string{"mutates_state": "false"}}, true},
		{"no declaration at all", taggableTool{name: "write", tags: nil}, false},
		{"typo is not a declaration", taggableTool{name: "write", tags: map[string]string{"idempotent": "ture"}}, false},
		{"interface beats a conflicting tag", idempotentTool{
			taggableTool: taggableTool{name: "write", tags: map[string]string{"side_effects": "true"}},
			idempotent:   true,
		}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := core.NewRegistry()
			require.NoError(t, registry.Register(tc.tool))
			binder := NewToolBinder()
			binder.BridgeFromRegistry(registry)

			assert.Equal(t, tc.want, binder.IsToolIdempotent(tc.tool.Name()))
		})
	}
}

// TestIsToolIdempotent_BindingStillWins: a tool bound as idempotent keeps that answer even when the
// registry says nothing, so existing wiring (and pure tools bound without a registry) is unaffected.
func TestIsToolIdempotent_BindingStillWins(t *testing.T) {
	binder, ok := NewToolBinder().(*toolBinder)
	require.True(t, ok)
	binder.BindIdempotentTool("calc", func(context.Context, map[string]any) (any, error) { return 1, nil })

	assert.True(t, binder.IsToolIdempotent("calc"))
	assert.False(t, binder.IsToolIdempotent("unbound"))
}

// TestIsToolIdempotent_UnparseableValueIsNotADeclaration: "not true" must not be read as "false".
// Collapsing the two would let a typo ("idempotent: ture", "side_effects: flase") authorise retries
// on a tool that never declared itself safe.
func TestIsToolIdempotent_UnparseableValueIsNotADeclaration(t *testing.T) {
	for _, tags := range []map[string]string{
		{"side_effects": "flase"},
		{"mutates_state": "nope"},
		{"idempotent": "ture"},
	} {
		registry := core.NewRegistry()
		require.NoError(t, registry.Register(taggableTool{name: "write", tags: tags}))
		binder := NewToolBinder()
		binder.BridgeFromRegistry(registry)

		assert.False(t, binder.IsToolIdempotent("write"), "tags %v must not count as a declaration", tags)
	}
}

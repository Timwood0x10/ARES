package toolsource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/tools/planner"
	core "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// newResolverFromRegistry wires a real planner.ToolResolver+ToolScorer to a
// registry so tests exercise the genuine resolution+scoring path.
func newResolverFromRegistry(t *testing.T, reg *core.Registry) (planner.ToolResolver, planner.ToolScorer) {
	t.Helper()
	provider := planner.NewRegistryProvider(reg)
	resolver, err := planner.NewToolResolver(provider)
	require.NoError(t, err)
	return resolver, planner.NewToolScorer()
}

func TestCapabilitySelector_PicksTopCandidate(t *testing.T) {
	reg := core.NewRegistry()
	calc := &testTool{name: "calculator", description: "math calc", capabilities: []core.Capability{core.CapabilityMath}}
	strUtil := &testTool{name: "string_utils", description: "string ops", capabilities: []core.Capability{core.CapabilityText}}
	require.NoError(t, reg.Register(calc))
	require.NoError(t, reg.Register(strUtil))

	resolver, scorer := newResolverFromRegistry(t, reg)
	sel := NewCapabilitySelector(resolver, scorer, nil) // nil → DefaultCapabilityExtractor

	available := []core.Tool{calc, strUtil}
	selected, err := sel.Select(context.Background(), "calculate 2+2", available)
	require.NoError(t, err)
	require.Len(t, selected, 1, "should pick the single best tool for Arithmetic")
	assert.Equal(t, "calculator", selected[0].Name())
}

func TestCapabilitySelector_FallbackNoCapabilityExtracted(t *testing.T) {
	reg := core.NewRegistry()
	resolver, scorer := newResolverFromRegistry(t, reg)
	sel := NewCapabilitySelector(resolver, scorer, nil)

	available := []core.Tool{&testTool{name: "x"}, &testTool{name: "y"}}
	// "hello world" maps to no capability.
	selected, err := sel.Select(context.Background(), "hello world", available)
	require.NoError(t, err)
	assert.Equal(t, available, selected, "no capability extracted → return all available")
}

func TestCapabilitySelector_FallbackResolverFindsNothing(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&testTool{name: "calculator", capabilities: []core.Capability{core.CapabilityMath}}))
	resolver, scorer := newResolverFromRegistry(t, reg)
	// Extractor emits a capability the resolver has no tools for.
	sel := NewCapabilitySelector(resolver, scorer, func(string) []string { return []string{"NonexistentCapability"} })

	available := []core.Tool{&testTool{name: "calculator"}}
	selected, err := sel.Select(context.Background(), "anything", available)
	require.NoError(t, err)
	assert.Equal(t, available, selected, "resolver finds nothing → return all available")
}

func TestCapabilitySelector_FallbackCandidateNotInAvailable(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&testTool{name: "calculator", capabilities: []core.Capability{core.CapabilityMath}}))
	resolver, scorer := newResolverFromRegistry(t, reg)
	sel := NewCapabilitySelector(resolver, scorer, nil)

	// available does NOT contain "calculator", so the top candidate cannot be
	// matched back to an available tool.
	available := []core.Tool{&testTool{name: "string_utils"}}
	selected, err := sel.Select(context.Background(), "calculate 2+2", available)
	require.NoError(t, err)
	assert.Equal(t, available, selected, "top candidate not in available → return all available")
}

func TestCapabilitySelector_DedupSameToolAcrossCapabilities(t *testing.T) {
	reg := core.NewRegistry()
	calc := &testTool{name: "calculator", capabilities: []core.Capability{core.CapabilityMath}}
	require.NoError(t, reg.Register(calc))
	resolver, scorer := newResolverFromRegistry(t, reg)
	// Extractor emits two capabilities both resolved to "calculator".
	sel := NewCapabilitySelector(resolver, scorer, func(string) []string {
		return []string{"Arithmetic", "Summation"}
	})

	available := []core.Tool{calc}
	selected, err := sel.Select(context.Background(), "sum the numbers", available)
	require.NoError(t, err)
	require.Len(t, selected, 1, "same tool resolved twice must be deduped")
	assert.Equal(t, "calculator", selected[0].Name())
}

func TestCapabilitySelector_NilDepsReturnsAll(t *testing.T) {
	cases := []struct {
		name string
		sel  *CapabilitySelector
	}{
		{"nil_resolver", NewCapabilitySelector(nil, planner.NewToolScorer(), nil)},
		{"nil_scorer", NewCapabilitySelector(nil, nil, nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			available := []core.Tool{&testTool{name: "a"}}
			out, err := tc.sel.Select(context.Background(), "calculate", available)
			require.NoError(t, err)
			assert.Equal(t, available, out, "nil resolver/scorer → graceful fallback to all")
		})
	}
}

func TestDefaultCapabilityExtractor(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"math", "calculate the sum", []string{"Arithmetic", "Summation"}},
		{"json", "parse the json", []string{"JSONProcessing"}},
		{"http", "make an http request to the url", []string{"HTTPRequest"}},
		{"none", "hello world", nil},
		{"empty", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultCapabilityExtractor(tc.in)
			assert.ElementsMatch(t, tc.want, got)
		})
	}
}

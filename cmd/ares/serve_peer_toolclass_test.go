package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ares_bootstrap "github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// fakeToolSchemas is the whole seam the reconciler needs: one method. Implementing all seven of
// sub.ToolBinder would have meant faking execution and idempotency to publish a list.
type fakeToolSchemas struct {
	mu      sync.Mutex
	schemas []core_tools.ToolSchema
}

func (f *fakeToolSchemas) GetToolSchemas() []core_tools.ToolSchema {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core_tools.ToolSchema(nil), f.schemas...)
}

func (f *fakeToolSchemas) set(s []core_tools.ToolSchema) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.schemas = s
}

func toolSchema(name, desc string, tags map[string]string) core_tools.ToolSchema {
	return core_tools.ToolSchema{Name: name, Description: desc, Tags: tags}
}

// TestToolClassSpecs_MapsTagsAndDeduplicates pins the single mapping both the boot builder and the
// reconciler use. A drift here means a tool described at startup and the same tool seen at runtime
// produce different L1 metadata, which is worse than no metadata at all.
func TestToolClassSpecs_MapsTagsAndDeduplicates(t *testing.T) {
	specs := toolClassSpecs([]core_tools.ToolSchema{
		toolSchema("grep", "search files", map[string]string{"enabled": "false", "budget": "5", "prior": "0.2"}),
		toolSchema("grep", "search files", nil), // same name+shape → same ToolClass
		toolSchema("", "no name", nil),          // unusable
		toolSchema("plain", "no tags", nil),
	})

	require.Len(t, specs, 2, "the duplicate ToolClass must collapse and the nameless schema must be skipped")

	byName := make(map[string]map[string]string, len(specs))
	for _, spec := range specs {
		byName[spec.Name] = spec.Metadata
	}

	assert.Equal(t, "false", byName["grep"]["enabled"], "a declared disable must survive")
	assert.Equal(t, "5", byName["grep"]["budget"])
	assert.Equal(t, "0.2", byName["grep"]["prior"])

	// Absent tags fall back to the permissive defaults, which is what keeps an undeclared tool
	// usable rather than silently disabled.
	assert.Equal(t, "true", byName["plain"]["enabled"])
	assert.Equal(t, "0", byName["plain"]["budget"])
	assert.Equal(t, "", byName["plain"]["prior"])
}

// TestStartToolClassReconciler_AlignsFromTheBinder is the wiring test: a tool that appears in the
// registry AFTER startup must reach L1 without a restart. That is the whole point of the periodic
// pass — the boot-only builder is what left runtime-added tools outside the governance surface.
func TestStartToolClassReconciler_AlignsFromTheBinder(t *testing.T) {
	dag, err := engine.NewMutableDAG(nil)
	require.NoError(t, err)

	src := &fakeToolSchemas{}
	src.set([]core_tools.ToolSchema{toolSchema("grep", "search files", nil)})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startToolClassReconcilerWithInterval(ctx, nil, dag, src, 10*time.Millisecond)

	// Polling with a timeout, not a sleep: the assertion is about the outcome, and a fixed sleep
	// would either be flaky or slow.
	require.Eventually(t, func() bool {
		return dag.CountByAgentType("tool/grep") == 1
	}, 2*time.Second, 10*time.Millisecond, "the registry entry must reach the graph without a restart")

	// A tool that disappears must disappear from the graph too: a stale node keeps advertising a
	// capability that no longer exists.
	//
	// The replacement set is NON-empty on purpose. An empty catalogue is deliberately treated as
	// "this tick could not read the registry" and skipped (ReconcileToolClassDAG), because wiping
	// the graph would make every disabled tool permissive again.
	src.set([]core_tools.ToolSchema{toolSchema("other", "replacement", nil)})
	require.Eventually(t, func() bool {
		return dag.CountByAgentType("tool/grep") == 0 && dag.CountByAgentType("tool/other") == 1
	}, 2*time.Second, 10*time.Millisecond)
}

// TestStartToolClassReconciler_NoWorkWithoutInputs: a nil graph or source must not start a worker.
// Silently doing nothing is the correct behaviour here — the caller has nothing to reconcile — but
// starting a goroutine that can never make progress is not.
func TestStartToolClassReconciler_NoWorkWithoutInputs(t *testing.T) {
	assert.NotPanics(t, func() {
		startToolClassReconcilerWithInterval(context.Background(), nil, nil, &fakeToolSchemas{}, 10*time.Millisecond)
		startToolClassReconcilerWithInterval(context.Background(), nil, nil, nil, 10*time.Millisecond)
	})
}

// TestInjectToolClassDAG_ZeroToolsAtBootStillFillsLater is the P1-4 case: a deployment whose tools
// arrive after startup (an MCP server connecting) used to keep a nil L1 graph forever, because the
// injection skipped everything when the boot-time schema list was empty.
//
// It exercises the whole wiring at once: empty graph injected, worker started under the component
// supervisor, registry change triggering an immediate pass instead of waiting for the interval.
func TestInjectToolClassDAG_ZeroToolsAtBootStillFillsLater(t *testing.T) {
	comp := &ares_bootstrap.Components{NewEvolution: &ares_bootstrap.NewEvolutionComponents{}}
	src := &fakeToolSchemas{} // empty at boot

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	injectToolClassDAG(ctx, comp, src)

	dag := comp.NewEvolution.ToolClassDAG()
	require.NotNil(t, dag, "an empty graph must still be injected, or the reconciler has nothing to fill")
	require.Equal(t, 0, len(dag.Steps()), "boot had no tools")

	// The tool shows up later.
	src.set([]core_tools.ToolSchema{toolSchema("late", "arrived after boot", map[string]string{"enabled": "true"})})
	notifyToolRegistryChanged()

	require.Eventually(t, func() bool {
		return len(comp.NewEvolution.ToolClassDAG().Steps()) == 1
	}, 2*time.Second, 10*time.Millisecond, "a runtime-attached tool must reach L1 without a restart")

	cancel()
	comp.WaitBackground()
}

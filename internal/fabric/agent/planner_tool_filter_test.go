package agentfabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// tagBinder is a ToolBinder whose schemas carry tags, so the advertisement filter can be
// exercised without a real registry.
type tagBinder struct{ schemas []resources.ToolSchema }

func (b *tagBinder) CallTool(context.Context, string, map[string]any) (any, error) {
	return nil, nil
}
func (b *tagBinder) ListTools() []string                    { return nil }
func (b *tagBinder) IsToolIdempotent(string) bool           { return true }
func (b *tagBinder) GetToolSchemas() []resources.ToolSchema { return b.schemas }

func filterBinder() *tagBinder {
	return &tagBinder{schemas: []resources.ToolSchema{
		{Name: "web.search", Tags: map[string]string{"domain": "weather"}},
		{Name: "grep", Tags: map[string]string{"domain": "code_read"}},
		{Name: "read", Tags: map[string]string{"domain": "code_read"}},
		{Name: "untagged"},
	}}
}

// TestPlannerCognition_AdvertisedToolSchemas pins the three behaviours of the filter: empty
// means everything (the previous behaviour), a known tag narrows the prompt, and a tag that
// matches nothing FAILS OPEN.
//
// Fail-open is the one that matters: a stale filter must degrade to "like today", never to an
// empty tool list — an empty advertisement cannot be told apart from "the tool does not exist"
// and nothing in the transcript would show that a filter was the cause.
func TestPlannerCognition_AdvertisedToolSchemas(t *testing.T) {
	binder := filterBinder()

	// 1. No filter: everything, exactly as before this feature existed.
	c := &plannerCognition{binder: binder}
	assert.Len(t, c.advertisedToolSchemas(nil), 4)

	// 2. Known tag: only the matching tools.
	c.toolTagFilter = map[string]string{"domain": "code_read"}
	got := c.advertisedToolSchemas(nil)
	require.Len(t, got, 2)
	for _, s := range got {
		assert.Equal(t, "code_read", s.Tags["domain"])
	}

	// 3. Unknown tag: fail open to the full set rather than advertising nothing.
	c.toolTagFilter = map[string]string{"domain": "does-not-exist"}
	assert.Len(t, c.advertisedToolSchemas(nil), 4,
		"a filter that matches nothing must degrade to every tool, not to none")

	// 4. A nil binder stays nil rather than panicking: the constructor rejects it, but the
	//    method is also reachable from tests that build the struct directly.
	empty := &plannerCognition{}
	assert.Empty(t, empty.advertisedToolSchemas(nil))
}

// TestPlannerCognition_CapabilityFlagsNarrowPerQuantum is the dynamic-loading half: the task
// carries capability flags in its payload, and each quantum advertises only the tools that
// carry at least one of them.
//
// Union (any), not intersection: Tags is map[string]string with one value per key, so a task
// needing weather AND code reading cannot AND two values onto one key — it would match
// nothing and fall open, i.e. route nowhere.
func TestPlannerCognition_CapabilityFlagsNarrowPerQuantum(t *testing.T) {
	binder := &tagBinder{schemas: []resources.ToolSchema{
		{Name: "web.search", Tags: map[string]string{"cap_network": "true"}},
		{Name: "grep", Tags: map[string]string{"cap_file": "true"}},
		{Name: "read", Tags: map[string]string{"cap_file": "true"}},
		{Name: "sql.exec", Tags: map[string]string{"cap_sql": "true"}},
	}}
	c := &plannerCognition{binder: binder}

	// The multi-domain task from the design: weather + code reading.
	caps := explicitTaskCapabilities(&models.Task{Payload: map[string]any{
		TaskToolCapabilityKey: []any{"network", "file"},
	}})
	require.Equal(t, []string{"network", "file"}, caps)

	got := c.advertisedToolSchemas(caps)
	require.Len(t, got, 3, "either flag is enough: the union of both domains")
	for _, s := range got {
		assert.NotEqual(t, "sql.exec", s.Name, "a capability nobody asked for must not be advertised")
	}

	// No flags: the previous behaviour (everything) is preserved.
	assert.Len(t, c.advertisedToolSchemas(nil), 4)

	// Unknown flags fail open rather than advertising nothing.
	assert.Len(t, c.advertisedToolSchemas([]string{"cap_nope"}), 4)

	// A comma-separated payload string is accepted too (the CLI-shaped form).
	commaCaps := explicitTaskCapabilities(&models.Task{Payload: map[string]any{
		TaskToolCapabilityKey: "cap_sql, cap_network",
	}})
	assert.Len(t, c.advertisedToolSchemas(commaCaps), 2)
}

// TestPlannerCognition_DisabledToolsAreNeverAdvertised is the policy half: L1 `enabled=false`
// is not routing, so the fail-open above must not resurrect it. Advertising a tool whose node
// can never grow spends a turn to produce nothing.
func TestPlannerCognition_DisabledToolsAreNeverAdvertised(t *testing.T) {
	binder := &tagBinder{schemas: []resources.ToolSchema{
		{Name: "grep", Tags: map[string]string{"cap_file": "true"}},
		{Name: "read", Tags: map[string]string{"cap_file": "true"}},
	}}
	l1, err := engine.NewMutableDAG([]*engine.Step{{
		// The node id must be the ToolClass id the planner computes for THIS schema, otherwise
		// isToolEnabled would miss the node and fail open.
		ID:        resources.ToolClassID("grep", resources.ToolArgShape(binder.schemas[0])),
		Name:      "grep",
		AgentType: "tool/grep",
		Metadata:  map[string]string{l1MetaEnabled: "false"},
	}})
	require.NoError(t, err)

	c := &plannerCognition{binder: binder, l1: l1}
	all := c.advertisedToolSchemas(nil)
	require.Len(t, all, 1, "the disabled tool must not be advertised even with no filter")
	assert.Equal(t, "read", all[0].Name)

	// And it stays out even when the routing matches nothing (which would otherwise fail open).
	got := c.advertisedToolSchemas([]string{"cap_nope"})
	require.Len(t, got, 1)
	assert.Equal(t, "read", got[0].Name)
}

// TestToolCapabilitiesForTask covers the producer half: without it the routing code exists but
// nothing ever narrows, which is the "wired but never fires" shape this repository keeps
// closing. Explicit payload tags win; otherwise the request text is tagged by keyword; an
// unrecognised request yields NO caps, which advertises everything (a wrong cap would hide a
// tool the model needed, a missing cap only costs tokens).
func TestToolCapabilitiesForTask(t *testing.T) {
	// Explicit payload tags win over the description.
	explicit := models.NewTask("t1", "coder", nil)
	explicit.Payload[TaskToolCapabilityKey] = []string{"file"}
	explicit.Payload["request"] = "查询北京天气"
	assert.Equal(t, []string{"file"}, toolCapabilitiesForTask(explicit),
		"an explicitly tagged task must not be re-derived")

	// Derived from the request text when the payload carries no tags.
	derived := models.NewTask("t2", "coder", nil)
	derived.Payload["request"] = "帮我查一下北京今天的天气，然后读 main.go 的 TODO"
	assert.Contains(t, toolCapabilitiesForTask(derived), "network")
	assert.Contains(t, toolCapabilitiesForTask(derived), "file")

	// Nothing recognised: no narrowing at all.
	quiet := models.NewTask("t3", "coder", nil)
	quiet.Payload["request"] = "嗯"
	assert.Empty(t, toolCapabilitiesForTask(quiet))

	// And the advertisement actually follows from it.
	binder := &tagBinder{schemas: []resources.ToolSchema{
		{Name: "web.search", Tags: map[string]string{"cap_network": "true"}},
		{Name: "grep", Tags: map[string]string{"cap_file": "true"}},
		{Name: "git", Tags: map[string]string{"cap_git": "true"}},
	}}
	c := &plannerCognition{binder: binder}
	got := c.advertisedToolSchemas(toolCapabilitiesForTask(derived))
	require.Len(t, got, 2, "the task's capabilities select two of three tools")
	names := []string{got[0].Name, got[1].Name}
	assert.ElementsMatch(t, []string{"grep", "web.search"}, names)
}

// TestAdvertisedToolSchemas_UntaggedToolsSurvive is the defect the MCP bridge exposed: a tool
// that declares NO capability cannot be judged by a capability filter, so hiding it removes a
// tool the filter was never able to consider. Before this, any task carrying capabilities
// dropped every untagged tool as long as one tagged tool matched.
func TestAdvertisedToolSchemas_UntaggedToolsSurvive(t *testing.T) {
	binder := &tagBinder{schemas: []resources.ToolSchema{
		{Name: "web.search", Tags: map[string]string{"cap_network": "true"}},
		{Name: "mcp-weather", Tags: nil}, // declares no capability at all
		{Name: "sql.exec", Tags: map[string]string{"cap_data": "true"}},
	}}
	c := &plannerCognition{binder: binder}

	got := c.advertisedToolSchemas([]string{"network"})
	names := make([]string, 0, len(got))
	for _, s := range got {
		names = append(names, s.Name)
	}
	assert.ElementsMatch(t, []string{"web.search", "mcp-weather"}, names,
		"a tool that declares no capability must survive a capability filter it cannot participate in")
	assert.NotContains(t, names, "sql.exec", "a tool that declares a different capability is still excluded")
}

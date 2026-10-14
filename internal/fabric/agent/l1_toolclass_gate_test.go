package agentfabric

// l1_toolclass_gate_test.go pins the convention a caller must follow to BLOCK a
// tool through the L1 capability graph, which is what the adversarial corpus's
// gap case depends on ("every tool disabled at L1, so the planner's tool calls are
// all skipped and the forced answer has no body").
//
// The convention is not obvious and getting it subtly wrong is silent: a DAG node
// keyed by the bare tool name never matches the lookup (the planner keys by
// ToolClass = name + "#" + argument shape) and the gate stays permissive, so the
// run would quietly produce a normal answer where the corpus expects a gap. That
// is exactly the class of mistake a test has to prevent, not a comment.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// grepToolClassID is the identifier the planner looks up for the fixture's
// "grep" tool: derived, never hand-written, so the test cannot drift from the
// production lookup.
func grepToolClassID(t *testing.T, binder *plannerTestBinder) string {
	t.Helper()
	for _, schema := range binder.GetToolSchemas() {
		if schema.Name == "grep" {
			return core.ToolClassID(schema.Name, core.ToolArgShape(schema))
		}
	}
	t.Fatal("fixture tool grep not found in the binder")
	return ""
}

// TestL1ToolClassGate_BlocksOnlyWhenTheNodeSaysDisabled locks both directions of
// the gate: disabled at the DERIVED ToolClass id blocks, everything else stays
// permissive (no graph, unknown tool, or a node without the key).
func TestL1ToolClassGate_BlocksOnlyWhenTheNodeSaysDisabled(t *testing.T) {
	binder := &plannerTestBinder{}
	id := grepToolClassID(t, binder)

	dag, err := engine.NewMutableDAG([]*engine.Step{{ID: id}})
	require.NoError(t, err)
	require.NoError(t, dag.SetNodeMetadata(id, map[string]string{l1MetaEnabled: "false"}))

	c := &plannerCognition{binder: binder, l1: dag}
	assert.False(t, c.isToolEnabled("grep"),
		"a ToolClass node marked enabled=false must block the tool")
	assert.True(t, c.isToolEnabled("not-a-tool"),
		"an unknown tool has no ToolClass to constrain and stays permissive")

	// The silent-failure direction: a DAG keyed by the BARE tool name must NOT
	// block anything, because the production lookup is by ToolClass. Asserting
	// this keeps the corpus honest — if someone later "simplifies" the gate to a
	// name lookup, the gap case would start passing for the wrong reason.
	bare, err := engine.NewMutableDAG([]*engine.Step{{ID: "grep"}})
	require.NoError(t, err)
	require.NoError(t, bare.SetNodeMetadata("grep", map[string]string{l1MetaEnabled: "false"}))
	assert.True(t, (&plannerCognition{binder: binder, l1: bare}).isToolEnabled("grep"),
		"a node keyed by the bare name does not block: the lookup is by ToolClass")

	// No graph at all stays permissive (the SDK's current, unchanged behaviour).
	assert.True(t, (&plannerCognition{binder: binder}).isToolEnabled("grep"))
}

// TestAgentToolAllowlist_BlocksWithoutASchema pins the OTHER gate that can stop a
// tool, and the reason it matters here: unlike the L1 ToolClass lookup it needs no
// SCHEMA — l1ToolClassID returns "" for a tool the binder does not advertise, and
// an id-less lookup is permissive, so the L1 graph cannot block a tool the runtime
// never advertised. The per-agent allowlist is name-based and therefore works
// exactly there, which is what lets an end-to-end case be engineered without first
// registering a tool with a schema.
func TestAgentToolAllowlist_BlocksWithoutASchema(t *testing.T) {
	// nil = inherit every tool (the documented default).
	permissive := &Agent{}
	assert.True(t, permissive.IsToolAllowed("anything"),
		"no allowlist means every tool is allowed")

	// non-nil but empty = admit NOTHING. The map being empty is the point: it is
	// distinct from nil, and that distinction is the whole mechanism.
	blocked := &Agent{toolAllowlist: map[string]bool{}}
	assert.False(t, blocked.IsToolAllowed("grep"))
	assert.False(t, blocked.IsToolAllowed("anything"))

	// A populated allowlist admits exactly its members.
	one := &Agent{toolAllowlist: map[string]bool{"grep": true}}
	assert.True(t, one.IsToolAllowed("grep"))
	assert.False(t, one.IsToolAllowed("sed"))
}

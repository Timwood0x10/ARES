package agentfabric

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/llmcore"
)

// TestRationaleToolSchema_MatchesWhatTheConsumerReads pins the one coupling that would fail
// SILENTLY: the advertised tool name and argument key must be the same strings the extractor
// looks for. A typo in either place means a model's stated rationale is dropped with no error
// anywhere — the feature would look installed and never fire, which is precisely the failure
// mode this whole change exists to end.
func TestRationaleToolSchema_MatchesWhatTheConsumerReads(t *testing.T) {
	schema := rationaleToolSchema()
	require.Equal(t, RecordRationaleToolName, schema.Function.Name)

	props, ok := schema.Function.Parameters["properties"].(map[string]interface{})
	require.True(t, ok, "the schema must carry a properties map")
	_, declared := props[rationaleArgKey]
	assert.True(t, declared, "the schema must declare the argument the extractor reads (%q)", rationaleArgKey)
	assert.Equal(t, []string{rationaleArgKey}, schema.Function.Parameters["required"])

	// The round trip: a call shaped by this schema IS read back by the consumer.
	call := llmcore.ToolCall{
		ID: "c1", Type: "function",
		Function: llmcore.FunctionCall{
			Name:      schema.Function.Name,
			Arguments: `{"` + rationaleArgKey + `":"because the first report was stale"}`,
		},
	}
	assert.Equal(t, "because the first report was stale",
		rationaleFrom(&llmcore.GenerateResponse{ToolCalls: []llmcore.ToolCall{call}}))
}

// rationaleCall builds a RecordRationaleToolName call with the given raw arguments.
func rationaleCall(arguments string) llmcore.ToolCall {
	return llmcore.ToolCall{
		ID:       "call-1",
		Type:     "function",
		Function: llmcore.FunctionCall{Name: RecordRationaleToolName, Arguments: arguments},
	}
}

// TestRationaleFrom_ReadsTheDedicatedToolCall is the P0-4A contract.
//
// It exists because of a measurement, not a hunch: on the deployment's provider every
// tool-call turn came back with an EMPTY Content (5/5), so content-only extraction stored
// nothing on exactly the turns a successor would want to understand. The rationale now also
// travels as the argument of a dedicated tool call — the one channel open to a model that is
// busy calling tools.
func TestRationaleFrom_ReadsTheDedicatedToolCall(t *testing.T) {
	resp := &llmcore.GenerateResponse{
		Content:   "", // measured: this is what a tool-call turn actually looks like
		ToolCalls: []llmcore.ToolCall{rationaleCall(`{"rationale":"  comparing both reports before writing  "}`)},
	}
	assert.Equal(t, "comparing both reports before writing", rationaleFrom(resp),
		"the argument must be the fallback when content is empty, trimmed")
}

// TestRationaleFrom_PrefersContentWhenTheModelWroteBoth: content is the model speaking
// directly; the tool call is the fallback for when it could not.
func TestRationaleFrom_PrefersContentWhenTheModelWroteBoth(t *testing.T) {
	resp := &llmcore.GenerateResponse{
		Content:   "content wins",
		ToolCalls: []llmcore.ToolCall{rationaleCall(`{"rationale":"tool loses"}`)},
	}
	assert.Equal(t, "content wins", rationaleFrom(resp))
}

// TestRationaleFrom_TakesTheFirstStatementAndIgnoresOtherCalls pins the two edges an
// implementation gets wrong: a quantum may state its reason once and then call several real
// tools, and the plan node carries ONE rationale — so the first statement is the one that
// preceded the work, and unrelated calls contribute nothing.
func TestRationaleFrom_TakesTheFirstStatementAndIgnoresOtherCalls(t *testing.T) {
	first := rationaleCall(`{"rationale":"first"}`)
	second := rationaleCall(`{"rationale":"restated after results"}`)
	other := llmcore.ToolCall{ID: "c2", Type: "function",
		Function: llmcore.FunctionCall{Name: "calculator", Arguments: `{"expression":"2+2"}`}}

	assert.Equal(t, "first", rationaleFrom(&llmcore.GenerateResponse{ToolCalls: []llmcore.ToolCall{first, second, other}}))
	assert.Empty(t, rationaleFrom(&llmcore.GenerateResponse{ToolCalls: []llmcore.ToolCall{other}}),
		"a turn that only did work states no rationale — absence stays absence")
}

// TestRationaleFrom_MalformedArgumentIsAbsentNotFatal: the argument is model output. A
// quantum whose rationale does not parse did not state one; failing the step instead would
// let a cosmetic call take down a session.
func TestRationaleFrom_MalformedArgumentIsAbsentNotFatal(t *testing.T) {
	for name, args := range map[string]string{
		"not json":     "just some prose",
		"wrong type":   `{"rationale":42}`,
		"missing key":  `{"reason":"nope"}`,
		"empty string": `{"rationale":"   "}`,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, rationaleFrom(&llmcore.GenerateResponse{ToolCalls: []llmcore.ToolCall{rationaleCall(args)}}))
		})
	}
}

// TestRationaleFrom_BoundsTheToolArgument: the cap applies to BOTH sources — an oversized
// tool argument must not become the path that puts an unbounded trace into every later
// prompt (which is the whole point of rationaleMaxRunes).
func TestRationaleFrom_BoundsTheToolArgument(t *testing.T) {
	long := strings.Repeat("x", rationaleMaxRunes*3)
	got := rationaleFrom(&llmcore.GenerateResponse{
		ToolCalls: []llmcore.ToolCall{rationaleCall(`{"rationale":"` + long + `"}`)},
	})
	assert.NotEmpty(t, got)
	assert.LessOrEqual(t, len([]rune(got)), rationaleMaxRunes+1, // +1 for the ellipsis rune
		"the write-side cap must hold for the tool-call source too")
}

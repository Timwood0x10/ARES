package agentruntime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// enricherFixture mirrors submitFixture but forwards SubmitterOptions, so
// the enrichment contract is locked against the same assembly the
// Execution builds.
func enricherFixture(opts ...SubmitterOption) (*Submitter, *taskfabric.Fabric, *agentfabric.SessionRegistry) {
	fabric := taskfabric.NewFabric()
	reg := agentfabric.NewSessionRegistry()
	sessions := &Sessions{Reg: reg, Fabric: fabric, Compile: planprojection.NewCompileCoordinator(fabric, nil)}
	return NewSubmitter(sessions, opts...), fabric, reg
}

// taskInput decodes a fabric task's checkpoint and returns its "input"
// payload entry — the field both prompt reads consume downstream.
func taskInput(t *testing.T, fabric *taskfabric.Fabric, taskID string) any {
	t.Helper()
	tk, err := fabric.Task(taskID)
	require.NoError(t, err)
	dc, err := taskfabric.DecodeCheckpoint(tk.Checkpoint)
	require.NoError(t, err)
	return dc.Payload["input"]
}

// TestSubmitterEnricherRewritesPromptBeforeAdmission locks the enrichment
// contract: the enricher runs after session-ID resolution (it keys
// per-session state on the resolved ID) and before admission, and the
// enriched text reaches BOTH prompt reads downstream — the submitted task
// payload (planner fallback) and the compiled session root (planner
// primary) — so the LLM plans on one consistent prompt.
func TestSubmitterEnricherRewritesPromptBeforeAdmission(t *testing.T) {
	var seenSession, seenPrompt string
	sub, fabric, reg := enricherFixture(WithPromptEnricher(func(_ context.Context, sessionID, prompt string) string {
		seenSession, seenPrompt = sessionID, prompt
		return "[history]\n" + prompt
	}))

	taskID, sessionID, err := sub.Submit(context.Background(), PlanCapability,
		map[string]any{"input": "hello"})
	require.NoError(t, err)
	require.Equal(t, "hello", seenPrompt, "enricher must receive the original prompt")
	require.Equal(t, sessionID, seenSession, "enricher must receive the resolved session ID")

	require.Equal(t, "[history]\nhello", taskInput(t, fabric, taskID),
		"submitted task payload must carry the enriched prompt (planner fallback read)")

	sess, err := reg.GetSession(sessionID)
	require.NoError(t, err)
	require.Equal(t, "[history]\nhello", taskInput(t, fabric, sess.Root()),
		"admitted session root must carry the enriched prompt (planner primary read)")
}

// TestSubmitterEnricherSeesCallerSessionID: a payload-supplied session ID
// is the key the enricher receives — follow-up turns on one chat thread
// must map to one memory session instead of a fresh auto-admitted one.
func TestSubmitterEnricherSeesCallerSessionID(t *testing.T) {
	var seenSession string
	sub, _, _ := enricherFixture(WithPromptEnricher(func(_ context.Context, sessionID, prompt string) string {
		seenSession = sessionID
		return prompt
	}))

	_, _, err := sub.Submit(context.Background(), PlanCapability,
		map[string]any{"input": "hi", "session_id": "chat-42"})
	require.NoError(t, err)
	require.Equal(t, "chat-42", seenSession)
}

// TestSubmitterEnricherPassThroughContracts locks the fail-open edges: an
// empty enrichment result, a nil option, and no option at all all leave
// the prompt exactly as submitted — enrichment can add context, never
// reject or empty a user submission.
func TestSubmitterEnricherPassThroughContracts(t *testing.T) {
	tests := []struct {
		name string
		opts []SubmitterOption
	}{
		{name: "empty enrichment result keeps prompt",
			opts: []SubmitterOption{WithPromptEnricher(func(context.Context, string, string) string { return "" })}},
		{name: "nil enricher option is a no-op",
			opts: []SubmitterOption{WithPromptEnricher(nil)}},
		{name: "no option passes through", opts: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sub, fabric, reg := enricherFixture(tc.opts...)

			taskID, sessionID, err := sub.Submit(context.Background(), PlanCapability,
				map[string]any{"input": "plain"})
			require.NoError(t, err)

			require.Equal(t, "plain", taskInput(t, fabric, taskID))

			sess, err := reg.GetSession(sessionID)
			require.NoError(t, err)
			require.Equal(t, "plain", taskInput(t, fabric, sess.Root()))
		})
	}
}

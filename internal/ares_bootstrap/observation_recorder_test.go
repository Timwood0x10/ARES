package ares_bootstrap

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	ares_memory "github.com/Timwood0x10/ares/internal/runtime/memory"
)

// newRecorderForTest builds the recorder against a real in-memory manager, so
// the whole chat pipeline (store → retention → cleaner → prompt) is exercised
// rather than a mock's idea of it.
func newRecorderForTest(t *testing.T) (*ObservationRecorder, ares_memory.MemoryManager, string) {
	t.Helper()
	cfg := ares_memory.DefaultMemoryConfig()
	cfg.MaxSessions = 10
	cfg.SessionTTL = time.Minute
	cfg.MaxTasks = 10
	cfg.TaskTTL = time.Minute
	mem, err := ares_memory.NewMemoryManager(cfg)
	require.NoError(t, err)

	sessionID, err := mem.CreateSession(context.Background(), "user-1")
	require.NoError(t, err)

	return NewObservationRecorder(mem), mem, sessionID
}

// conversation returns the stored messages without the session's opening
// system greeting, which CreateSession itself writes.
func conversation(t *testing.T, mem ares_memory.MemoryManager, sessionID string) []ares_memory.Message {
	t.Helper()
	msgs, err := mem.GetMessages(context.Background(), sessionID)
	require.NoError(t, err)
	out := make([]ares_memory.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == ares_memory.RoleSystem {
			continue
		}
		out = append(out, m)
	}
	return out
}

// completedToolEvent builds the terminal event the fabric emits for a finished
// tool task, with the output carried by checkpoint_json exactly as production
// does (restoreKeyCheckpointJSON).
func completedToolEvent(t *testing.T, taskID, sessionID, tool, intent, output string, args map[string]any) *ares_events.Event {
	t.Helper()

	cp := taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
		SessionID: sessionID,
		StepCheckpoint: map[string]any{
			"result": "ok",
			"items": []any{map[string]any{
				"item_id": taskID,
				"content": output,
			}},
		},
	})
	raw, err := taskfabric.MarshalCheckpoint(cp)
	require.NoError(t, err)

	payload := map[string]any{
		eventKeyTaskID:     taskID,
		eventKeyCapability: "tool/" + tool,
		eventKeySessionID:  sessionID,
		eventKeyCheckpoint: string(raw),
		eventKeyDeps:       []string{"plan-0"},
	}
	if intent != "" {
		payload[eventKeyIntent] = intent
	}
	for k, v := range args {
		payload[eventKeyArgsPrefix+k] = v
	}
	return &ares_events.Event{
		ID:         "ev-" + taskID,
		Type:       ares_events.EventTaskCompleted,
		ModuleName: "taskfabric",
		Payload:    payload,
	}
}

// TestObservationRecorder_WritesTheCausalPair is the wiring test for the piece
// that was missing in production: a completed tool task must become
// "why → did → saw" in the session, not vanish.
func TestObservationRecorder_WritesTheCausalPair(t *testing.T) {
	ctx := context.Background()
	rec, mem, sessionID := newRecorderForTest(t)

	original := strings.Repeat("matched line in parser.go\n", 20)
	ev := completedToolEvent(t, "task-grep-1", sessionID, "grep",
		"grep the parser first because the lexer is only reachable through it",
		original, map[string]any{"query": "tokenizer"})

	rec.consumeOne(ctx, ev)

	msgs := conversation(t, mem, sessionID)
	require.Len(t, msgs, 2, "one decision, one action, one observation")

	call := msgs[0]
	assert.Equal(t, ares_memory.RoleAssistant, call.Role)
	assert.Contains(t, call.Content, "the lexer is only reachable through it",
		"the WHY must ride the action it produced")
	require.Len(t, call.ToolCalls, 1)
	assert.Equal(t, "grep", call.ToolCalls[0].Function.Name)
	assert.Contains(t, call.ToolCalls[0].Function.Arguments, "tokenizer",
		"the action must identify itself")
	assert.Equal(t, "task-grep-1", call.TurnID, "the pair shares a turn so turn-aware cleaning keeps it together")
	assert.Equal(t, "plan-0", call.ParentID, "the causal edge is recorded")

	observation := msgs[1]
	assert.Equal(t, ares_memory.RoleToolResult, observation.Role)
	assert.Equal(t, original, observation.Content)
	assert.Equal(t, "task-grep-1", observation.ToolCallID, "the observation answers that call")
	require.NotEmpty(t, observation.ArtifactRefs, "the store must retain the bulk and stamp a ref")

	// And the ref resolves: the record is replayable, not just identifiable.
	got, err := mem.GetArtifact(ctx, observation.ArtifactRefs[0])
	require.NoError(t, err)
	assert.Equal(t, original, got)
}

// TestObservationRecorder_PromptCarriesTheRef closes the loop through the
// prompt: the marker the model reads names a ref that still resolves. This also
// pins the rendering bound fix (the flat builder used to re-cut every line at
// 100 runes, which truncated the ref off the end of the marker).
func TestObservationRecorder_PromptCarriesTheRef(t *testing.T) {
	ctx := context.Background()
	rec, mem, sessionID := newRecorderForTest(t)

	original := strings.Repeat("warning: deprecated call site\n", 40)
	rec.consumeOne(ctx, completedToolEvent(t, "task-run-1", sessionID, "code_runner",
		"run the suite to see which test fails", original, map[string]any{"command": "go test ./..."}))

	prompt, err := mem.BuildContext(ctx, "continue", sessionID)
	require.NoError(t, err)

	ref := ""
	if i := strings.Index(prompt, "ref:artifact://"); i >= 0 {
		ref = prompt[i+len("ref:"):]
		if j := strings.IndexAny(ref, " \n]"); j >= 0 {
			ref = ref[:j]
		}
	}
	require.NotEmpty(t, ref, "the prompt must carry the ref, got: %s", prompt)

	got, err := mem.GetArtifact(ctx, ref)
	require.NoError(t, err, "the ref printed into the prompt must dereference")
	assert.Equal(t, original, got)

	assert.Contains(t, prompt, "run the suite to see which test fails",
		"the why survives compression too")
	assert.Less(t, len(prompt), len(original), "and the bulk does not")
}

// TestObservationRecorder_DedupesTaskIDs: the store may redeliver, and a retried
// task emits more than one terminal event. One task must produce one pair.
func TestObservationRecorder_DedupesTaskIDs(t *testing.T) {
	ctx := context.Background()
	rec, mem, sessionID := newRecorderForTest(t)

	ev := completedToolEvent(t, "task-dup", sessionID, "grep", "because", strings.Repeat("x", 300), nil)
	rec.consumeOne(ctx, ev)
	rec.consumeOne(ctx, ev)
	rec.consumeOne(ctx, ev)

	assert.Len(t, conversation(t, mem, sessionID), 2, "redelivery must not duplicate the chain")
}

// TestObservationRecorder_SkipsNonObservations pins the filters: only a
// completed TOOL task with a session scope is an observation. Recording the
// others would put plan/answer text (or another session's output) into a
// conversation record where it does not belong.
func TestObservationRecorder_SkipsNonObservations(t *testing.T) {
	ctx := context.Background()
	rec, mem, sessionID := newRecorderForTest(t)

	// Not a tool capability.
	ev := completedToolEvent(t, "task-answer", sessionID, "grep", "x", "payload", nil)
	ev.Payload[eventKeyCapability] = "ares/answer"
	rec.consumeOne(ctx, ev)

	// No session scope.
	ev = completedToolEvent(t, "task-nosess", sessionID, "grep", "x", "payload", nil)
	ev.Payload[eventKeySessionID] = ""
	rec.consumeOne(ctx, ev)

	// Fused capability: not a single action.
	ev = completedToolEvent(t, "task-fused", sessionID, "grep", "x", "payload", nil)
	ev.Payload[eventKeyCapability] = "tool/grep+read"
	rec.consumeOne(ctx, ev)

	// Undecodable envelope: a missing fact, not an empty one.
	ev = completedToolEvent(t, "task-broken", sessionID, "grep", "x", "payload", nil)
	ev.Payload[eventKeyCheckpoint] = "{not json"
	rec.consumeOne(ctx, ev)

	assert.Empty(t, conversation(t, mem, sessionID), "nothing above is an observation")
}

// TestObservationRecorder_RecordsAnEmptyObservation: an envelope that decodes
// but carries no item content IS an observation ("the tool returned nothing").
// Skipping it would leave a decision with an action and no outcome.
func TestObservationRecorder_RecordsAnEmptyObservation(t *testing.T) {
	ctx := context.Background()
	rec, mem, sessionID := newRecorderForTest(t)

	cp := taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
		SessionID:      sessionID,
		StepCheckpoint: map[string]any{"result": "ok"},
	})
	raw, err := taskfabric.MarshalCheckpoint(cp)
	require.NoError(t, err)

	rec.consumeOne(ctx, &ares_events.Event{
		Type: ares_events.EventTaskCompleted,
		Payload: map[string]any{
			eventKeyTaskID:     "task-empty",
			eventKeyCapability: "tool/grep",
			eventKeySessionID:  sessionID,
			eventKeyCheckpoint: string(raw),
		},
	})

	msgs := conversation(t, mem, sessionID)
	require.Len(t, msgs, 2)
	assert.Empty(t, msgs[1].Content, "the empty outcome is recorded as empty")
	assert.Equal(t, "task-empty", msgs[1].ToolCallID)
}

// TestToolArgsFromPayload: args travel as arg.-namespaced payload keys, never as
// "" (an unparseable tool call would break replay).
func TestToolArgsFromPayload(t *testing.T) {
	got := toolArgsFromPayload(map[string]any{
		"arg.query": "x",
		"arg.path":  "/a.go",
		"input":     "envelope plumbing, not an arg",
		"intent":    "not an arg either",
	})
	assert.JSONEq(t, `{"query":"x","path":"/a.go"}`, string(got))
	assert.Equal(t, "{}", string(toolArgsFromPayload(map[string]any{"input": "x"})))
}

// TestObservationRecorder_LateDeliveryStillSortsChronologically is the ordering
// guard: the recorder writes from the event stream, so its message can arrive
// AFTER the answer that consumed it. The prompt must still read
// user → tool call → observation → answer, because Message.Time is when the
// thing happened and slice order is only when the write landed.
func TestObservationRecorder_LateDeliveryStillSortsChronologically(t *testing.T) {
	ctx := context.Background()
	rec, mem, sessionID := newRecorderForTest(t)

	base := time.Now().Add(-2 * time.Minute)
	require.NoError(t, mem.AddStructuredMessage(ctx, sessionID, ares_memory.Message{
		Role: ares_memory.RoleUser, Content: "find the failing test", Time: base,
	}))
	// The answer is written first (the session finished), the observation
	// arrives late from the event stream with an earlier timestamp.
	require.NoError(t, mem.AddStructuredMessage(ctx, sessionID, ares_memory.Message{
		Role: ares_memory.RoleAssistant, Content: "the tokenizer test fails", Time: base.Add(2 * time.Minute),
	}))

	ev := completedToolEvent(t, "task-late", sessionID, "code_runner",
		"run the suite to see the failure", strings.Repeat("FAIL tokenizer\n", 20),
		map[string]any{"command": "go test ./..."})
	ev.Timestamp = base.Add(time.Minute)
	rec.consumeOne(ctx, ev)

	prompt, err := mem.BuildContext(ctx, "next", sessionID)
	require.NoError(t, err)

	idxUser := strings.Index(prompt, "find the failing test")
	idxCall := strings.Index(prompt, "run the suite to see the failure")
	idxResult := strings.Index(prompt, "Tool result:")
	idxAnswer := strings.Index(prompt, "the tokenizer test fails")
	require.True(t, idxUser >= 0 && idxCall >= 0 && idxResult >= 0 && idxAnswer >= 0,
		"all four beats must render, got:\n%s", prompt)

	assert.Less(t, idxUser, idxCall, "the question precedes the decision")
	assert.Less(t, idxCall, idxResult, "the decision precedes the observation")
	assert.Less(t, idxResult, idxAnswer, "the observation precedes the answer that used it")
}

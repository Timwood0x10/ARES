// Package ares_bootstrap — chat-path OBSERVATION producer.
//
// This closes the last gap in the causal-chain work: the session memory store
// can retain a tool observation and stamp a replayable ref, and the cleaner can
// carry that ref into the prompt — but until now NOTHING in production wrote a
// tool observation into a session, so the capability existed and never fired.
// The chat path stored the user turn and the final answer, and every
// intermediate observation (the bulk of the run, and the only record of what the
// agent actually saw) was dropped on the floor.
//
// The producer is a task.completed subscriber, deliberately NOT a change inside
// the fabric/kernel/agentruntime. The L2 core is memory-agnostic by decision
// (the kernel must not know what a conversation is), and the codebase already
// consumes terminal events for exactly this kind of projection three times over
// (distillation, skill outcomes, GA fitness) — so observing the event stream
// keeps that boundary intact while delivering session id, task id and output
// together.
//
// What it writes is a CAUSAL PAIR, not a bare result:
//
//	assistant  content = the round's stated intent (payload "intent", stamped by
//	                    the planner), tool_calls = [the call with its args]
//	tool       content = the observation (the tool's output envelope content),
//	                    tool_call_id = the task id
//
// The pair is what makes the record readable as "why → did → saw" instead of a
// list of effects, and it is why the assistant message is written at all: an
// orphan observation carries no question. Both messages share TurnID = task id
// so the turn-aware cleaner keeps them together as one completed turn.
//
// Two hazards of an event-driven producer, handled structurally:
//   - redelivery/retry would double-record, so task ids are deduped in a bounded
//     ring (the event store may replay, and a retried task legitimately emits
//     more than one terminal event);
//   - a lost record is never a lost task: the write is best-effort and does not
//     block the consumer, the same policy as the skill outcome writer.
package ares_bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/causalchain"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	ares_memory "github.com/Timwood0x10/ares/internal/runtime/memory"
	memctx "github.com/Timwood0x10/ares/internal/runtime/memory/context"
)

// Event-payload keys this recorder reads. They are the cross-package string
// contract of the fabric's terminal event:
//
//	task_id, capability, session_id  → recordWithExtrasLocked (fabric_events.go)
//	checkpoint_json                  → restoreKeyCheckpointJSON (restore.go)
//	intent                           → the planner's intentKey (agentfabric)
//	arg.<name>                       → argMetadataPrefix (agentfabric/l2graph.go)
//
// They are spelled here rather than imported because each is owned by the
// package that WRITES it; a copy on the consumer side is a statement of what the
// producer promised, and it fails loudly (empty observation) if that changes.
const (
	eventKeyTaskID     = "task_id"
	eventKeyCapability = "capability"
	eventKeySessionID  = "session_id"
	eventKeyCheckpoint = "checkpoint_json"
	eventKeyIntent     = "intent"
	eventKeyArgsPrefix = "arg."
	eventKeyDeps       = "deps"
)

const (
	// toolCapabilityPrefix is the capability shape the scheduler routes to a
	// tool node. Anything else (ares/plan, ares/answer, agent capabilities) is
	// not an observation and must not enter the conversation record.
	toolCapabilityPrefix = "tool/"

	// observedIntentMaxRunes bounds the replayed intent. It matches the
	// planner's replay budget (agentfabric intentMaxRunes) so the same
	// reasoning costs the same in both paths, instead of the chat record quietly
	// growing a second, larger bound.
	observedIntentMaxRunes = 320

	// observationDedupWindow is how many recent task ids are remembered. A run
	// grows at most a few dozen tool nodes, so this covers a long session while
	// keeping the ring trivially small.
	observationDedupWindow = 1024
)

// ObservationRecorder projects completed tool tasks into their session's
// conversation record.
//
// It is EXPORTED because the sink is not always the bootstrap memory manager:
// an embedding runtime (the SDK) builds its own manager for the sessions it
// reads, while bootstrap's own manager may exist alongside it. Each entry point
// attaches the recorder to the manager that actually owns its sessions — the
// recorder itself is stateless apart from the dedupe ring, so attaching it twice
// is harmless (a store that does not own the session simply records nothing).
type ObservationRecorder struct {
	mem    ares_memory.MemoryManager
	logger *slog.Logger

	mu   sync.Mutex
	seen map[string]struct{}
	ring []string
}

// NewObservationRecorder builds a recorder writing into mem.
func NewObservationRecorder(mem ares_memory.MemoryManager) *ObservationRecorder {
	return &ObservationRecorder{
		mem:    mem,
		logger: slog.Default().With("component", "observation_recorder"),
		seen:   make(map[string]struct{}, observationDedupWindow),
	}
}

// Consume handles one event. Callers that own their own subscription loop (the
// SDK's errgroup-driven backend) call this directly; the bootstrap path uses
// subscribeObservationRecorder below.
func (rec *ObservationRecorder) Consume(ctx context.Context, ev *ares_events.Event) {
	rec.consumeOneRecovered(ctx, ev)
}

// subscribeObservationRecorder wires the recorder to the terminal event stream
// until ctx ends. It is a no-op when memory or the event store is unavailable —
// offline wiring, not an error (the same degrade contract as the other
// subscribers).
func subscribeObservationRecorder(ctx context.Context, comp *Components) {
	if comp.Memory == nil || comp.EventStore == nil {
		return
	}
	rec := NewObservationRecorder(comp.Memory)
	comp.GoBackground(ctx, "observation-recorder", func(ctx context.Context) error {
		ch, err := comp.EventStore.Subscribe(ctx, ares_events.EventFilter{
			Types: []ares_events.EventType{ares_events.EventTaskCompleted},
		})
		if err != nil {
			rec.logger.Warn("bootstrap: observation recorder subscribe failed", "error", err)
			return nil
		}
		rec.logger.Info("bootstrap: observation recorder started (tool task.completed → session memory)")
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return nil
				}
				if ev == nil {
					continue
				}
				rec.Consume(ctx, ev)
			case <-ctx.Done():
				return nil
			}
		}
	})
}

// consumeOneRecovered keeps one malformed event from killing the loop: the
// consumer is process-wide, so a panic here would silently end observation
// recording for every session.
func (rec *ObservationRecorder) consumeOneRecovered(ctx context.Context, ev *ares_events.Event) {
	defer func() {
		if r := recover(); r != nil {
			rec.logger.Error("observation recorder: event panicked; skipped",
				"event_id", ev.ID, "panic", r)
		}
	}()
	rec.consumeOne(ctx, ev)
}

// consumeOne records one completed tool task as a causal pair.
func (rec *ObservationRecorder) consumeOne(ctx context.Context, ev *ares_events.Event) {
	capability, _ := ev.Payload[eventKeyCapability].(string)
	if !strings.HasPrefix(capability, toolCapabilityPrefix) {
		return
	}
	tool := strings.TrimPrefix(capability, toolCapabilityPrefix)
	// A fused capability ("tool/a+b") is not a single call; replaying it as one
	// would invent an action that never happened.
	if tool == "" || strings.Contains(tool, "+") {
		return
	}

	sessionID, _ := ev.Payload[eventKeySessionID].(string)
	if sessionID == "" {
		// No session scope: there is nothing to append to. Recording it
		// elsewhere would put one caller's tool output into another's history.
		return
	}
	taskID, _ := ev.Payload[eventKeyTaskID].(string)
	if taskID == "" {
		return
	}
	if !rec.markSeen(taskID) {
		return
	}

	output, ok := toolOutputFromPayload(ev.Payload)
	if !ok {
		// The envelope could not be decoded: skip rather than record a
		// truncated or invented observation. A missing observation costs one
		// history entry; a wrong one corrupts the chain it is supposed to carry.
		rec.logger.Debug("observation recorder: checkpoint undecodable; skipped", "task_id", taskID)
		return
	}

	intent, _ := ev.Payload[eventKeyIntent].(string)
	argsJSON := toolArgsFromPayload(ev.Payload)
	parent := firstDependency(ev.Payload)
	// Stamp the COMPLETION time, not the write time: an event delivered late
	// must still sort into the moment the observation happened, or a
	// conversation read right after the answer would show the tool result after
	// the answer that already used it.
	at := ev.Timestamp
	if at.IsZero() {
		at = time.Now()
	}

	// Write the call first, so a reader (and the turn-aware cleaner) sees the
	// question before the answer even if the second write fails.
	call := ares_memory.Message{
		Role:    ares_memory.RoleAssistant,
		Content: string(causalchain.NormalizeIntent(intent, observedIntentMaxRunes)),
		ToolCalls: []ares_memory.ToolCall{{
			ID:       taskID,
			Type:     "function",
			Function: ares_memory.ToolCallFunction{Name: tool, Arguments: string(argsJSON)},
		}},
		Time:      at,
		TurnID:    taskID,
		EventKind: "assistant.tool_call",
		ParentID:  parent,
	}
	if err := rec.mem.AddStructuredMessage(ctx, sessionID, call); err != nil {
		rec.logRecordFailure(sessionID, taskID, err)
		return
	}

	observation := ares_memory.Message{
		Role:       ares_memory.RoleToolResult,
		Content:    output,
		ToolCallID: taskID,
		Time:       at,
		TurnID:     taskID,
		EventKind:  "task.completed",
		ParentID:   parent,
	}
	if err := rec.mem.AddStructuredMessage(ctx, sessionID, observation); err != nil {
		rec.logRecordFailure(sessionID, taskID, err)
		return
	}
	// The store retains the verbatim payload and stamps the ref at write time
	// (see memory/context/artifact.go); nothing here has to ask for it.
	rec.logger.Debug("observation recorder: recorded",
		"session_id", sessionID, "task_id", taskID, "tool", tool, "runes", len([]rune(output)))
}

// logRecordFailure separates "this store does not own that session" from a real
// fault. The former is expected whenever the recorder is attached to a manager
// that is not the conversation's owner — an embedding runtime builds its own
// manager, and both subscriptions see the same event stream.
func (rec *ObservationRecorder) logRecordFailure(sessionID, taskID string, err error) {
	if errors.Is(err, memctx.ErrSessionNotFound) {
		rec.logger.Debug("observation recorder: session not in this store",
			"session_id", sessionID, "task_id", taskID)
		return
	}
	rec.logger.Warn("observation recorder: record failed",
		"session_id", sessionID, "task_id", taskID, "error", err)
}

// markSeen records taskID and reports whether it is new. Bounded ring: the
// oldest id is forgotten first, so a replayed very old event may be recorded
// twice — duplicates cost prompt tokens, never correctness.
func (rec *ObservationRecorder) markSeen(taskID string) bool {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if _, ok := rec.seen[taskID]; ok {
		return false
	}
	if len(rec.ring) >= observationDedupWindow {
		delete(rec.seen, rec.ring[0])
		rec.ring = rec.ring[1:]
	}
	rec.seen[taskID] = struct{}{}
	rec.ring = append(rec.ring, taskID)
	return true
}

// toolOutputFromPayload extracts the observation text from the terminal event.
//
// It returns (text, true) when the envelope decoded — including an EMPTY text,
// which is a real observation ("the tool returned nothing") — and ("", false)
// when the envelope is absent or undecodable, which is a missing fact and must
// not be recorded as an empty one.
func toolOutputFromPayload(payload map[string]any) (string, bool) {
	raw, _ := payload[eventKeyCheckpoint].(string)
	if raw == "" {
		return "", false
	}
	var env taskfabric.CheckpointEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return "", false
	}
	dc, err := taskfabric.DecodeCheckpoint(&env)
	if err != nil {
		return "", false
	}
	step, ok := dc.StepCheckpoint.(map[string]any)
	if !ok {
		return "", false
	}
	items, _ := step["items"].([]any)
	for _, it := range items {
		item, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if content, ok := item["content"].(string); ok && strings.TrimSpace(content) != "" {
			return content, true
		}
	}
	return "", true
}

// toolArgsFromPayload rebuilds the call's arguments from the arg.-namespaced
// payload keys. An argument-less call yields "{}" — never "" — so the recorded
// tool call stays parseable.
func toolArgsFromPayload(payload map[string]any) []byte {
	args := make(map[string]any)
	for k, v := range payload {
		if name, ok := strings.CutPrefix(k, eventKeyArgsPrefix); ok && name != "" {
			args[name] = v
		}
	}
	if len(args) == 0 {
		return []byte("{}")
	}
	b, err := json.Marshal(args)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// firstDependency returns the node this task depended on, for the causal edge. A
// fan-in task has several; the first is recorded — the same summary the
// planner's Unit.Parent carries — while message order keeps the real sequence.
func firstDependency(payload map[string]any) string {
	switch deps := payload[eventKeyDeps].(type) {
	case []string:
		if len(deps) > 0 {
			return deps[0]
		}
	case []any:
		for _, d := range deps {
			if s, ok := d.(string); ok && s != "" {
				return s
			}
		}
	case string:
		return deps
	}
	return ""
}

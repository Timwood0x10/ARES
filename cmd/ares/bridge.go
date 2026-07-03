package main

//nolint: errcheck // fmt.Fprintf to errStdout: best-effort

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

// bridgeEvents subscribes to an EventStore and re-emits every event
// into a PluginBus (EventBus). Events are enriched with missing fields
// that real agents don't emit.
func bridgeEvents(ctx context.Context, store ares_events.EventStore, bus ares_runtime.EventBus, meta map[string]agentMeta) {
	ch, err := store.Subscribe(ctx, ares_events.EventFilter{})
	if err != nil {
		return
	}

	agentIDs := make(map[string]bool, len(meta))
	for id := range meta {
		agentIDs[id] = true
	}

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}

			payload := enrichEvent(evt, agentIDs, meta)
			bus.Emit(ctx, evt.StreamID, evt.Type, evt.ModuleName, payload)
		}
	}
}

// enrichEvent enriches event payload with missing fields.
func enrichEvent(evt *ares_events.Event, agentIDs map[string]bool, meta map[string]agentMeta) map[string]any {
	payload := evt.Payload
	if payload == nil {
		payload = make(map[string]any)
	}

	switch evt.Type {
	case ares_events.EventAgentStarted:
		if m, ok := meta[evt.StreamID]; ok {
			enriched := clonePayload(payload)
			enriched["name"] = m.name
			enriched["role"] = m.role
			enriched["model_name"] = m.model
			enriched["parent_id"] = m.parentID
			return enriched
		}

	case ares_events.EventTaskCreated, ares_events.EventTaskCompleted, ares_events.EventTaskFailed:
		enriched := clonePayload(payload)

		if _, hasAgentID := enriched["agent_id"]; !hasAgentID {
			if agentIDs[evt.StreamID] {
				enriched["agent_id"] = evt.StreamID
			}
		}

		if _, hasTaskID := enriched["task_id"]; !hasTaskID {
			if agentIDs[evt.StreamID] {
				enriched["task_id"] = fmt.Sprintf("task-%s-%d", evt.StreamID, evt.Timestamp.UnixNano())
			}
		}

		return enriched
	}

	return payload
}

func clonePayload(original map[string]any) map[string]any {
	cloned := make(map[string]any, len(original)+4)
	for k, v := range original {
		cloned[k] = v
	}
	return cloned
}

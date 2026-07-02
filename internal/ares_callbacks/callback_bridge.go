package ares_callbacks

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// BridgeEventStore implements the Emitter interface, converting callback
// ares_events into persisted EventStore ares_events. This unifies the callback and
// event sourcing systems so that instrumentation consumers only need to
// watch one stream.
type BridgeEventStore struct {
	store   ares_events.EventStore
	agentID string
}

// NewBridge creates a BridgeEventStore that writes callback ares_events
// to the given EventStore. The agentID is used as the event stream ID.
func NewBridge(store ares_events.EventStore, agentID string) *BridgeEventStore {
	return &BridgeEventStore{store: store, agentID: agentID}
}

// Emit converts a callback Context to an ares_events.Event and appends it.
func (b *BridgeEventStore) Emit(ctx *Context) {
	if ctx == nil || b.store == nil {
		return
	}
	eventType := b.mapEventType(ctx.Event)
	if eventType == "" {
		return
	}
	payload := map[string]any{
		"agent_id":  ctx.AgentID,
		"tool_name": ctx.ToolName,
		"model":     ctx.Model,
		"duration":  ctx.Duration.String(),
	}
	if ctx.Error != nil {
		payload["error"] = ctx.Error.Error()
	}
	if ctx.Input != "" {
		payload["input"] = ctx.Input
	}
	if ctx.Output != "" {
		payload["output"] = ctx.Output
	}
	if ctx.TokenCount > 0 {
		payload["token_count"] = ctx.TokenCount
	}
	for k, v := range ctx.Extra {
		payload[k] = v
	}

	if !ares_events.Emit(context.Background(), b.store, b.agentID, eventType, "callbacks", payload) {
		log.Warn("failed to emit event", "event_type", eventType, "stream_id", b.agentID)
	}
}

// mapEventType translates callback Event constants to ares_events.EventType.
func (b *BridgeEventStore) mapEventType(ce Event) ares_events.EventType {
	switch ce {
	case EventLLMStart:
		return ares_events.EventLLMCall
	case EventLLMEnd:
		return ares_events.EventLLMCall
	case EventLLMError:
		return ares_events.EventLLMCall
	case EventToolStart:
		return ares_events.EventToolCallStarted
	case EventToolEnd:
		return ares_events.EventToolCallCompleted
	case EventToolError:
		return ares_events.EventToolCallCompleted
	case EventAgentStart:
		return ares_events.EventAgentStarted
	case EventAgentEnd:
		return ares_events.EventAgentStopped
	case EventAgentError:
		return ares_events.EventAgentStopped
	default:
		log.Debug("callback bridge: unmapped event", "event", ce)
		return ""
	}
}

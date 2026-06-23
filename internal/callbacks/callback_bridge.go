package callbacks

import (
	"context"
	"log/slog"

	"github.com/Timwood0x10/ares/internal/events"
)

// BridgeEventStore implements the Emitter interface, converting callback
// events into persisted EventStore events. This unifies the callback and
// event sourcing systems so that instrumentation consumers only need to
// watch one stream.
type BridgeEventStore struct {
	store   events.EventStore
	agentID string
}

// NewBridge creates a BridgeEventStore that writes callback events
// to the given EventStore. The agentID is used as the event stream ID.
func NewBridge(store events.EventStore, agentID string) *BridgeEventStore {
	return &BridgeEventStore{store: store, agentID: agentID}
}

// Emit converts a callback Context to an events.Event and appends it.
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

	events.Emit(context.Background(), b.store, b.agentID, eventType, payload)
}

// mapEventType translates callback Event constants to events.EventType.
func (b *BridgeEventStore) mapEventType(ce Event) events.EventType {
	switch ce {
	case EventLLMStart:
		return events.EventLLMCall
	case EventLLMEnd:
		return events.EventLLMCall
	case EventLLMError:
		return events.EventLLMCall
	case EventToolStart:
		return events.EventToolCallStarted
	case EventToolEnd:
		return events.EventToolCallCompleted
	case EventToolError:
		return events.EventToolCallCompleted
	case EventAgentStart:
		return events.EventAgentStarted
	case EventAgentEnd:
		return events.EventAgentStopped
	case EventAgentError:
		return events.EventAgentStopped
	default:
		slog.Debug("callback bridge: unmapped event", "event", ce)
		return ""
	}
}

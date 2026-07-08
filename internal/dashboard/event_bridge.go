package dashboard

import (
	"context"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"

	"golang.org/x/sync/errgroup"
)

// EventBridge subscribes to the EventStore and forwards ares_events to the WebSocket hub
// and the intelligence engine.
type EventBridge struct {
	eventStore ares_events.EventStore
	hub        *WSHub
	intel      *Engine
	cancel     context.CancelFunc
	eg         errgroup.Group
}

// NewEventBridge creates a new EventBridge.
func NewEventBridge(eventStore ares_events.EventStore, hub *WSHub, intel *Engine) *EventBridge {
	return &EventBridge{
		eventStore: eventStore,
		hub:        hub,
		intel:      intel,
	}
}

// Start begins forwarding ares_events to the WebSocket hub.
func (b *EventBridge) Start(ctx context.Context) error {
	ctx, b.cancel = context.WithCancel(ctx)

	ch, err := b.eventStore.Subscribe(ctx, ares_events.EventFilter{})
	if err != nil {
		b.cancel()
		return err
	}

	b.eg.Go(func() error {
		return b.forwardLoop(ctx, ch)
	})

	return nil
}

// Stop stops the event bridge.
func (b *EventBridge) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
	if err := b.eg.Wait(); err != nil {
		log.Error("dashboard: event bridge forward loop error", "error", err)
	}
}

// forwardLoop reads ares_events and broadcasts them to appropriate channels.
func (b *EventBridge) forwardLoop(ctx context.Context, ch <-chan *ares_events.Event) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			b.handleEvent(evt)
		}
	}
}

// handleEvent routes an event to the WebSocket channels and intelligence engine.
func (b *EventBridge) handleEvent(evt *ares_events.Event) {
	view := EventView{
		ID:        evt.ID,
		StreamID:  evt.StreamID,
		Type:      string(evt.Type),
		Payload:   evt.Payload,
		Version:   evt.Version,
		Timestamp: evt.Timestamp,
	}

	b.hub.BroadcastToChannel(WSChannelEvents, &WSMessage{
		Type: WSTypeEvent,
		Data: view,
	})

	// Feed intelligence engine.
	agentID := evt.StreamID
	latency := extractLatency(evt)
	hasError := evt.Type == "error" || evt.Type == "task.failed" || evt.Type == "tool.error" || evt.Type == "llm.error"
	isRestart := evt.Type == "agent.restarted" || evt.Type == "failover.completed"

	if b.intel != nil {
		switch {
		case isRestart:
			b.intel.ObserveAgentEvent(agentID, "restart", 0, false)
		case hasError:
			b.intel.ObserveAgentEvent(agentID, "error", 0, true)
		case latency > 0:
			b.intel.ObserveAgentEvent(agentID, "latency", latency, false)
		default:
			b.intel.ObserveAgentEvent(agentID, "tick", 0, false)
		}
	}

	switch evt.Type {
	case ares_events.EventAgentStarted, ares_events.EventAgentStopped,
		ares_events.EventFailoverTriggered, ares_events.EventFailoverCompleted:
		b.hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
			Type: WSTypeAgentUpdate,
			Data: view,
		})

	case ares_events.EventTaskCreated, ares_events.EventTaskCompleted, ares_events.EventTaskFailed:
		if execID := extractExecutionID(evt); execID != "" {
			channel := WSChannelPrefixWorkflow + execID
			b.hub.BroadcastToChannel(channel, &WSMessage{
				Type: WSTypeStepUpdate,
				Data: view,
			})
		}
	}
}

// extractExecutionID attempts to extract a workflow execution ID from event payload.
func extractExecutionID(evt *ares_events.Event) string {
	if evt.Payload == nil {
		return ""
	}
	for _, key := range []string{"execution_id", "workflow_id", "task_id"} {
		if id, ok := evt.Payload[key].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

// extractLatency attempts to extract a latency value in milliseconds from an
// event payload. The "duration_ms" key is already in milliseconds. The
// "duration" key stores nanoseconds (as float64, int64, or time.Duration) and
// is converted to milliseconds to avoid unit confusion that would cause
// 5ms to appear as 5,000,000ms.
func extractLatency(evt *ares_events.Event) float64 {
	if evt.Payload == nil {
		return 0
	}
	// duration_ms is already in milliseconds.
	if d, ok := evt.Payload["duration_ms"].(float64); ok {
		return d
	}
	// duration is stored as nanoseconds; convert to ms.
	if d, ok := evt.Payload["duration"].(float64); ok {
		return d / 1e6
	}
	if d, ok := evt.Payload["duration"].(time.Duration); ok {
		return float64(d.Milliseconds())
	}
	if d, ok := evt.Payload["duration"].(int64); ok {
		return float64(d) / 1e6
	}
	return 0
}

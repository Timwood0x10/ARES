package dashboard

import (
	"context"
	"sync"

	"goagentx/internal/events"
)

// EventBridge subscribes to the EventStore and forwards events to the WebSocket hub.
type EventBridge struct {
	eventStore events.EventStore
	hub        *WSHub
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewEventBridge creates a new EventBridge.
func NewEventBridge(eventStore events.EventStore, hub *WSHub) *EventBridge {
	return &EventBridge{
		eventStore: eventStore,
		hub:        hub,
	}
}

// Start begins forwarding events to the WebSocket hub.
func (b *EventBridge) Start(ctx context.Context) error {
	ctx, b.cancel = context.WithCancel(ctx)

	ch, err := b.eventStore.Subscribe(ctx, events.EventFilter{})
	if err != nil {
		return err
	}

	b.wg.Add(1)
	go b.forwardLoop(ctx, ch)

	return nil
}

// Stop stops the event bridge.
func (b *EventBridge) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
	b.wg.Wait()
}

// forwardLoop reads events and broadcasts them to appropriate channels.
func (b *EventBridge) forwardLoop(ctx context.Context, ch <-chan *events.Event) {
	defer b.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			b.handleEvent(evt)
		}
	}
}

// handleEvent routes an event to the appropriate WebSocket channels.
func (b *EventBridge) handleEvent(evt *events.Event) {
	view := EventView{
		ID:        evt.ID,
		StreamID:  evt.StreamID,
		Type:      string(evt.Type),
		Payload:   evt.Payload,
		Version:   evt.Version,
		Timestamp: evt.Timestamp,
	}

	// Always broadcast to the events channel.
	b.hub.BroadcastToChannel(WSChannelEvents, &WSMessage{
		Type: WSTypeEvent,
		Data: view,
	})

	// Route to specific channels based on event type.
	switch {
	case evt.Type == events.EventAgentStarted || evt.Type == events.EventAgentStopped:
		b.hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
			Type: WSTypeAgentUpdate,
			Data: view,
		})

	case evt.Type == events.EventFailoverTriggered || evt.Type == events.EventFailoverCompleted:
		b.hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
			Type: WSTypeAgentUpdate,
			Data: view,
		})

	case evt.Type == events.EventTaskCreated || evt.Type == events.EventTaskCompleted || evt.Type == events.EventTaskFailed:
		// Route to workflow channel if we can extract an execution ID.
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
func extractExecutionID(evt *events.Event) string {
	if evt.Payload == nil {
		return ""
	}
	// Try common payload keys.
	for _, key := range []string{"execution_id", "workflow_id", "task_id"} {
		if id, ok := evt.Payload[key].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

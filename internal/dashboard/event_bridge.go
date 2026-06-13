package dashboard

import (
	"context"
	"log/slog"

	"goagentx/internal/events"

	"golang.org/x/sync/errgroup"
)

// EventBridge subscribes to the EventStore and forwards events to the WebSocket hub.
type EventBridge struct {
	eventStore events.EventStore
	hub        *WSHub
	cancel     context.CancelFunc
	eg         errgroup.Group
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
		slog.Error("dashboard: event bridge forward loop error", "error", err)
	}
}

// forwardLoop reads events and broadcasts them to appropriate channels.
func (b *EventBridge) forwardLoop(ctx context.Context, ch <-chan *events.Event) error {
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
	switch evt.Type {
	case events.EventAgentStarted, events.EventAgentStopped,
		events.EventFailoverTriggered, events.EventFailoverCompleted:
		b.hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
			Type: WSTypeAgentUpdate,
			Data: view,
		})

	case events.EventTaskCreated, events.EventTaskCompleted, events.EventTaskFailed:
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

package dashboard

import (
	"testing"
	"time"
)

func TestWSHubBroadcastToChannel(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	// Give hub goroutine time to start.
	time.Sleep(10 * time.Millisecond)

	msg := &WSMessage{
		Type: WSTypeEvent,
		Data: map[string]string{"test": "value"},
	}

	// Should not panic even with no subscribers.
	hub.BroadcastToChannel("test-channel", msg)
}

func TestWSHubBroadcastAll(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	msg := &WSMessage{
		Type: WSTypeEvent,
		Data: "test",
	}

	// Should not panic with no clients.
	hub.BroadcastAll(msg)
}

func TestWSHubClientCount(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	if hub.ClientCount() != 0 {
		t.Errorf("ClientCount = %d, want 0", hub.ClientCount())
	}
}

func TestWSHubChannelCount(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	defer hub.Stop()

	time.Sleep(10 * time.Millisecond)

	if hub.ChannelCount() != 0 {
		t.Errorf("ChannelCount = %d, want 0", hub.ChannelCount())
	}
}

func TestWSMessageTypes(t *testing.T) {
	// Verify message type constants are defined.
	types := []string{
		WSTypeSubscribe,
		WSTypeUnsubscribe,
		WSTypeEvent,
		WSTypeAgentUpdate,
		WSTypeStepUpdate,
		WSTypeDAGChange,
		WSTypeHeartbeat,
		WSTypeMCPChange,
		WSTypePing,
		WSTypePong,
	}

	for _, typ := range types {
		if typ == "" {
			t.Error("message type should not be empty")
		}
	}
}

func TestWSChannels(t *testing.T) {
	// Verify channel constants are defined.
	channels := []string{
		WSChannelEvents,
		WSChannelAgents,
		WSChannelMCP,
	}

	for _, ch := range channels {
		if ch == "" {
			t.Error("channel should not be empty")
		}
	}

	if WSChannelPrefixWorkflow != "workflow:" {
		t.Errorf("WSChannelPrefixWorkflow = %s, want workflow:", WSChannelPrefixWorkflow)
	}
	if WSChannelPrefixDAG != "dag:" {
		t.Errorf("WSChannelPrefixDAG = %s, want dag:", WSChannelPrefixDAG)
	}
}

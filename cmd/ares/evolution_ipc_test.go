package main

import (
	"context"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agents/peer"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
)

// fakeMessageAgent implements the SendMessage surface (interface assertion
// used by wireEvolutionIPC) with a recording delivery function.
type fakeMessageAgent struct {
	id  string
	mu  sync.Mutex
	got []*ahp.AHPMessage
}

func (a *fakeMessageAgent) ID() string { return a.id }

func (a *fakeMessageAgent) SendMessage(_ context.Context, msg *ahp.AHPMessage) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, msg)
	return nil
}

func (a *fakeMessageAgent) messages() []*ahp.AHPMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*ahp.AHPMessage, len(a.got))
	copy(out, a.got)
	return out
}

// stubIPCProtocolSource returns a fixed IPC policy.
type stubIPCProtocolSource struct {
	policy aresrecovery.IPCProtocolPolicy
}

func (s *stubIPCProtocolSource) ActiveIPCProtocolPolicy(context.Context) (aresrecovery.IPCProtocolPolicy, error) {
	return s.policy, nil
}

// buildBridge mirrors wireEvolutionIPC's registration logic on a fresh bus so
// the test can exercise the full json+gzip round trip without constructing
// the large leader.Agent / sub.Agent interfaces. It creates the bus AND the
// policy-aware IPC on the SAME bus, so the peer send reaches the registered
// handler.
func buildBridge(target *fakeMessageAgent, policy aresrecovery.IPCProtocolPolicy) *peer.Registry {
	bus := agentipc.NewBus()
	ipc := aresrecovery.NewEvolutionAwareIPC(bus, &stubIPCProtocolSource{policy: policy})
	_ = bus.Register(target.ID(), func(ctx context.Context, msg *agentipc.Message) (*agentipc.Message, error) {
		payload, err := aresrecovery.Decode(msg.Payload)
		if err != nil {
			return nil, err
		}
		ahpMsg, err := toAHPMessage(payload)
		if err != nil {
			return nil, err
		}
		return nil, target.SendMessage(ctx, ahpMsg)
	})
	reg := peer.NewRegistry()
	_ = reg.Register(target.ID(), func(ctx context.Context, m *ahp.AHPMessage) error {
		return ipc.Send(ctx, m.AgentID, target.ID(), peerTopic, m)
	})
	return reg
}

// TestEvolutionIPCBridgeRoundTrip verifies the full round trip under
// json+gzip: the peer send is compressed on the wire and the target agent
// receives the original message content (proving Decode in the bus handler).
func TestEvolutionIPCBridgeRoundTrip(t *testing.T) {
	ctx := context.Background()
	target := &fakeMessageAgent{id: "sub-1"}

	reg := buildBridge(target, aresrecovery.IPCProtocolPolicy{
		Encoding: aresrecovery.WireJSONGzip, MinCompressSize: 1,
	})

	msg := &ahp.AHPMessage{
		MessageID: "m-roundtrip",
		AgentID:   "leader",
		Method:    ahp.AHPMethodACK,
		Payload:   map[string]any{"compressed": true, "n": 42},
	}
	if err := reg.Send(ctx, "sub-1", msg); err != nil {
		t.Fatalf("peer send: %v", err)
	}
	got := target.messages()
	if len(got) != 1 {
		t.Fatalf("want 1 delivered message, got %d", len(got))
	}
	if got[0].MessageID != "m-roundtrip" || got[0].Payload["compressed"] != true || got[0].Payload["n"] != float64(42) {
		t.Fatalf("round-trip content mismatch: %+v", got[0])
	}
}

// TestEvolutionIPCBridgePlainJSON verifies the default plain-json policy
// delivers the original message unchanged (backward compatible with the
// direct peer channel).
func TestEvolutionIPCBridgePlainJSON(t *testing.T) {
	ctx := context.Background()
	target := &fakeMessageAgent{id: "sub-1"}

	reg := buildBridge(target, aresrecovery.IPCProtocolPolicy{Encoding: aresrecovery.WireJSON})

	msg := &ahp.AHPMessage{
		MessageID: "m-plain",
		AgentID:   "leader",
		Method:    ahp.AHPMethodTask,
		Payload:   map[string]any{"hello": "world"},
	}
	if err := reg.Send(ctx, "sub-1", msg); err != nil {
		t.Fatalf("peer send: %v", err)
	}
	got := target.messages()
	if len(got) != 1 {
		t.Fatalf("want 1 delivered message, got %d", len(got))
	}
	if got[0].MessageID != "m-plain" || got[0].Payload["hello"] != "world" {
		t.Fatalf("unexpected delivered message %+v", got[0])
	}
}

// TestToAHPMessage verifies both decode paths: the original pointer passes
// through, and a json+gzip round-trip map is re-hydrated.
func TestToAHPMessage(t *testing.T) {
	original := &ahp.AHPMessage{MessageID: "m1", AgentID: "a", Method: ahp.AHPMethodACK}

	// Path 1: original pointer unchanged.
	got, err := toAHPMessage(original)
	if err != nil {
		t.Fatalf("toAHPMessage(original): %v", err)
	}
	if got != original {
		t.Fatal("original pointer must pass through unchanged")
	}

	// Path 2: a decoded json+gzip payload is a map; re-hydrate it.
	mapPayload := map[string]any{
		"message_id": "m1",
		"agent_id":   "a",
		"method":     "ACK",
		"payload":    map[string]any{"k": "v"},
	}
	got2, err := toAHPMessage(mapPayload)
	if err != nil {
		t.Fatalf("toAHPMessage(map): %v", err)
	}
	if got2.MessageID != "m1" || got2.AgentID != "a" || got2.Method != ahp.AHPMethodACK {
		t.Fatalf("re-hydrated message mismatch: %+v", got2)
	}
	if got2.Payload["k"] != "v" {
		t.Fatalf("payload lost in re-hydration: %+v", got2.Payload)
	}
}

// TestToAHPMessageRejectsGarbage verifies a non-AHPMessage payload surfaces
// as an error instead of silently producing an empty message.
func TestToAHPMessageRejectsGarbage(t *testing.T) {
	if _, err := toAHPMessage(42); err == nil {
		t.Fatal("non-message payload must error")
	}
}

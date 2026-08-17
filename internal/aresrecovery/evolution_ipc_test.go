package aresrecovery

import (
	"context"
	"errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/agentipc"
)

// stubIPCProtocolSource returns a fixed IPC policy for tests.
type stubIPCProtocolSource struct {
	policy IPCProtocolPolicy
	err    error
}

func (s *stubIPCProtocolSource) ActiveIPCProtocolPolicy(context.Context) (IPCProtocolPolicy, error) {
	return s.policy, s.err
}

// TestEvolutionIPCPlainJSONPassThrough verifies the default policy sends the
// raw payload unchanged (backward compatible).
func TestEvolutionIPCPlainJSONPassThrough(t *testing.T) {
	bus := agentipc.NewBus()
	var got any
	if err := bus.Register("b", func(_ context.Context, msg *agentipc.Message) (*agentipc.Message, error) {
		got = msg.Payload
		return &agentipc.Message{From: msg.To, To: msg.From}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	ipc := NewEvolutionAwareIPC(bus, &stubIPCProtocolSource{policy: IPCProtocolPolicy{Encoding: WireJSON}})
	if err := ipc.Send(context.Background(), "a", "b", "t", "plain"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got != "plain" {
		t.Fatalf("plain payload must pass through unchanged, got %#v", got)
	}
}

// TestEvolutionIPCCompressesLargePayload verifies json+gzip policy compresses
// large payloads into a WireMessage and the receiver can Decode it back.
func TestEvolutionIPCCompressesLargePayload(t *testing.T) {
	bus := agentipc.NewBus()
	var received *agentipc.Message
	if err := bus.Register("b", func(_ context.Context, msg *agentipc.Message) (*agentipc.Message, error) {
		received = msg
		return &agentipc.Message{From: msg.To, To: msg.From}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	ipc := NewEvolutionAwareIPC(bus, &stubIPCProtocolSource{
		policy: IPCProtocolPolicy{Encoding: WireJSONGzip, MinCompressSize: 64},
	})

	big := map[string]any{"data": make([]string, 500)}
	for i := range make([]struct{}, 500) {
		big["data"].([]string)[i] = "payload-item-with-some-length-" + string(rune('a'+i%26))
	}
	if err := ipc.Send(context.Background(), "a", "b", "t", big); err != nil {
		t.Fatalf("send: %v", err)
	}
	wire, ok := received.Payload.(*WireMessage)
	if !ok {
		t.Fatalf("payload must be a WireMessage under json+gzip, got %T", received.Payload)
	}
	if !wire.Compressed {
		t.Fatal("large payload must be compressed")
	}
	decoded, err := Decode(wire)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	decodedMap, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("decoded must be a map, got %T", decoded)
	}
	if arr, ok := decodedMap["data"].([]any); !ok || len(arr) != 500 {
		t.Fatalf("decoded data must round-trip 500 items, got %T len=%d", decodedMap["data"], len(decodedMap["data"].([]any)))
	}
}

// TestEvolutionIPCBelowThresholdNotCompressed verifies the MinCompressSize
// threshold: small payloads are still wrapped but not compressed.
func TestEvolutionIPCBelowThresholdNotCompressed(t *testing.T) {
	bus := agentipc.NewBus()
	var received *agentipc.Message
	if err := bus.Register("b", func(_ context.Context, msg *agentipc.Message) (*agentipc.Message, error) {
		received = msg
		return &agentipc.Message{From: msg.To, To: msg.From}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	ipc := NewEvolutionAwareIPC(bus, &stubIPCProtocolSource{
		policy: IPCProtocolPolicy{Encoding: WireJSONGzip, MinCompressSize: 1024},
	})
	if err := ipc.Send(context.Background(), "a", "b", "t", "small"); err != nil {
		t.Fatalf("send: %v", err)
	}
	wire, ok := received.Payload.(*WireMessage)
	if !ok {
		t.Fatalf("payload must be a WireMessage, got %T", received.Payload)
	}
	if wire.Compressed {
		t.Fatal("small payload must not be compressed below the threshold")
	}
	decoded, err := Decode(wire)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded != "small" {
		t.Fatalf("decoded = %#v, want small", decoded)
	}
}

// TestEvolutionIPCPlainPayloadDecodePassthrough verifies Decode returns a
// non-WireMessage payload unchanged (plain sends keep working).
func TestEvolutionIPCPlainPayloadDecodePassthrough(t *testing.T) {
	got, err := Decode("raw-value")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != "raw-value" {
		t.Fatalf("decode must pass through non-wire payloads, got %#v", got)
	}
}

// TestEvolutionIPCPolicyErrorPropagates verifies a policy-source failure
// surfaces instead of sending with a default encoding.
func TestEvolutionIPCPolicyErrorPropagates(t *testing.T) {
	bus := agentipc.NewBus()
	ipc := NewEvolutionAwareIPC(bus, &stubIPCProtocolSource{err: errors.New("policy store down")})
	if err := ipc.Send(context.Background(), "a", "b", "t", "x"); err == nil {
		t.Fatal("policy error must propagate")
	}
}

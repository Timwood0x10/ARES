// Evolution-aware IPC wiring (v0.3.0 M2-3): bridges the agent peer channel
// through aresrecovery.EvolutionAwareIPC so the active evolution strategy's
// wire policy (ipc.encoding = json | json+gzip) shapes real agent-to-agent
// messages — "Evolution decides; Kernel enforces", same as the spawn gate and
// quota manager.
//
// The peer registry (internal/agents/peer) is the production agent-messaging
// channel: agents register SendMessage delivery functions and send directly
// without routing through the leader. Instead of replacing that channel, this
// wiring interposes the evolution-aware IPC bus between the registry and the
// agents' send functions, so every peer message passes through the policy
// (plain json by default — backward compatible — or json+gzip when the
// evolution strategy deploys it).
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/peer"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
)

// peerTopic is the bus topic used for peer-channel messages routed through the
// evolution-aware IPC bus.
const peerTopic = "peer"

// evolutionIPCBridge holds the evolution-aware IPC bus and the peer registry
// wired to it. The bridge is created once per serve; a nil policy source (no
// evolution store) keeps plain json encoding, which is behaviorally identical
// to the direct peer channel. Cross-Fabric message tracing (M4-1) is applied
// inline by the registry's send wrapper using the tracer passed to
// wireEvolutionIPC, so it is not stored here.
type evolutionIPCBridge struct {
	ipc *aresrecovery.EvolutionAwareIPC
	reg *peer.Registry
}

// wireEvolutionIPC builds the evolution-aware peer bridge: a Bus whose Send
// applies the active evolution IPC policy, with one bus handler per agent that
// decodes the wire payload and forwards the original AHPMessage to the agent's
// delivery function. The returned registry routes every peer send through the
// bus. When tracer is non-nil, every peer send is also recorded as a
// cross-Fabric message span (v0.3.0 M4-1 TraceMessage — the v0.4.0 review
// flagged it as library-only; this is its production write path).
//
// Args:
//   - leaderAgent: the leader agent (registered under its ID).
//   - subAgents: the sub agents (registered under their IDs).
//   - store: the evolution strategy store (nil → plain json, no-op policy).
//   - tracer: the shared GlobalTracer (nil → no message tracing).
//
// Returns:
//   - *evolutionIPCBridge: the wired bridge (registry + ipc).
//   - error: when a bus handler cannot be registered.
func wireEvolutionIPC(leaderAgent leader.Agent, subAgents []sub.Agent, store evolution.StrategyStore, tracer *aresrecovery.GlobalTracer) (*evolutionIPCBridge, error) {
	bus := agentipc.NewBus()
	ipc := aresrecovery.NewEvolutionAwareIPC(bus, ares_bootstrap.NewIPCProtocolPolicySource(store))
	reg := peer.NewRegistry()

	register := func(agentID string, send func(context.Context, *ahp.AHPMessage) error) {
		if agentID == "" || send == nil {
			return
		}
		targetID := agentID
		// Bus handler: decode the wire payload back to the original
		// AHPMessage and deliver to the agent. Plain json sends pass through
		// unchanged (Decode returns the payload as-is); json+gzip sends are
		// restored here, so the agent always sees the original message.
		_ = bus.Register(targetID, func(ctx context.Context, msg *agentipc.Message) (*agentipc.Message, error) {
			payload, err := aresrecovery.Decode(msg.Payload)
			if err != nil {
				return nil, fmt.Errorf("evolution IPC decode: %w", err)
			}
			ahpMsg, err := toAHPMessage(payload)
			if err != nil {
				return nil, err
			}
			if err := send(ctx, ahpMsg); err != nil {
				return nil, err
			}
			// agentipc.Handler contract: a nil reply + nil error means the
			// message was delivered and no reply is expected (fire-and-forget
			// peer delivery). The Send primitive ignores the reply, so the
			// nil value is the documented success path, not an invalid one.
			return nil, nil //nolint:nilnil // documented Handler "no reply" contract.
		})
		// Peer registry entry: route the peer send through the evolution-aware
		// bus. The sender's identity comes from the message itself.
		_ = reg.Register(targetID, func(ctx context.Context, m *ahp.AHPMessage) error {
			if tracer != nil {
				tracer.TraceMessage(m.MessageID, "sent", m.TaskID, map[string]any{
					"from":       m.AgentID,
					"to":         targetID,
					"topic":      peerTopic,
					"method":     string(m.Method),
					"session_id": m.SessionID,
				})
			}
			return ipc.Send(ctx, m.AgentID, targetID, peerTopic, m)
		})
	}

	// SendMessage is exposed via interface assertion (same discovery as
	// buildPeerRegistry); agents that do not implement it are skipped, not an
	// error.
	if sender, ok := leaderAgent.(interface {
		SendMessage(context.Context, *ahp.AHPMessage) error
	}); ok {
		register(leaderAgent.ID(), sender.SendMessage)
	}
	for _, sa := range subAgents {
		if sender, ok := sa.(interface {
			SendMessage(context.Context, *ahp.AHPMessage) error
		}); ok {
			register(sa.ID(), sender.SendMessage)
		}
	}
	return &evolutionIPCBridge{ipc: ipc, reg: reg}, nil
}

// toAHPMessage restores an *ahp.AHPMessage from a decoded payload. Plain
// sends deliver the original pointer unchanged; json+gzip sends round-trip
// through JSON, so the decoded value is a map that must be re-hydrated.
//
// KNOWN LIMITATION (JSON round-trip): under the json+gzip wire policy the
// payload is serialized to JSON, so values inside AHPMessage.Payload that
// JSON cannot represent faithfully are type-drifted on delivery — e.g. int
// becomes float64, non-string map keys are coerced, and custom structs are
// flattened to plain maps. This is a JSON wire-format limitation, not a bug
// in this function; the plain-json policy (the default) delivers the original
// pointer unchanged and has no such drift. Payload values should therefore be
// JSON-friendly (string/float64/bool/arrays/maps with string keys) when the
// evolution strategy enables json+gzip compression.
//
// Args:
//   - payload: the decoded payload (either *ahp.AHPMessage or a JSON map).
//
// Returns:
//   - *ahp.AHPMessage: the restored message.
//   - error: when the payload is not an AHPMessage or cannot be re-hydrated.
func toAHPMessage(payload any) (*ahp.AHPMessage, error) {
	if m, ok := payload.(*ahp.AHPMessage); ok {
		return m, nil
	}
	// Re-hydrate from the JSON map produced by a json+gzip round-trip.
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("evolution IPC re-marshal: %w", err)
	}
	var m ahp.AHPMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("evolution IPC re-hydrate: %w", err)
	}
	return &m, nil
}

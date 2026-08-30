# ares Architecture Deep Dive (XXVI): Agent Communication — How the Primitive Layer Lets Agents Talk (0.3.x)

> 0.3.x update: AHP (Agent Harmony Protocol) evolved into **Agent IPC** — a peer-mesh message bus. Six primitives: Send / Request / Reply / Delegate / Handoff / Subscribe. Replaces the 0.2.x five message types (Task/Result/Progress/ACK/Heartbeat). Agents are peer cognitive processes (A ≡ B ≡ C); communication doesn't need a Leader relay. The legacy AHP is retained as a compatibility layer. See article (II) in this series for details.

> Note: This article is grounded in the actual code (`internal/agents/peer`, `internal/agents/leader`, `internal/ares_protocol/ahp`, `internal/agents/actionlog`, `internal/agents/lease`) — the dedicated Agent-OS communication-primitive article in the docs series.

## 1. Why Agent Communication Matters

In a multi-agent system, the leader, sub-agents, and peers need to exchange messages. ARES's default execution path is event-driven single-path (`EventSubTaskScheduled` → execute → `EventSubTaskResult`). So why a separate direct-messaging primitive?

Because the event stream is the "scheduling view" while the communication primitives are the "collaboration view":

| Dimension | Event Stream (EventStore) | Communication (peer) |
|-----------|---------------------------|----------------------|
| View | Task dispatch, audit, replay | Ad-hoc collaboration, notifications |
| Persistence | Full event sourcing | In-memory registry + best-effort delivery |
| Timing | Async, replayable | Synchronous call, fire-and-forget |
| Role | Who should execute what | Who needs to know what |

**Core principle: peer delivery is a supplementary notification channel, NOT a task execution channel.** Task execution always goes through the event stream (auditable, recoverable); peer delivery only carries lightweight notifications (progress, collaboration hints) and never blocks the main flow on failure.

## 2. Peer Registry: Who Knows Whom

`internal/agents/peer/registry.go` is the address book:

```go
// SendFunc delivers a message to one agent. The implementation is
// responsible for enqueueing (blocking or async).
type SendFunc func(ctx context.Context, msg *ahp.AHPMessage) error

type Registry struct {
    peers map[string]SendFunc
    mu    sync.RWMutex
}
```

Core methods:

```go
func (r *Registry) Register(agentID string, send SendFunc) error   // register a delivery function
func (r *Registry) Unregister(agentID string)                      // deregister
func (r *Registry) Lookup(agentID string) (SendFunc, bool)         // find a delivery function
func (r *Registry) IDs() []string                                  // all online agents
func (r *Registry) Send(ctx context.Context, targetID string, msg *ahp.AHPMessage) error // deliver by ID
```

`Register` accepts a **delivery function**, not an Agent object — decoupling the registry from concrete agent implementations: anything that can `SendMessage` can be a peer via interface assertion (see §8 for the production wiring).

## 3. Message Format: The AHP Protocol

Messages use `AHPMessage` from `internal/ares_protocol/ahp`:

```go
type AHPMessage struct {
    ID      string            // unique message ID
    From    string            // sender agent ID
    To      string            // receiver agent ID
    Method  AHPMethod         // task / ack / heartbeat / progress ...
    Payload map[string]any    // business payload
}
```

Methods (`AHPMethod`):
- `AHPMethodTask`: task request (the sub's `messageHandler` does NOT execute tasks on it — tasks are event-stream driven, see §9)
- `AHPMethodACK`: acknowledgment (also a protocol-level placeholder; task results go through the event stream)
- `AHPMethodHeartbeat`: heartbeat
- `AHPMethodProgress`: progress notification (used by NotifyPeer)

## 4. NotifyPeer: The Leader's Supplementary Notification

`NotifyPeer` in `internal/agents/leader/agent_types.go` is the leader-side direct-send entry point:

```go
func (a *leaderAgent) NotifyPeer(ctx context.Context, targetID, message string) {
    // nil registry or empty target returns immediately (idempotent no-op)
    msg := ahp.NewMessage(ahp.AHPMethodProgress, a.id, targetID, "", "")
    msg.Payload = map[string]any{"note": message}
    if err := reg.Send(ctx, targetID, msg); err != nil {
        log.Debug("leader peer notify skipped", "target", targetID, "error", err)
    }
}
```

Two deliberate design choices:
1. **Failure logs at Debug only** — direct delivery is a "supplementary notification"; losing it does not affect task execution, so it never propagates
2. **Progress semantics** — NotifyPeer sends `AHPMethodProgress`; the receiver knows this is a "collaboration hint", not a "task directive", so it can never be confused with event-stream tasks

## 5. Message Queue: Buffering and Backpressure

`internal/ares_protocol/ahp/queue.go` provides a bounded message queue:

```go
type Queue struct {
    MaxSize  int              // default 1000 (sub task scenarios configure 500)
    messages chan *AHPMessage
}
```

- Bounded capacity → natural backpressure: when the queue is full the sender observes blocking/dropping, preventing unbounded memory growth
- Production wiring: `ahp.NewMessageQueue(leaderID, &ahp.QueueOptions{MaxSize: 500})` (one queue each for leader and sub)

## 6. Action Log: Auditable Task Records

`internal/agents/actionlog/actionlog.go` is the append-only audit log of "what was done" (`Store`):

```go
func (s *Store) Append(ctx context.Context, e Entry) error         // append (idempotent)
func (s *Store) List(sessionID string) []Entry                     // all records in a session
func (s *Store) Replay(sessionID, startID string) ([]Entry, error) // replay from a given entry
func (s *Store) Count() int                                        // record count
```

- Sub agents append an `actionlog.Entry` (task result + metadata) at all three task-result exits (success/failure/error) via `recordAction`
- `Append` is idempotent: re-appending the same Entry produces no duplicate (safe under retries / event replay)
- `Replay` supports replaying from a `startID` — the handle for audit and failure recovery

## 7. Session Lease: Concurrent Session Control

`internal/agents/lease` provides **session-level leases** (TTL leases): concurrent workers must acquire a lease before mutating the same session, preventing two workers from writing one session concurrently.

- Mount point: the memoryManager in `internal/ares_memory` (`leaseMgr` field)
- Semantics: leases expire (TTL), invalidating automatically on timeout; holders must complete operations within the lease or release explicitly
- Relationship to peer communication: lease is the mutual-exclusion control of "who may touch this session"; peer is the collaboration notification of "who needs to know this" — orthogonal

## 8. Production Wiring: buildPeerRegistry

`cmd/ares/serve.go` assembles the communication primitives after creating the leader and sub agents:

```go
// buildPeerRegistry registers the leader and sub agents' message senders into
// a peer.Registry. Agents that do not expose SendMessage (interface assertion)
// are skipped, not an error.
func buildPeerRegistry(leaderAgent leader.Agent, subAgents []sub.Agent) *peer.Registry {
    reg := peer.NewRegistry()
    if sender, ok := leaderAgent.(interface{ SendMessage(...) error }); ok {
        _ = reg.Register(leaderAgent.ID(), sender.SendMessage)
    }
    for _, sa := range subAgents {
        if sender, ok := sa.(interface{ SendMessage(...) error }); ok {
            _ = reg.Register(sa.ID(), sender.SendMessage)
        }
    }
    return reg
}
```

Then:

```go
leaderWithPeer := leaderAgent // after construction
leaderWithPeer.SetPeerRegistry(peerRegistry) // leader gains the peer registry
```

**Interface assertion, not type assertion** — a key design: the peer registry only requires "can send messages", never a concrete type, so new agent types need no registry-logic changes.

## 9. Communication vs. Execution: Where's the Boundary

The sub's `messageHandler` (`internal/agents/sub/handler.go`) best illustrates the boundary when handling direct AHP messages:

```go
case ahp.AHPMethodTask:
    return h.handleTaskMessage(ctx, msg)  // no-op: execution is the executor's job
case ahp.AHPMethodACK:
    return h.handleAckMessage(ctx, msg)   // no-op: protocol-level placeholder
case ahp.AHPMethodHeartbeat:
    return nil // heartbeat acknowledged
```

The empty `handleTaskMessage` / `handleAckMessage` implementations are **intentional** (the code comments say so): task execution is always driven by the event stream (`EventSubTaskScheduled`); direct AHP task/ack are protocol-layer channel placeholders — avoiding two parallel task-dispatch paths that would double-execute.

## 10. Summary

| Primitive | Package | Responsibility | Failure Semantics |
|-----------|---------|----------------|-------------------|
| Peer Registry | `internal/agents/peer` | Agent address book + direct delivery | Best-effort, log-only |
| NotifyPeer | `internal/agents/leader` | Leader supplementary notification (Progress) | Debug log, never propagates |
| Message Queue | `internal/ares_protocol/ahp` | Bounded buffering + backpressure | Queue full → block/drop |
| Action Log | `internal/agents/actionlog` | Task audit + replay (idempotent Append) | Record failure never affects tasks |
| Session Lease | `internal/agents/lease` | Concurrent session mutex (TTL lease) | Auto-expiry on timeout |

**Design line: communication primitives are the collaboration layer; the event stream is the execution layer.** The two are strictly separated — execution is auditable, recoverable, replayable; collaboration is lightweight, immediate, droppable. This is the "building-block" design of the Agent-OS primitive layer: every primitive is independently detachable and arbitrarily combinable without affecting the main execution loop.

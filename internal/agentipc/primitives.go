package agentipc

import (
	"context"
	"errors"
	"time"
)

// taskIDKey is the canonical payload key carrying a task identifier across
// collaboration/handoff messages. cmd/ares mirrors this value on the receive
// side (its own constant) — change both together.
const taskIDKey = "task_id"

// defaultRequestTimeout is used when Request is called with timeout <= 0
// (B16: prevents indefinite blocking on a missing timeout).
const defaultRequestTimeout = 30 * time.Second

// Send is the fire-and-forget primitive: deliver a message to a target agent
// without waiting for a reply. The target's handler is invoked synchronously
// in the caller's goroutine; a failed handler returns the error but does not
// block the sender (no reply channel is set up). Send does NOT pair with a
// reply — use Request for request/reply semantics.
//
// Args:
//   - ctx: passed to the handler.
//   - to: the target agent id.
//   - topic: the message subject.
//   - payload: the message body.
//
// Returns:
//   - error: ErrAgentNotRegistered / ErrNoHandler, or the handler error.
func (b *Bus) Send(ctx context.Context, from, to, topic string, payload any) error {
	b.mu.RLock()
	h, ok := b.handlers[to]
	b.mu.RUnlock()
	if !ok {
		// GAP-3: the target does not exist — the message is undeliverable.
		b.deadLetters.Record(from, to, topic, payload, ErrAgentNotRegistered.Error())
		return ErrAgentNotRegistered
	}
	msg := &Message{
		ID:      b.allocID(),
		From:    from,
		To:      to,
		Topic:   topic,
		Payload: payload,
		At:      b.allocNow(),
	}
	_, err := h(ctx, msg)
	if err != nil {
		// GAP-3: record undelivered/failed fire-and-forget sends.
		b.deadLetters.Record(from, to, topic, payload, err.Error())
	}
	return err
}

// Request is the synchronous request/reply primitive: send a message to a
// target agent and wait for a reply within the timeout. The bus allocates a
// correlation id and registers a pending reply channel; the target's handler
// must call Reply with the same correlation id to complete the request. A
// timeout or context cancellation removes the pending entry and returns
// ErrTimeout.
//
// Args:
//   - ctx: cancellation propagates to the wait.
//   - from: the sender agent id.
//   - to: the target agent id.
//   - topic: the request subject.
//   - payload: the request body.
//   - timeout: how long to wait for a reply.
//
// Returns:
//   - *Message: the reply (nil on timeout/error).
//   - error: ErrAgentNotRegistered / ErrNoHandler / ErrTimeout.
func (b *Bus) Request(ctx context.Context, from, to, topic string, payload any, timeout time.Duration) (*Message, error) {
	// B16: validate timeout — <=0 gets a sane default instead of blocking forever.
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	// B16: derive a child context so the handler goroutine is cancelled when
	// the timeout fires or the caller cancels — the handler no longer leaks.
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	b.mu.RLock()
	h, ok := b.handlers[to]
	b.mu.RUnlock()
	if !ok {
		return nil, ErrAgentNotRegistered
	}
	corrID := b.allocID() + "-corr"
	replyCh := make(chan *Message, 1)
	b.mu.Lock()
	b.pending[corrID] = replyCh
	b.mu.Unlock()
	defer b.removePending(corrID)

	req := &Message{
		ID:            b.allocID(),
		From:          from,
		To:            to,
		Topic:         topic,
		CorrelationID: corrID,
		Payload:       payload,
		At:            b.allocNow(),
	}
	// Invoke the handler in a managed goroutine so the reply can be delivered
	// asynchronously via Reply. If the handler returns a reply directly, it is
	// stamped and delivered through the same reply channel. If the handler
	// returns an error, a nil reply is delivered so the caller's select wakes
	// up and the error is surfaced.
	go func() {
		reply, err := h(reqCtx, req)
		if err != nil {
			// Surface the error: deliver a sentinel nil reply so the caller
			// wakes up; the actual error is stashed on the pending entry.
			b.stashError(corrID, err)
			_ = b.deliverReply(corrID, nil)
			return
		}
		if reply != nil {
			// Copy the handler-returned reply and stamp it — never mutate the
			// caller's message (the handler may return a shared template
			// across concurrent requests, so in-place stamping would race).
			stamped := *reply
			stamped.CorrelationID = corrID
			stamped.To = from
			stamped.From = to
			_ = b.deliverReply(corrID, &stamped)
		}
		// If the handler returned nil with no error, it intends to reply
		// asynchronously later via Reply. The select below waits for the
		// timeout in that case.
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	// GAP-3: single dead-letter exit — replyErr captures the failure reason
	// from whichever select arm fires, and exactly one Record happens below
	// (the two timeout arms race; without the flag a message could be
	// recorded twice).
	var replyErr error
	defer func() {
		if replyErr != nil {
			b.deadLetters.Record(from, to, topic, payload, replyErr.Error())
		}
	}()
	select {
	case reply := <-replyCh:
		if reply == nil {
			// A nil reply signals a handler error — pull it from the stash.
			if err := b.popError(corrID); err != nil {
				replyErr = err
				return nil, err
			}
			replyErr = ErrTimeout
			return nil, ErrTimeout
		}
		return reply, nil
	case <-ctx.Done():
		// Caller-side cancellation / deadline propagation is NOT a delivery
		// failure: the request may well have been delivered and handled. The
		// dead-letter queue is a bounded FIFO reserved for genuine delivery
		// failures, so recording cancellations here would evict them. Leave
		// replyErr nil.
		return nil, ctx.Err()
	case <-timer.C:
		replyErr = ErrTimeout
		return nil, ErrTimeout
	}
}

// Reply delivers a reply to a pending request identified by the correlation
// id. It is called by the agent's handler when it has the answer (asynchronous
// reply — the handler may compute the reply later and call Reply separately).
// A reply to an unknown correlation id (already timed out or cancelled) is a
// no-op best-effort drop.
//
// Args:
//   - corrID: the correlation id from the original request.
//   - reply: the reply message (From/To/CorrelationID are stamped by the bus).
//
// Returns:
//   - error: ErrInvalidMessage when corrID is empty.
func (b *Bus) Reply(corrID string, reply *Message) error {
	if corrID == "" {
		return ErrInvalidMessage
	}
	if reply == nil {
		return ErrInvalidMessage
	}
	return b.deliverReply(corrID, reply)
}

// deliverReply pushes a reply to the pending channel. Best-effort: a full or
// absent channel means the request already completed (timeout/cancel).
func (b *Bus) deliverReply(corrID string, reply *Message) error {
	b.mu.Lock()
	ch, ok := b.pending[corrID]
	b.mu.Unlock()
	if !ok {
		return nil // best-effort drop — orphan reply for a completed request
	}
	select {
	case ch <- reply:
	default:
	}
	return nil
}

// removePending deletes a pending entry. Called via defer in Request.
func (b *Bus) removePending(corrID string) {
	b.mu.Lock()
	delete(b.pending, corrID)
	delete(b.pendingErr, corrID)
	b.mu.Unlock()
}

// stashError stores a handler error so the caller can surface it after the
// nil-reply sentinel wakes the select.
func (b *Bus) stashError(corrID string, err error) {
	b.mu.Lock()
	b.pendingErr[corrID] = err
	b.mu.Unlock()
}

// popError returns and clears a stashed handler error.
func (b *Bus) popError(corrID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	err := b.pendingErr[corrID]
	delete(b.pendingErr, corrID)
	return err
}

// Delegate forwards a request to another agent on the caller's behalf (design
// §4 IPC: Delegate). The delegating agent is the one making the call; the
// target sees the delegator as the From. The original requester's
// correlation id is preserved end-to-end so the reply can chain back. This is
// the primitive for "I can't handle this — let me ask someone who can".
//
// Args:
//   - ctx: cancellation.
//   - delegator: the agent delegating (forwards on behalf of).
//   - to: the final target.
//   - topic: the request subject.
//   - payload: the request body.
//   - timeout: reply wait timeout.
//
// Returns:
//   - *Message: the reply.
//   - error: ErrAgentNotRegistered / ErrTimeout.
func (b *Bus) Delegate(ctx context.Context, delegator, to, topic string, payload any, timeout time.Duration) (*Message, error) {
	return b.Request(ctx, delegator, to, topic, payload, timeout)
}

// Handoff transfers a task's ownership from one agent to another (design §4
// IPC: Handoff). Unlike Send, Handoff carries a structured handoff payload
// (task id + context snapshot + artifacts) and the receiver acknowledges
// acceptance. The sender yields the task; the receiver takes it. This is the
// peer-to-peer task-transfer primitive — it does NOT go through the Scheduler.
//
// Args:
//   - ctx: cancellation.
//   - from: the yielding agent.
//   - to: the accepting agent.
//   - taskID: the task being handed off.
//   - contextSnapshot: the task context snapshot (selected projection).
//   - timeout: acceptance wait.
//
// Returns:
//   - *Message: the receiver's acceptance reply.
//   - error: ErrAgentNotRegistered / ErrTimeout.
func (b *Bus) Handoff(ctx context.Context, from, to, taskID string, contextSnapshot map[string]any, timeout time.Duration) (*Message, error) {
	payload := map[string]any{
		taskIDKey:   taskID,
		"context":   contextSnapshot,
		"artifacts": []any{},
	}
	return b.Request(ctx, from, to, "handoff-task", payload, timeout)
}

// Subscribe registers an agent's interest in a topic (design §4 IPC:
// Subscribe). Subscribers receive broadcast messages on that topic. A
// broadcast to a topic fans out to every subscriber's handler. This is the
// primitive for "I found X — anyone interested in X should know".
//
// Args:
//   - agentID: the subscribing agent.
//   - topic: the topic of interest.
//
// Returns:
//   - error: fmt.Errorf for an empty agent id.
func (b *Bus) Subscribe(agentID, topic string) error {
	if agentID == "" {
		return errors.New("agentipc: agent id required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// B16: deduplicate — don't add the same agent to the same topic twice.
	for _, existing := range b.subscribers[topic] {
		if existing == agentID {
			return nil // already subscribed
		}
	}
	b.subscribers[topic] = append(b.subscribers[topic], agentID)
	return nil
}

// Unsubscribe removes an agent's subscription to a topic.
func (b *Bus) Unsubscribe(agentID, topic string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subscribers[topic]
	out := subs[:0]
	for _, s := range subs {
		if s != agentID {
			out = append(out, s)
		}
	}
	b.subscribers[topic] = out
}

// Broadcast sends a message to every subscriber of a topic (fire-and-forget
// fan-out). Each subscriber's handler is invoked; a handler error is collected
// but does not stop the fan-out. Returns the count of successful deliveries.
//
// Args:
//   - ctx: passed to each handler.
//   - from: the broadcasting agent.
//   - topic: the broadcast topic.
//   - payload: the message body.
//
// Returns:
//   - int: number of subscribers that received the message without error.
func (b *Bus) Broadcast(ctx context.Context, from, topic string, payload any) int {
	b.mu.RLock()
	subs := make([]string, len(b.subscribers[topic]))
	copy(subs, b.subscribers[topic])
	b.mu.RUnlock()

	delivered := 0
	for _, subID := range subs {
		b.mu.RLock()
		h, ok := b.handlers[subID]
		b.mu.RUnlock()
		if !ok {
			continue
		}
		msg := &Message{
			ID:      b.allocID(),
			From:    from,
			To:      subID,
			Topic:   topic,
			Payload: payload,
			At:      b.allocNow(),
		}
		if _, err := h(ctx, msg); err == nil {
			delivered++
		}
	}
	return delivered
}

// allocID generates a unique message id (thread-safe).
func (b *Bus) allocID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nextIDLocked()
}

// allocNow returns the current time (thread-safe via atomic-less read of the
// now closure; the closure is write-once at construction).
func (b *Bus) allocNow() time.Time {
	return b.now()
}

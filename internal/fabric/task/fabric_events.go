package taskfabric

import (
	"context"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// pendingAppend is one durable-store write deferred until after f.mu is
// released. recordLocked builds it under the lock (cheap, in-memory only);
// flushAppends performs the actual store.Append I/O off-lock so a slow or
// blocking event store never stalls the fabric's CAS/state-machine mutex.
// Bounds for the durable-append path in flushAppends. Declared as vars (not
// consts) so white-box tests can shrink them — the production values are the
// defaults and nothing outside the package mutates them.
var (
	// flushAppendTimeout bounds a single store.Append. The fabric's in-memory
	// transition is already committed by the time the flush runs, so a store
	// that stops answering must fail this write rather than pin the caller's
	// goroutine — and, through the ordering barrier, every later mutation.
	flushAppendTimeout = 10 * time.Second
	// flushOrderWaitTimeout bounds how long a flush may wait for an earlier
	// sequence to land before it gives up on strict causal ordering. Exceeding
	// it is logged as a durable-order divergence; it never stalls silently.
	flushOrderWaitTimeout = 30 * time.Second
)

type pendingAppend struct {
	store  ares_events.EventStore // captured under lock — never read via f.store off-lock
	typ    EventType
	taskID string
	event  *ares_events.Event
	// seq is the fabric-wide monotonic sequence assigned under f.mu at record
	// time. flushAppends waits for seq contiguity so durable appends from
	// concurrent fabric calls land in causal order.
	seq uint64
}

// recordLocked appends one lifecycle event to the in-memory log (the only
// part that needs f.mu) and, when a store is attached, returns the durable
// write to be flushed AFTER the lock is released. It never performs I/O.
// Callers MUST be holding f.mu and MUST flush the returned value (via a
// deferred flushAppends) once unlocked. Returns nil when there is nothing to
// persist (no store, or an unmapped event type).
func (f *Fabric) recordLocked(t *Task, typ EventType) *pendingAppend {
	ev := TaskEvent{
		Type:       typ,
		TaskID:     t.ID,
		AgentID:    t.Owner,
		Origin:     t.Origin,
		State:      t.State,
		Checkpoint: t.Checkpoint,
		At:         f.now(),
	}
	// Every recorded event is a task mutation (state transition, lease change
	// or quantum boundary), so stamp UpdatedAt here to keep it the single
	// source of "last change" the Tasks page renders — otherwise terminal
	// transitions (Complete/Fail) would leave UpdatedAt frozen at the last
	// quantum. Matches the event's own timestamp so the two never drift.
	t.UpdatedAt = ev.At
	f.events = append(f.events, ev)
	// Cap the in-memory event log so it cannot grow unboundedly. Only compact when
	// the log exceeds 2×max so the amortized cost is O(1) per append.
	if max := maxInMemoryEvents; max > 0 && len(f.events) > 2*max {
		copy(f.events, f.events[len(f.events)-max:])
		f.events = f.events[:max]
	}
	if f.store == nil {
		return nil
	}
	et := taskEventType(typ)
	if et == "" {
		return nil
	}
	f.flushSeq++
	// Rebuild payload: must-persist events carry every
	// field RestoreFromStore needs to fold the task back (capability,
	// priority, dependencies, deadline, retry budget, creation time and the
	// versioned checkpoint JSON). Observability-only events keep the minimal
	// provenance payload.
	payload := map[string]any{
		restoreKeyTaskID:  t.ID,
		restoreKeyAgentID: t.Owner,
		restoreKeyOrigin:  t.Origin,
		restoreKeyState:   string(t.State),
		// The fencing epoch rides on EVERY persisted event, not just the
		// must-persist ones: Acquire bumps f.epoch and records the
		// observability-only task.acquired, so restricting the epoch to
		// must-persist events would lose every token granted after the last
		// checkpoint — the rebuilt fabric would then RE-ISSUE those tokens
		// and a stale pre-crash holder would pass ownerLocked's epoch check.
		restoreKeyEpoch: f.epoch,
	}
	// The strategy attribution key also rides on EVERY persisted event,
	// same reasoning as the epoch — the observability-only task.acquired/
	// task.completed events are the ones RuntimeObserver subscribes to, so
	// restricting the key to must-persist events would leave the observation
	// side reading nothing and every sample would fall back to "the strategy
	// active at fold time". The value comes from the checkpoint envelope;
	// decode is pure in-memory (safe under f.mu) and failure degrades to ""
	// (no key — the observer's activeID fallback), never an error.
	if sid := strategyIDFromCheckpoint(t.Checkpoint); sid != "" {
		payload[restoreKeyStrategyID] = sid
	}
	// SessionID rides on every event too, same reasoning as StrategyID —
	// the session scope must be visible without decoding checkpoints.
	if sid := sessionIDFromCheckpoint(t.Checkpoint); sid != "" {
		payload[restoreKeySessionID] = sid
	}
	// The task's capability rides on EVERY persisted event (not only the
	// must-persist ones that fold the rebuild): terminal task.completed/
	// task.failed events are what the skill outcome writer (the experience
	// WRITE side, M4.4) subscribes to, and its {skill=capability, pattern}
	// record needs the capability without decoding the checkpoint envelope.
	// Written unconditionally — an empty capability is a legal value for
	// unconstrained tasks; foldRestoreEvent requires the key to exist.
	payload[restoreKeyCapability] = t.Capability
	// Cascade provenance rides on every persisted event for a cascaded task
	// (same reasoning as capability): the skill outcome writer must be able
	// to tell a subtask that executed and failed from one that never ran
	// because a predecessor died, without decoding the checkpoint envelope.
	if t.FailedDependency != "" {
		payload[restoreKeyFailedDependency] = t.FailedDependency
	}
	// Token usage (input/output/total) rides on the TERMINAL events only:
	// the envelope accumulates the session's LLM spend across yield→resume
	// quanta (see CheckpointEnvelope v4), and the completed event is where
	// the RuntimeObserver's cost penalty reads it. Zero/absent on failed
	// events and pre-v4 envelopes — the observer then scores on outcome and
	// latency alone, never inventing a cost.
	if typ == EventTaskCompleted {
		if in, out := tokenUsageFromCheckpoint(t.Checkpoint); in > 0 || out > 0 {
			payload[restoreKeyInputTokens] = in
			payload[restoreKeyOutputTokens] = out
			payload[restoreKeyTotalTokens] = in + out
		}
	}
	if isMustPersistEvent(typ) {
		payload[restoreKeyPriority] = t.Priority
		if len(t.Dependencies) > 0 {
			deps := make([]string, len(t.Dependencies))
			copy(deps, t.Dependencies)
			payload[restoreKeyDependencies] = deps
		}
		if !t.Deadline.IsZero() {
			payload[restoreKeyDeadline] = t.Deadline.Format(time.RFC3339)
		}
		payload[restoreKeyRetryAttempts] = t.RetryPolicy.Attempts
		payload[restoreKeyRetryMax] = t.RetryPolicy.MaxRetries
		payload[restoreKeyCreatedAt] = t.CreatedAt.Format(time.RFC3339)
		if t.Checkpoint != nil {
			if b, err := MarshalCheckpoint(t.Checkpoint); err == nil {
				payload[restoreKeyCheckpointJSON] = string(b)
			} else {
				// The rebuilt task will resume without this checkpoint. Log
				// so the divergence is detectable; do not fail the transition.
				log.Error("taskfabric: checkpoint marshal failed (restore will lose progress)", "task_id", t.ID, "error", err)
			}
		}
	}
	return &pendingAppend{
		store:  f.store,
		typ:    typ,
		taskID: t.ID,
		event: &ares_events.Event{
			Type:       et,
			StreamID:   t.ID,
			ModuleName: "taskfabric",
			Payload:    payload,
			Timestamp:  ev.At,
		},
		seq: f.flushSeq,
	}
}

// flushAppends performs the deferred durable writes off-lock. It is registered
// with `defer f.flushAppends(&pending)` BEFORE `defer f.mu.Unlock()` so, by
// LIFO defer order, the unlock runs first and this flush runs immediately
// after — still within the same call (so divergence logging stays
// synchronous with the mutating method) but with f.mu already released (so the
// store I/O never blocks other fabric operations). Takes a pointer so it reads
// the slice's final value populated during the method body.
//
// Durability: must-persist events (TaskCreated, TaskCheckpointed,
// TaskCompleted, TaskFailed, TaskExpired) carry state the runtime relies on
// for recovery and replay. A failed append for these events is not silently
// swallowed — it is logged so a durable-state divergence (in-memory vs event
// log) is detectable. The in-memory state machine stays authoritative within a
// process (the append failure does not roll back the transition). Observability
// events remain best-effort and silent on failure.
//
// Liveness: the ordering barrier is bounded on BOTH sides. The append itself
// gets a deadline (a store that stops answering must not pin the caller), and
// the wait for an earlier sequence gets a deadline too — sync.Cond has no
// timed wait, so a broadcast timer wakes the waiter. Without those bounds a
// single wedged append would block every later fabric mutation forever: the
// in-memory transition has already happened by this point, so the caller would
// never return and the whole write path would stall behind one dead store.
func (f *Fabric) flushAppends(pending *[]*pendingAppend) {
	for _, p := range *pending {
		if p == nil {
			continue
		}
		// Wait until every earlier-recorded durable event has been
		// appended, so concurrent fabric calls flush in causal (record) order
		// and the store's per-stream version sequence never inverts.
		f.flushCond.L.Lock()
		deadline := time.Now().Add(flushOrderWaitTimeout)
		orderTimedOut := false
		for p.seq > f.flushedSeq+1 {
			if !time.Now().Before(deadline) {
				orderTimedOut = true
				break
			}
			// Wake this waiter at the deadline even if no flusher ever
			// broadcasts again.
			timer := time.AfterFunc(time.Until(deadline), f.flushCond.Broadcast)
			f.flushCond.Wait()
			timer.Stop()
		}
		if orderTimedOut {
			// A skipped event counts as PROCESSED for ordering purposes:
			// advance flushedSeq past it (still under the cond lock, still
			// monotonic). Without the advance, one timed-out skip permanently
			// poisons the barrier — the skipped seq is never completed by
			// anyone, so every later event would re-wait the full
			// flushOrderWaitTimeout only to skip as well, degrading every
			// subsequent fabric mutation to one timeout each.
			if p.seq > f.flushedSeq {
				f.flushedSeq = p.seq
			}
			flushed := f.flushedSeq
			// Durability outranks causal order for a durable transition. A
			// late append is recoverable — the store assigns its own per-stream
			// version and restore folds events by task id, not by position —
			// while a dropped task.completed/task.failed/task.created/
			// task.checkpointed/task.expired is not: the task would be rebuilt
			// from an older checkpoint and its already-committed work re-run.
			// So a must-persist event falls THROUGH to the append below
			// (out of causal order) instead of being dropped; only the
			// observability-only events take the skip path.
			if !isMustPersistEvent(p.typ) {
				f.flushCond.L.Unlock()
				f.flushCond.Broadcast()
				log.Error("taskfabric: durable append ordering timed out; skipping causal barrier",
					"event_type", p.typ, "task_id", p.taskID, "seq", p.seq, "flushed_seq", flushed)
				continue
			}
			log.Error("taskfabric: durable append ordering timed out; persisting must-persist event out of causal order",
				"event_type", p.typ, "task_id", p.taskID, "seq", p.seq, "flushed_seq", flushed)
		}
		var appendErr error
		if p.store != nil {
			ctx, cancel := context.WithTimeout(context.Background(), flushAppendTimeout)
			appendErr = p.store.Append(ctx, p.taskID, []*ares_events.Event{p.event}, 0)
			cancel()
		}
		// Advance the high-water mark monotonically: discardAppends and the
		// timeout-skip path may have already claimed past p.seq, so a bare
		// ++ would regress flushedSeq and re-open the causal barrier.
		if p.seq > f.flushedSeq {
			f.flushedSeq = p.seq
		}
		f.flushCond.L.Unlock()
		f.flushCond.Broadcast()
		if appendErr != nil {
			if isMustPersistEvent(p.typ) {
				log.Error("taskfabric: must-persist event append failed (durable log diverges from memory)", "event_type", p.typ, "task_id", p.taskID, "error", appendErr)
			}
		}
	}
}

// discardAppends claims pending durable-append sequence numbers that will
// never be written (CompilePlan batch rollback: flushing them would publish
// phantom task.created events for tasks that no longer exist). Mirrors the
// timeout-skip semantics of flushAppends deliberately — the discarded seqs
// count as PROCESSED for ordering purposes, so flushedSeq advances past them
// and the causal barrier is not poisoned by a gap nobody will ever complete.
// Without this, the next fabric mutation's flushAppends would wait the full
// flushOrderWaitTimeout on the abandoned seqs and then lose its own event to
// the timeout-skip path. Off-lock contract: call after releasing f.mu, same
// as flushAppends.
func (f *Fabric) discardAppends(pending *[]*pendingAppend) {
	var maxSeq uint64
	for _, p := range *pending {
		if p != nil && p.seq > maxSeq {
			maxSeq = p.seq
		}
	}
	if maxSeq == 0 {
		return
	}
	f.flushCond.L.Lock()
	if maxSeq > f.flushedSeq {
		f.flushedSeq = maxSeq
	}
	f.flushCond.L.Unlock()
	f.flushCond.Broadcast()
}

// isMustPersistEvent reports whether a lifecycle event is a must-persist
// transition: the runtime's recovery/replay correctness depends on these
// events being in the durable log. Other events (Ready, Acquired, Started,
// Yielded, Preempted, Released) are observability-only: they enrich
// the trace but are not required for state rebuild.
func isMustPersistEvent(typ EventType) bool {
	switch typ {
	case EventTaskCreated, EventTaskCheckpointed, EventTaskCompleted,
		EventTaskFailed, EventTaskExpired, EventTaskDeleted:
		return true
	default:
		return false
	}
}

// taskEventType maps the fabric's internal event type to the ares_events
// task.* event type. Unknown types map to "" and are never published.
func taskEventType(typ EventType) ares_events.EventType {
	switch typ {
	case EventTaskCreated:
		return ares_events.EventTaskCreated
	case EventTaskReady:
		return ares_events.EventTaskReady
	case EventTaskAcquired:
		return ares_events.EventTaskAcquired
	case EventTaskStarted:
		return ares_events.EventTaskStarted
	case EventTaskYielded:
		return ares_events.EventTaskYielded
	case EventTaskCheckpointed:
		return ares_events.EventTaskCheckpointed
	case EventTaskPreempted:
		return ares_events.EventTaskPreempted
	case EventTaskReleased:
		return ares_events.EventTaskReleased
	case EventTaskCompleted:
		return ares_events.EventTaskCompleted
	case EventTaskFailed:
		return ares_events.EventTaskFailed
	case EventTaskExpired:
		return ares_events.EventTaskExpired
	case EventTaskDeleted:
		return ares_events.EventTaskDeleted
	default:
		return ""
	}
}

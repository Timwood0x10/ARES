# GoAgent v2 Bug Report

**Date**: 2026-06-11
**Source**: Deep code review (4 agents, 4 modules)

---

## Critical Bugs (12)

### C1: Embedding queue dedup key mismatch
- **File**: `internal/storage/postgres/embedding_queue.go:83-87, 233-235`
- **Bug**: `Enqueue` uses SHA256, `Reconcile` uses PG MD5. Keys never match → duplicate tasks every reconcile cycle.
- **Fix**: Unify hashing logic — use Go SHA256 in both places.

### C2: Write buffer data loss on Stop()
- **File**: `internal/storage/postgres/write_buffer.go:124-127`
- **Bug**: `close(b.buffer)` triggers channel-close case which returns without flushing accumulated batch.
- **Fix**: Flush remaining batch before returning in channel-close case.

### C3: Embedding enqueue outside transaction
- **File**: `internal/storage/postgres/write_buffer.go:302-317`
- **Bug**: `Enqueue` uses Pool not transaction. If tx.Commit fails after Enqueue succeeds → orphaned tasks.
- **Fix**: Add `EnqueueTx(ctx, tx, task)` method.

### C4: FetchPendingTasks lock ineffective
- **File**: `internal/storage/postgres/embedding_queue.go:96-128`
- **Bug**: `FOR UPDATE SKIP LOCKED` without transaction — lock acquired and immediately released.
- **Fix**: Wrap fetch + mark-processing in single transaction.

### C5: Reconcile threshold time arithmetic wrong
- **File**: `internal/storage/postgres/embedding_queue.go:238`
- **Bug**: Go `time.Duration` (nanoseconds) passed to PG `timestamp - integer` → interpreted as days. 5 minutes = 821 million years.
- **Fix**: Pass `threshold.Microseconds()` and use `$1 * INTERVAL '1 microsecond'`.

### C6: Executor panic recovery sends on closed channel
- **File**: `internal/workflow/engine/executor.go:236-255`
- **Bug**: `wg.Done()` before `recover()` → main goroutine closes resultChan before recovery sends.
- **Fix**: Move `wg.Done()` after recover block.

### C7: Graph executor ignores in-degree
- **File**: `internal/workflow/graph/executor.go:84-93`
- **Bug**: BFS adds node to ready queue on ANY predecessor edge. Multi-predecessor nodes execute too early.
- **Fix**: Track in-degree, only add when in-degree drops to 0.

### C8: Queue send on closed channel panic
- **File**: `internal/protocol/ahp/queue.go:53-65, 136-149`
- **Bug**: `Enqueue` checks `closed.Load()` then sends — Close() can run between check and send.
- **Fix**: Add `defer recover()` or mutex guard.

### C9: HeartbeatSender Start/Stop race
- **File**: `internal/protocol/ahp/heartbeat.go:216-275`
- **Bug**: `sync.Once` not reusable. Start sets fields after releasing lock — Stop can see stale cancel.
- **Fix**: Move context/waitgroup setup inside mutex. Remove sync.Once.

### C10: WaitGroup panic in finalizeMemory
- **File**: `internal/agents/leader/agent.go:402-411`
- **Bug**: `select stopCh` + `distillWg.Add(1)` not atomic. Stop can close+Wait between check and Add.
- **Fix**: Add `distillMu` mutex to make close-then-wait atomic with check-then-add.

### C11: Start/Stop TOCTOU race
- **File**: `internal/agents/leader/agent.go:189-239`
- **Bug**: `Status()` check and `setStatus()` are separate lock acquisitions. Two goroutines can both pass the guard.
- **Fix**: Hold mutex across check-and-set.

### C12: Process/ProcessStream no mutual exclusion
- **File**: `internal/agents/leader/agent.go:438-448, 562-572`
- **Bug**: Two concurrent Process calls both see Ready, both set Busy, corrupt shared state.
- **Fix**: Add `processingMu` mutex.

---

## High Bugs (7)

### H1: Deadlock false positive (5s timeout)
- **File**: `internal/workflow/engine/executor.go:196-225`
- **Bug**: `wg.Wait()` waits for ALL goroutines, not just dependencies. Long tasks trigger false deadlock.
- **Fix**: Use sync.Cond or per-dependency wait.

### H2: DynamicExecutor hangs on node removal
- **File**: `internal/workflow/engine/dynamic_executor.go:155,328`
- **Bug**: Removed step skipped without sending result. Collection loop expects N results, gets N-1.
- **Fix**: Send synthetic "skipped" result when step is removed.

### H3: stepEg.Wait() concurrent with Go()
- **File**: `internal/workflow/engine/dynamic_executor.go:352,376`
- **Bug**: errgroup.Wait() called while Go() still being called — premature return.
- **Fix**: Use dedicated stepDone channel instead of errgroup.Wait().

### H4: NewDAG silently drops duplicate step IDs
- **File**: `internal/workflow/engine/types.go:148-153`
- **Bug**: Map insert overwrites first step with same ID. Edges become orphaned.
- **Fix**: Check for duplicates, return ErrDuplicateID.

### H5: getRandomSuffix nil dereference
- **File**: `internal/protocol/ahp/message.go:190-193`
- **Bug**: `rand.Int` error discarded. Nil `*big.Int` → panic on `.Int64()`.
- **Fix**: Handle error, return "000000" fallback.

### H6: SendMessage swallows all errors
- **File**: `internal/protocol/ahp/protocol.go:73-91`
- **Bug**: All errors mapped to ErrTaskQueueFull. DLQ reason always "queue_full".
- **Fix**: Preserve original error, distinguish reason types.

### H7: Protocol has no Close() method
- **File**: `internal/protocol/ahp/protocol.go`
- **Bug**: No way to close all queues. Channel/goroutine leak.
- **Fix**: Add Close() that closes all queues.

---

## Medium Bugs (16)

| # | File | Bug |
|---|------|-----|
| M1 | `pool.go:244` | ManagedRow connection leak — no finalizer |
| M2 | `migrate.go` | Missing tables: embedding_queue, dead_letter, knowledge_chunks, experiences |
| M3 | `circuit_breaker.go:110` | HalfOpen cleanup resets counter → multiple concurrent probes |
| M4 | `vector.go:122` | Dimension validation uses unrelated MaxVectorSearchLimit |
| M5 | `executor.go:454` | MaxAttempts=0 skips execution, returns fake success |
| M6 | `reloader.go:201` | TOCTOU race in scanAndLoad loses file changes |
| M7 | `reloader.go:254` | Map reference shared unsafely across callbacks |
| M8 | `graph/executor.go` | No validation of edge endpoints or node reachability |
| M9 | `dynamic_executor.go:451` | Version check-then-act race in recomputeOrder |
| M10 | `queue.go:123` | Peek() non-atomic, concurrent calls can return nil |
| M11 | `message.go:55` | NewTaskMessage allows nil payload |
| M12 | `dlq.go:114` | DLQ.Remove leaks trailing pointer in backing array |
| M13 | `ahp_adapter.go:21` | onFailure field has no synchronization |
| M14 | `resurrection.go:292` | onFailure missing isStopped check |
| M15 | `resurrection.go:370` | resurrect nil pointer after Unwatch |
| M16 | `leader/agent.go:400` | streamEg not tied to stopCh — goroutines survive Stop |

---

## Low Bugs (11)

| # | File | Bug |
|---|------|-----|
| L1 | `security.go:172` | safeFormatTable returns empty string on invalid name |
| L2 | `write_buffer.go:139` | Retry waits for next timer tick instead of immediate retry |
| L3 | `embedding_queue.go:63` | Enqueue silent on duplicate, no feedback |
| L4 | `queue.go:57` | ctx.Done() effectively dead code due to default branch |
| L5 | `dlq.go:106` | Clear() retains full backing array |
| L6 | `leader/agent.go:189` | Start doesn't cleanup on partial validation failure |
| L7 | `sub/agent.go:226` | ProcessStream goroutine has no lifecycle management |
| L8 | `leader/supervisor.go:185` | doFailover uses cancelled ctx for Stop |
| L9 | `dispatcher.go:130` | Partial results with misleading error messages |
| L10 | `dynamic_executor.go:312` | Bare go keyword in step goroutines |
| L11 | `mutable_dag.go:312` | Snapshot excludes step data |

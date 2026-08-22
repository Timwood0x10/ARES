# Bug: Kernel scheduler cooperative-preemption branch is unreachable — drain's wg.Wait keeps RUNNING tasks from ever crossing a drain boundary

- **ID**: BUG-KSCHED-001
- **Severity**: P2 / Medium (wired-but-dead feature: API and tests exist, the production path can never trigger it)
- **Status**: Fixed
- **Date**: 2026-08-22
- **Packages**: `internal/kernelscheduler`

## Symptom

`Scheduler.PreemptLowerPriority` (the scheduler-side wiring of P2.2
cooperative preemption) never preempts anything in the real scheduling loop.
Verified end to end: with a low-priority task RUNNING, creating a
higher-priority READY task produces no `task.preempted` event, and the
in-flight quantum keeps its slot until it finishes naturally.

## Trigger

Any normal scheduling flow through `Scheduler.Run`. No special configuration.

## Root cause

`drain()` ends with a `wg.Wait()` over every execution goroutine it spawned:

```go
// drain()
for _, taskID := range tasks { go s.execute(ctx, id) ... }
wg.Wait() // ← drain blocks until all in-flight quanta finish
```

`PreemptLowerPriority` is called only at the **entry** of `drain()`:

```go
tasks := s.fabric.ResumableTasks()
s.PreemptLowerPriority(tasks) // sole call site
```

Together: at any drain entry, every task dispatched by the previous drain has
already reached a terminal state (COMPLETED / FAILED / SUSPENDED — guaranteed
by RunQuantum's done/err/!done exits). `RunningTasks()` is therefore always
empty at the only place preemption runs, making the branch dead code.

The comment documents the intent as preempting "a task that is RUNNING from a
previous drain" — i.e. RUNNING was expected to survive across drains; the
`wg.Wait` serialization breaks exactly that premise.

## Fix

`Run` gains one managed watcher goroutine (exits on ctx cancellation, each
sweep recover-guarded — code_rules_v2 §4.1/§4.2) that calls
`PreemptLowerPriority(ResumableTasks())` once per poll tick, independent of
the blocking drain loop.

Semantics stay cooperative: preemption only mutates durable state
(RUNNING→READY, lease cleared); the stale holder's late completion is rejected
by the fencing token (`ErrNotOwner` / epoch mismatch, benign). The next free
drain re-acquires the preempted task.

The original entry-of-drain call site stays: it covers the immediate check
when nothing is in flight, and concurrent invocation with the watcher is made
safe by epoch fencing.

## Reproduction & regression test

`internal/kernelscheduler/scheduler_contract_test.go`:

```
TestPreemptLowerPriorityHandsBackRunningTask
```

A gated executor blocks its first quantum → a higher-priority task arrives →
assert `task.preempted` fires → release the gate → assert the high-priority
task completes and the low-priority task re-executes ≥ 2 times (stale holder
fencing-rejected, then freshly re-acquired). Before the fix the test failed
with "never preempted".

## Impact

- Cooperative preemption (P2.2) now actually works through the production
  scheduling path;
- Worst-case wait for a higher-priority task drops from "the current longest
  quantum" to "one preemption sweep plus the current step boundary";
- No behavioral-compatibility risk: the path previously never fired, so no
  caller can depend on its old never-preempts behavior.

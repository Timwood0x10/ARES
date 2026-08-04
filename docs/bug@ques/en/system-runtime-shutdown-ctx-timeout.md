# Bug: system_runtime shutdown ignores caller context timeout

- **ID**: BUG-SR-001
- **Severity**: P1 / High
- **Status**: fixed
- **Date**: 2026-08-04
- **Package**: `internal/system_runtime`

## Symptom

In `Orchestrator.Shutdown(ctx)`, both `stopComponent` and `cleanupComponent` call the
context-less `Waiter.Wait()` with only a hardcoded `stopTimeout` (30s) guard. They
**ignore the caller's `ctx` deadline entirely**.

As a result, when a caller invokes Shutdown with a shorter deadline (e.g. 5s) and a
component ignores cancellation (its `Wait()` blocks forever), Shutdown still blocks for
the full 30s before returning. This violates the "all boundary conditions must be handled
explicitly (timeout)" rule and can stall process exit.

## Trigger Conditions

- Any started component implements `Waiter` whose `Wait()` does not observe `ctx.Done()`.
- The caller passes a Shutdown context deadline shorter than `stopTimeout` (30s).

## Root Cause

The Wait branch in `orchestrator.go`:

```go
select {
case waitErr := <-waitCh:
    ...
case <-time.After(stopTimeout):
    log.Warn("system_runtime: wait timed out", ...)
}
```

It lacks a `ctx.Done()` branch, so a short caller deadline is not honored.

## Fix

Add a `case <-ctx.Done():` branch to the Wait select in both `stopComponent` and
`cleanupComponent`, so Shutdown respects the caller's deadline and returns early
(graceful abort, not a component error):

```go
select {
case waitErr := <-waitCh:
    ...
case <-time.After(stopTimeout):
    log.Warn("system_runtime: wait timed out", ...)
case <-ctx.Done():
    log.Warn("system_runtime: wait aborted by shutdown context", ...)
}
```

## Regression Tests

- `TestOrchestrator_Shutdown_BlockingWaiterTimesOut`: Shutdown with a 5s deadline and a
  forever-blocking `Wait()` must return within 7s (not 30s).
- `TestOrchestrator_Shutdown_AggregatesErrors`: Stop and errgroup errors are aggregated.
- `TestOrchestrator_Shutdown_IdempotentAndConcurrent`: concurrent Shutdown runs Stop once.
- `TestOrchestrator_Start_*`: cleanup and reverse-rollback on Bind/Start/Ready failure.

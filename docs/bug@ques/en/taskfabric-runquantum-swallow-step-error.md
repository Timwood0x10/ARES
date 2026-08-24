# RunQuantum Swallowed Step Errors → Failures Recorded as Successes

Date: 2026-08-24
Scope: internal/taskfabric, internal/kernelscheduler

## Symptom

Every task failure inside a kernel quantum was invisible:

- `Fabric.RunQuantum` dropped `stepErr` and returned `f.Fail(...)`'s result,
  which is `nil` for BOTH requeue-to-READY and final FAILED.
- The scheduler's `endQuantumOutcome(winner, capability, taskID, err)` therefore
  saw `err == nil` and recorded the failed quantum as a SUCCESS in
  `LoadTracker.End` (`ok++`) and W4 attribution (`Record(..., true)`).
- `Scheduler.Scheduled` incremented on failures.
- `logFailure` received `nil`, so the root cause never reached any log.

Repeatedly-failing agents saw their confidence RISE toward 1.0 (the exact
inverse of the documented EndNeutral rationale), poisoning scheduling scores.

## Fix

```go
if stepErr != nil {
    if failErr := f.Fail(taskID, agentID, epoch); failErr != nil {
        return errors.Join(stepErr, failErr)
    }
    return stepErr // state transition applied; caller must see the failure
}
```

The old contract test asserting "RunQuantum must swallow step error" was
rewritten to assert the new contract (error propagated AND state transitioned).

## Related fix (same review): panic leaked the LoadTracker slot

A panic inside an executor unwound through `RunQuantum` skipping
`endQuantumOutcome`; `tracker.load[winner]` stayed ≥ 1 forever, so
`Score`'s `(1-clamp01(load))` factor permanently zeroed the agent.
The scheduler now releases the slot neutrally via a defer guard on the
panic path only.

## Regression Tests

- `TestFabricRunQuantumFails` / `TestFabricRunQuantumFailsExhausted`
  (internal/taskfabric/quantum_test.go): error propagated, READY/FAILED states.
- `TestSchedulerAttributesFailureAsFailure`: attribution confidence drops to 0,
  tracker confidence 0, load released, Scheduled counter untouched.
- `TestSchedulerPanicReleasesLoadSlot`: after a panicking executor, agent load
  returns to 0 (still schedulable).

## Follow-up discovery: zero-confidence stranded retries (exposed by this fix)

Once failures were recorded truthfully, `TestGraphsEndpointNodeFailureReturns422`
hung: after one failure `LoadTracker.Confidence` drops to 0, and
`Score = overlap × (1-load) × confidence × boost` becomes 0 — so the only
capable executor could NEVER be rescheduled, its bounded retry budget could
never be spent, and collab-graph nodes waited forever. The old bug masked this
design flaw because failures were recorded as successes (confidence 1.0).

Fix in `taskfabric.Pick`: last-resort tier — when no capability-overlapping
candidate scores positive, return the best one ranked WITHOUT the confidence
factor (capability gate, load, priority still apply). Healthy candidates are
unaffected; a 0-confidence agent stays at the BOTTOM of the ranking per the
documented SetAgentConfidence contract instead of being un-schedulable.

Regression tests: `TestPick_LastResortKeepsAllFailureCandidateReachable`,
`TestPick_HealthyBeatsFailed`, `TestPick_NoOverlapStillNil`.

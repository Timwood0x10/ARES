# Lease Renewal, Live-DAG Injection, PG Subscription Loss, Archive Durability, Resurrection Race, Zombie Executors

Date: 2026-08-24
Scope: taskfabric, kernelscheduler, cmd/ares serve wiring, ares_events (PG store + archive), ares_runtime

## 1. No lease renewal → duplicate concurrent execution

`Lease.ExpiresAt` was set once at Acquire; no renewal existed. Any quantum
longer than the TTL was requeued by `CheckExpiredLeases` while the original
holder still ran — the recovery sweep fires every second — and a second
executor executed the same task concurrently (state stayed fenced-correct,
but spend/side-effects doubled).

Fix: `Fabric.Renew(id, agentID, epoch, ttl)` (fenced) plus a scheduler
heartbeat at ttl/3 (min 5s), managed by an errgroup for the quantum's
lifetime; it stops on completion, ctx cancel, or lost ownership.
`Scheduler.WithTTL` exposes the knob. Test:
`TestSchedulerRenewsLeaseDuringLongQuantum` (700ms step vs 300ms TTL →
exactly one execution).

Related clock-consistency fix: `Acquire` now builds leases on the FABRIC's
clock (`f.now`) instead of wall time — expiry is evaluated against f.now, so
the mixed pair made every lease born-expired once a fixture advanced the
fabric clock past real time.

## 2. UpdateLiveDAG never called in serve

Workflow/recovery structure patches mutated the synthetic input→process→output
bootstrap DAG forever; "live promotion" affected nothing observable.

Fix: `cmd/ares/serve_live_dag.go` builds the live topology from the configured
peer population (one node per peer; legacy agents.sub Dependencies become DAG
edges) and serve injects it via `RegisterAgentDAG("agents", …)` +
`NewEvolution.UpdateLiveDAG(dag)` after agent creation. Tests:
`TestBuildLiveAgentDAG_*`, `TestUpdateLiveDAG_WiredFromServeShape`.

## 3a. PG event subscription silently skipped timestamp ties

The poll cursor advanced to the batch max `created_at`; the next query used
strictly `>`, permanently skipping same-microsecond siblings beyond the LIMIT
cut (PG timestamps are µs; bursts routinely collide).

Fix: overlap-window polling — query `>= cursor`, dedup by event id
(bounded delivered-set), advance the cursor only when a poll drained the
window completely (fewer than LIMIT rows).

## 3b. Archive sink failure permanently lost the round record

The round boundary (`lastArchivedVersion`) advanced BEFORE the durable sink
write; on transient sink error there was no rollback, so compaction later
trimmed raws whose archive copy never landed.

Fix: commit the boundary ONLY after a successful sink write; on failure the
same window is retried with the same round number. Tests:
`TestArchiveSink_TransientFailureRetriesSameRound`,
`TestArchiveSink_BoundaryStaysZeroAfterFailure`.

## 4a. Resurrection-after-stop race (~1s window)

`NotifyAgentDead` checked operator intent once, then scheduled an async
restore ~1s out. A Stop/Pause landing inside that window was clobbered: the
restore installed a fresh running instance ~1s after the operator stopped it.

Fix: `managedAgent.operatorIntent` set by StopAgent/PauseAgent; RestoreAgent
re-checks it INSIDE the install critical section and aborts (desired state is
"stopped"). ResumeAgent clears the flag so future deaths resurrect normally.
Tests: `TestRestoreAgent_AbortsWhenOperatorStoppedAfterScheduling`,
`TestNotifyAgentDead_ThenOperatorStop_RestoreAborted`.

## 4b. Killed agents kept zombie executor registrations

`agentfabric.Kill` removed only the fabric entry; static scheduler
registrations lived forever — the stale-winner fallback could execute tasks on
a dead agent's registration, and every spawn_agent call grew the registry.

Fix: `Scheduler.reconcileFabricDeaths()` runs each drain and unregisters
executors whose fabric entry is gone, skipping recovery-bound replacements
(those unregister at terminal state). Tests:
`TestReconcileFabricDeaths_*`.

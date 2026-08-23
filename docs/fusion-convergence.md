# Fusion Convergence — self-verification (2026-08-22)

> Executes the DoD of `aresos-fusion-plan.md` Phase D: reference matrix,
> single-engine proof, and per-capability call chains from the `cmd/ares`
> production path. Every claim below is reproducible with the quoted grep.

## 1. Reference matrix — all deleted items at ZERO references

| Removed | Grep (production + tests) | Hits |
|---|---|---|
| `internal/plugins/resurrection` | `grep -rln "plugins/resurrection" --include="*.go" .` | **0** |
| `workflow.Runner` execution stack (`NewRunner`/`RunWorkflow`/`CompileBound`) | `grep -rln "NewRunner\|RunWorkflow\|CompileBound"` | **0** |
| Collaboration APIs (`DelegateToSpecialist`/`Orchestrate(`) | code refs only; remaining hits are historical COMMENTS in `evolution_ipc_test.go` (updated to topic names) | **0 code** |
| `api/graph` / `api/service/workflow` / `api/client` | package paths gone from module | **0** |
| Scheduler genome dimension (generation side) | `NewSchedulerGenome`/`NewSchedulerDiffer` | **0** |

## 2. Single-engine proof — every task executes through the kernel

Grep for task-creation/execution entry points in production code:

| Site | Engine used |
|---|---|
| `sdk/task.go` `Submit` → `submitThroughScheduler` → `sdkFabric.Create` | kernel fabric + kernelscheduler |
| `sdk/graph_run.go` agent nodes | `rt.Submit` (same path) |
| `cmd/ares/collab_graph.go` `runCollabGraph` → `fabric.Create(Dependencies)` | kernel fabric DAG + kernelscheduler |
| `cmd/ares/peer_mode.go` `submitPeerTask` → `POST /api/tasks` | kernel fabric + kernelscheduler |
| `cmd/ares/evolution_ipc.go` `executeCollabViaKernel` → `runCollabGraph` | kernel fabric DAG |

The only non-fabric node execution is `sdk.Graph` FUNCTION nodes running
inline (pure compute, no LLM, no durable intent) — documented at the type and
in v040 design §3. No second scheduler, no second runner exists.

## 3. Per-capability production call chains

| Capability (phase) | Chain (file:function) |
|---|---|
| Task recovery (W1) | `serve_agents.go:createAndServeAgents` → `createPeerAgents` → `kernel_loop.go:runKernelRecoveryLoop` → `aresrecovery.RestartAgent` (A2: in-place revival under same id) |
| Agent cognitive revival (A) | `agentfabric/lifecycle.go:Kill` → snapshot capture (`snapshot.go`) → `kernel_loop` arbitration → `RestartAgent(same id)` → `lifecycle.go:Recover` |
| Chaos ops (A/P1) | `actions.go:handleChaos` → `peer_mode.go:chaosKillRandomFabric/chaosKillAllFabric` → `agents.Kill`; recover → `chaosRecoverSweep` → `RequeueExpiredLeases` |
| Graph submission (B/C4) | `actions.go:handleSubmitGraph` → `collab_graph.go:runCollabGraph` → `fabric.Create(Dependencies)` → kernelscheduler |
| IPC collaboration topics (C2) | `serve.go:setupPeerRegistry` → `evolution_ipc.go:wireEvolutionIPC(kernel)` → `executeCollabViaKernel` → `runCollabGraph` |
| Memory distill (0.2.x→G1) | bootstrap distiller subscribes EventStore; spawn prior via `peer_mode.go:loadExperiencePrior` |
| Evolution feedback (F1/W4) | `kernelscheduler/scheduler.go:endQuantumOutcome` → `attribution.Record` → feedback loop → tracker confidence |

## 4. Gates at convergence

`make fmt && make check` → EXIT 0 (gofmt empty · vet · staticcheck ·
golangci-lint 0 issues · full suite green). `-race` verified on all touched
packages per phase.

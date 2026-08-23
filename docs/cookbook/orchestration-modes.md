# Orchestration Modes — one engine, three shapes

> Fusion plan Phase C: the retired `DelegateToSpecialist` / `NewPipeline` /
> `Orchestrate` collaboration APIs are expressed with ONE execution substrate —
> the kernel fabric DAG (`taskfabric` Dependencies + `kernelscheduler`), which
> is the same engine `sdk.Graph` compiles to. Pick your surface:
>
> - **Go code**: `sdk.Graph` + `rt.RunGraph` (conditions, router, subgraphs).
> - **HTTP ops**: `POST /api/graphs` (explicit DAG of capability nodes).
> - **IPC topics**: `delegate-task` / `pipeline-stage` / `orchestrate-worker`
>   (protocol unchanged; handlers drive the same kernel path).

## Semantic mapping

| Legacy API | Capability | sdk.Graph shape | Kernel-DAG shape |
|---|---|---|---|
| `DelegateToSpecialist(delegator, specialist, taskID, payload)` | hand ONE unit of work to a capable peer | 2 nodes: `prepare → specialist` | 1 node whose capability = specialist |
| `NewPipeline(stages…)` + `Run(from, input)` | sequential stages, output feeds next | chain `s1 → s2 → …`, each stage reads prior writes from shared state | chain via Dependencies; stage outputs read from completion checkpoints |
| `Orchestrate(coordinator, workers, taskID, payload)` | fan-out to N workers in parallel, aggregate | root fan-out + Join node with N incoming edges (round barrier) | worker nodes share deps on root; join node depends on ALL workers |

Executable proofs:

- Pipeline + join semantics: `cmd/ares/collab_graph_test.go`
  (`TestGraphsEndpointPipeline`, `TestGraphsEndpointFanOutJoin`).
- Conditions/router/subgraphs: `sdk/graph_test.go`
  (`TestGraphConditionBranch`, `TestGraphRouterLoop`,
  `TestGraphNodeSubgraphNode`).

## What was NOT carried over (intentional)

- Ordering schedulers (FIFO/Priority/SJF/RR/WeightedFair): a fully-parallel
  ready batch has no "who runs first" decision; concurrency throttling lives
  on as `sdk.Graph.MaxRoundConcurrency`.
- The scheduler EVOLUTION dimension: retired generation-side
  (`TODO(evolution-dim)` in `provide_new_evolution.go` marks the candidate
  successor — a concurrency genome evolving MaxRoundConcurrency).

## HTTP usage

```bash
curl -X POST http://localhost:8080/api/graphs \
  -H "Authorization: Bearer $ARES_API_KEY" \
  -d '{
    "schema_version": 1,
    "run_id": "review-pipeline",
    "nodes": [
      {"id": "research", "capability": "research", "input": "topic X"},
      {"id": "review",   "capability": "review",   "input": null},
      {"id": "publish",  "capability": "writer",   "input": null}
    ],
    "edges": [
      {"from": "research", "to": "review"},
      {"from": "review",   "to": "publish"}
    ]
  }'
# 200 {"graph_id":"review-pipeline","outputs":{"research":"…","review":"…","publish":"…"},"success":true}
```

Validation runs BEFORE any task is created: unknown capability → 400 listing
available capabilities; wrong `schema_version` → 400; node count bounded by
the Graph builder caps.

# Code Compliance Tasklist

> 依据 `plan/rules/code_rules.md` 生成的整改清单。
> 状态: ✅ 已完成 | 🔄 进行中 | ⏳ 待开始 | ❌ 阻塞

---

## P0 — 编译/构建阻塞

| # | 规则 | 文件 | 问题 | 状态 |
|---|------|------|------|------|
| 1 | ③错误处理 | `cmd/ares/actions.go` | `//nolint: errcheck` 标注 | ✅ |
| 2 | ③错误处理 | `cmd/ares/arena.go` | `//nolint: errcheck` 标注 | ✅ |
| 3 | ③错误处理 | `cmd/ares/bridge.go` | `//nolint: errcheck` 标注 | ✅ |
| 4 | ④并发 | `cmd/ares/serve.go` | 裸 `go` → `errgroup.WithContext` | ✅ |
| 5 | ④并发 | `cmd/ares/demo.go` | 裸 `go` → `errgroup.WithContext` | ✅ |
| 6 | ④并发 | `internal/llm/output/*` | 5 处裸 `go func()` | ⏳ |

## P1 — 文件长度（≤ 1000 行）

| # | 行数 | 文件 | 拆分方案 | 状态 |
|---|------|------|----------|------|
| 7 | 2111 | `internal/storage/postgres/services/retrieval_service_test.go` | 拆 search / merge / utility | ⏳ |
| 8 | 1574 | `internal/storage/postgres/services/retrieval_helpers.go` | 拆 query / rewrite / bm25 | ⏳ |
| 9 | 1497 | `internal/ares_runtime/runtime_test.go` | 已有 `runtime_core_test.go` | ⏳ |
| 10 | 1451 | `examples/end-to-end/main.go` | 拆为子包 | ⏳ |
| 11 | 1433 | `internal/ares_evolution/genome/population_test.go` | 拆 evolve / selection / mutation | ⏳ |
| 12 | 1420 | `internal/storage/postgres/repositories/tool_repository_test.go` | 拆 crud / cache / search | ⏳ |
| 13 | 1367 | `internal/ares_evolution/genome/selection_test.go` | 拆个休 / 群体 / 锦标赛 | ⏳ |
| 14 | 1356 | `internal/ares_evolution/genome/intelligence_test.go` | 拆 basic / advanced / edge | ⏳ |
| 15 | 1352 | `internal/workflow/engine/dynamic_executor_test.go` | 已有 `dynamic_executor_core_test.go` | ⏳ |
| 16 | 1316 | `internal/ares_shutdown/shutdown_comprehensive_test.go` | 拆 graceful / force / timeout | ⏳ |
| 17 | 1299 | `internal/ares_evolution/scoring/memory_aware_scorer_test.go` | 拆 scoring / decay / edge | ⏳ |
| 18 | 1298 | `internal/api_impl/api_impl_test.go` | 已有 `api_impl_core_test.go` | ⏳ |
| 19 | 1286 | `internal/ares_events/compactor_test.go` | 拆 compaction / eviction / query | ⏳ |
| 20 | 1275 | `internal/plugins/resurrection/resurrection_test.go` | 拆 lifecycle / failover / recovery | ⏳ |
| 21 | 1247 | `internal/ares_evolution/genome/integration_test.go` | 拆 test 子包 | ⏳ |
| 22 | 1220 | `internal/ares_quant/research/agents/agents_test.go` | 拆 strategy / execution / report | ⏳ |
| 23 | 1209 | `internal/retrievalservice/retrievalservice_test.go` | 拆 query / cache / embedding | ⏳ |
| 24 | 1170 | `internal/ares_memory/service/service_test.go` | 拆 crud / session / task | ⏳ |
| 25 | 1148 | `internal/storage/postgres/repositories/task_result_repository_test.go` | 拆 crud / batch / query | ⏳ |
| 26 | 1122 | `examples/autonomous-evolution/scenarios.go` | 拆 scenario / evaluation / report | ⏳ |
| 27 | 1078 | `internal/ares_memory/context/cleaner_test.go` | 拆 clean / evict / stats | ⏳ |
| 28 | 1071 | **`internal/dashboard/api.go`** | **已拆 `api.go`(253) + `api_handlers.go`(840)** | ✅ |
| 29 | 1042 | `internal/ares_flight/flight_test.go` | 拆 timeline / graph / diagnostics | ⏳ |
| 30 | 1039 | `internal/storage/postgres/repositories/conversation_repository_test.go` | 拆 session / message / query | ⏳ |
| 31 | 1033 | `api/tools/tools_test.go` | 拆 register / execute / error | ⏳ |
| 32 | 1022 | `internal/storage/postgres/repositories/knowledge_repository_test.go` | 拆 crud / embed / search | ⏳ |
| 33 | 1009 | `internal/ares_memory/production_manager.go` | 拆 init / session / task | ⏳ |

## P2 — 函数长度（≤ 100 行）

| # | 文件 | 函数 | 行数 | 状态 |
|---|------|------|------|------|
| 34 | `internal/ares_runtime/manager.go` | `handleRestore` | ~120 | ⏳ |
| 35 | `internal/dashboard/api.go` | `handleRoot` | ~120（已迁到 api_handlers.go） | ⏳ |
| 36 | `internal/tools/resources/builtin/memory/memory_tools.go` | `UserProfile.Execute` | ~110 | ⏳ |
| 37 | `internal/llm/output/validator.go` | `validateValue` | ~105 | ⏳ |

## P3 — 公共函数缺少注释

| # | 文件 | 缺少注释的函数 | 状态 |
|---|------|-----------------|------|
| 38 | `internal/dashboard/api.go` | `handleAgents`, `listAgents`, `handleMCP`, 等 30+ 个 handler | ⏳ |
| 39 | `internal/monitoring/http_api.go` | `handleHealth`, `handleAnomalies`, `handleInsights` | ⏳ |

## P4 — 格式化/导入分组

| # | 文件 | 问题 | 状态 |
|---|------|------|------|
| 40 | 全局 | 运行 `goimports -w .` | ⏳ |
| 41 | 全局 | 运行 `gofmt -s -w .` | ⏳ |

---

## 执行顺序

```
P0 (错误处理 + 并发) → P1 (拆文件) → P2 (拆函数) → P3 (补注释) → P4 (格式化)
```

# ORPHAN_MODULES.md — 孤儿模块清单（决定性强查）

> 生成时间：脚本扫描 `github.com/Timwood0x10/ares` 全仓 216 包。
> 方法：反向 import 图 + `go list -deps` 算所有 main 入口(11)与 `api/*` 公开包(13)的传递依赖并集。
> 判据：不在任何二进制传递依赖 = 运行时不可能被用（反射也调不到未编译进二进制的包）。

## 总览

- 总包数：**216**（含 main 37）
- 孤儿包（非 main、零内部 import 方）：**47**
  - 🔴 真死 `internal/*`：**26**（不在任何二进制、也不被公开 SDK 间接引用）
  - 🟡 公开 SDK 契约 `api/*`：**13**（意图性内部孤儿，留给外部消费者 import）
  - 🟠 兼容/可选适配器 `compat/*`：**8**（多为死适配器或按需 build-tag/接口选型）

## 🔴 真死 internal 包（26）

| # | 包路径 | 代码行数(非test) | 导出符号 | 备注 |
|---|---------|----------------|----------|------|
| 1 | `internal/api_impl` | 882 | 39 |  |
| 2 | `internal/ares_eval/service` | 1302 | 36 |  |
| 3 | `internal/ares_experience/service` | 92 | 12 |  |
| 4 | `internal/ares_integration` | 0 | 0 |  |
| 5 | `internal/ares_quant` | 340 | 1 |  |
| 6 | `internal/ares_quant/dataflow` | 585 | 33 |  |
| 7 | `internal/ares_quant/errors` | 7 | 1 |  |
| 8 | `internal/ares_quant/marketmaking` | 1328 | 45 |  |
| 9 | `internal/ares_quant/marketmaking_api` | 1569 | 64 |  |
| 10 | `internal/ares_quant/research/agents` | 1261 | 49 |  |
| 11 | `internal/ares_quant/store` | 382 | 20 |  |
| 12 | `internal/ares_security` | 440 | 14 |  |
| 13 | `internal/ares_shutdown` | 1017 | 57 |  |
| 14 | `internal/evolution/deployment` | 237 | 11 |  |
| 15 | `internal/knowledge/mcp` | 269 | 4 |  |
| 16 | `internal/knowledge/provider/code` | 235 | 5 |  |
| 17 | `internal/knowledge/provider/mysql` | 242 | 6 |  |
| 18 | `internal/knowledge/provider/vector` | 269 | 7 |  |
| 19 | `internal/knowledge/store/postgres` | 399 | 12 |  |
| 20 | `internal/knowledge/store/sqlite` | 325 | 11 |  |
| 21 | `internal/knowledge/workflow` | 143 | 12 |  |
| 22 | `internal/memoryservice` | 714 | 21 |  |
| 23 | `internal/retrievalservice` | 638 | 18 | 仅 test 引用 |
| 24 | `internal/storage/memory` | 142 | 5 | 仅 test 引用 |
| 25 | `internal/storage/postgres/query` | 514 | 24 |  |
| 26 | `internal/tools/resources` | 102 | 1 |  |

这些包不被任何 main 二进制编译、也不被 `api/*` 公开 SDK 传递依赖。Go `internal/` 规则禁止外部 import，故为**真死代码**。

## 🟡 公开 SDK 契约 api/*（13）

| # | 包路径 | 代码行数(非test) | 导出符号 | 备注 |
|---|---------|----------------|----------|------|
| 1 | `api/agent` | 103 | 5 |  |
| 2 | `api/bootstrap` | 444 | 20 |  |
| 3 | `api/client` | 1410 | 58 | 仅 test 引用 |
| 4 | `api/evolution/genome` | 224 | 7 |  |
| 5 | `api/flight` | 133 | 21 |  |
| 6 | `api/graph` | 89 | 21 |  |
| 7 | `api/integration` | 0 | 0 |  |
| 8 | `api/memory` | 250 | 24 |  |
| 9 | `api/memory/distillation` | 146 | 11 |  |
| 10 | `api/service/callbacks` | 59 | 9 |  |
| 11 | `api/service/eval` | 114 | 10 |  |
| 12 | `api/service/events` | 192 | 13 |  |
| 13 | `api/service/knowledge` | 264 | 4 |  |

意图性内部孤儿：设计上由外部仓库 import，仓库内无任何代码引用（连 `internal/api_impl` 都没接上它们）。非死代码，但当前未被使用。

## 🟠 兼容/可选适配器 compat/*（8）

| # | 包路径 | 代码行数(非test) | 导出符号 | 备注 |
|---|---------|----------------|----------|------|
| 1 | `compat/llm/ollama` | 61 | 5 |  |
| 2 | `compat/loader/html` | 45 | 5 |  |
| 3 | `compat/loader/markdown` | 39 | 5 | 仅 test 引用 |
| 4 | `compat/loader/pdf` | 78 | 5 |  |
| 5 | `compat/protocol/mcp` | 176 | 5 |  |
| 6 | `compat/protocol/openai_api` | 1117 | 7 |  |
| 7 | `compat/tool/builtin` | 45 | 5 | 仅 test 引用 |
| 8 | `compat/vector/pgvector` | 169 | 6 |  |

零内部 import 方、不在任何 main 依赖。多为死适配器，或经 build-tag/接口按需选型（需逐包确认是否仍有用）。

## 结论与建议

- **仍有大量孤儿**，远超 CLOSURE_PLAN 点名范围。CLOSURE_PLAN 的 P0/P1/P2.3 在现码已闭环（计划基于过期快照），但其『孤儿严重』的大判断为真——孤儿集中在**服务层 / knowledge / quant / evolution-deployment**。
- **不擅自接线**（遵循『方向优先于接线』）：接上未实现/半成品包 = 空转。
- 建议二选一，待用户定方向：
  1. **删除未发布功能**：这些包若本不进产品，直接清理（最干净，消除假闭环噪声）。
  2. **真正接线**：经 `internal/ares_bootstrap` 接到运行时——但需先确认实现完整、非半成品。

> 注：本文件为只读核查产物，未被纳入 git 跟踪，按需可删除。

---

## 评估结论（逐包 verdict，2026-07-21 补）

> 方法补充：在可达性之外，逐包核查了 (a) 是否含 `New*`/`RegisterRoutes`/`StartService` 构造器；(b) 内部依赖是否全部已接线；(c) 是否有运行时触发点；(d) 是否为 bootstrap 已选实现的替代品。结论：**绝大多数不该删，是可接线/可嵌入的完整功能**；但接线方式不统一，分四类处理。

### ✅ 可直接接线（完整功能 + 有明确触发点/用途）
| 包 | 关键构造器 | 接线点 / 用途 | 接线状态 (2026-07-21) |
|---|---|---|---|
| `internal/api_impl` | `StartService`/`RunReview`/`Stop`/`HTTPServer` | **完整应用启动器**（LLM+MCP+dashboard+event store+flight 一键拉起），是 `cmd/ares` 的替代/可嵌入启动路径。 | ✅ **已接**：`cmd/ares start` 子命令 (start.go) + 样例 `configs/api_impl.yaml` |
| `internal/ares_eval/service` | `NewService`/`RegisterRoutes`/`NewPGEvalResultRepository` | 评估 HTTP 服务，挂载路由即生效。 | ✅ 已接（PG 条件触发；经 `ares start` 挂 `/api/v1/eval/*`） |
| `internal/ares_experience/service` | `NewConflictResolver`/`NewDistillationService`/`NewRankingService` | 经验服务；ranking/conflict 无依赖，distillation 需 embedding+LLM+repo。 | ✅ 已接（ranking/conflict 构造暴露；distillation 仍待 embedding） |
| `internal/ares_security` | `NewSanitizer`/`NewSafeLogger` | 输入净化，接入 pipeline 即生效。 | ✅ **已接**：`llm.Client` 加 `WithSanitizer`，`recordLLMCall` 落盘前脱敏 |
| `internal/ares_shutdown` | `NewManager`/`NewCallbackRegistry` | 优雅关停钩子。 | ✅ **已接**：`runServe` 信号处理器改 `Manager`（HTTP→MCP→runtime 三阶段钩子） |
| `internal/memoryservice` | `NewService`/`NewMemoryRepository` | agent 记忆服务。 | ✅ 已接（内存 repo 构造 + 挂 `/api/v1/sessions/*`） |
| `internal/retrievalservice` | `NewService` | 检索服务（仅 test 引用，接线即启用）。 | ✅ 已接（内存 repo 构造 + 挂 `/api/v1/knowledge/*`） |
| `internal/storage/postgres/query` | `NewQueryCache`/`NewMemoryQueryCache` | postgres 查询缓存。 | ✅ 已接（构造暴露于 Service，Stop 时 Close；无独立 HTTP） |

### ⚠️ 后端可切换适配器（用户 2026-07-21 纠正：非死代码，保留）
- **`internal/knowledge/store/postgres`、`knowledge/store/sqlite`、`knowledge/provider/{vector,mysql,code}`、`knowledge/mcp`、`knowledge/workflow`**：KB 的**灵活切换后端 DB 的适配器**（按 config/选型切换，不是半成品）。**保留，不删**。它们不是默认运行时要接进 `internal/knowledge` 父包——切换靠配置/provider 选择，不属于"孤儿待接线"范畴。
- **`internal/ares_quant/*`**（quant/dataflow/errors/marketmaking/marketmaking_api/research/agents/store + 未列的全死子图 indicators/market/portfolio/research）：一整套**量化交易子系统**（5000+ 行，完整实现）。属独立产品功能，不在当前 agent 主链路。→ 是否纳入产品范围（接线）还是暂留（不删）。
- **`internal/storage/memory`**：内存向量后端（P2.2 孤岛）。接成 `Storage.Type=memory` 需补整套 memory repository，否则空转。→ 定「是否要内存后端」。
- **`internal/ares_quant/*`**（quant/dataflow/errors/marketmaking/marketmaking_api/research/agents/store + 未列的全死子图 indicators/market/portfolio/research）：一整套**量化交易子系统**（5000+ 行，完整实现）。属独立产品功能，不在当前 agent 主链路。→ 是否纳入产品范围（接线）还是暂留（不删）。
- **`internal/storage/memory`**：内存向量后端（P2.2 孤岛）。接成 `Storage.Type=memory` 需补整套 memory repository，否则空转。→ 定「是否要内存后端」。

### 🚫 接上会空转（无触发点）→ 先定触发再接，否则删
- **`internal/evolution/deployment`**：`DeploymentPipeline.Deploy` 全仓零调用方（进化系统的 Deploy 走的是 `activeStrategyMgr.Deploy` / `stateManager.Deploy`，非此 Pipeline）。接上无人调用=空转。→ 要么让进化部署路径改用此 Pipeline（架构改动），要么删。

### 🔍 误报（非死代码，不动）
- **`internal/ares_integration`**：目录**仅 `_test.go`（6345 行测试，零生产代码）**，是测试套件不是功能包。从删除候选移除。

### 🧹 薄父包 / 可能冗余
- **`internal/tools/resources`**：102 行、1 导出，实际函数已迁至 `agent/` 与 `builtin/` 子包。可能仅剩薄门面，评估是否可删或保留为 facade。

### `api/*` 公开 SDK
纯契约/客户端库（外部可直接 import 使用，「外部能用」成立）。无需删；服务端是否实现由 `api_impl` 等决定（`api_impl` 不实现 `api/*`，是独立启动器）。

### `compat/*` 适配器（8 个）
零内部引用、不在任何 main 依赖。多为按需 build-tag/接口选型的兼容后端；逐包确认是否仍有用，无用则删。

### 建议接线顺序（待用户定范围后执行）
1. **第一批（零风险、明确价值）**：`ares_security`、`ares_shutdown`、`storage/postgres/query` → 接入现有 pipeline/bootstrap。
2. **第二批（HTTP 服务）**：`ares_eval/service`、`ares_experience/service`、`memoryservice`、`retrievalservice` → 在 bootstrap 挂路由/构造。
3. **设计决策后再动**：knowledge/* 多后端、`ares_quant` 范围、`storage/memory` 后端、`evolution/deployment` 触发。
4. **保留/嵌入**：`api_impl` 作为可嵌入启动器；`ares_integration`(测试) 与 `api/*`(SDK) 不动。

---

## 接线进展日志（2026-07-21，第二部分）

### 已接（Phase 1，已 `go build` 验证通过）
1. **`internal/api_impl` → `cmd/ares start`** (`cmd/ares/start.go` + `configs/api_impl.yaml`)
   - 新增 cobra 子命令 `ares start`，加载 `api_impl.ServiceConfig` 后调 `StartService` + `Wait()`。
   - 使"完整应用启动器"成为可达的嵌入/替代启动路径（之前零调用方=真死）。
2. **`internal/ares_security` → `llm.Client`** (`internal/llm/client.go` + `internal/ares_bootstrap/provide_llm.go`)
   - `llm.Client` 增 `WithSanitizer` option；`recordLLMCall`（落盘/链路追踪前）对 prompt/response 调 `Sanitizer.Sanitize` 脱敏。
   - 触发点=每一次真实 LLM 调用；不改请求体，仅净化记录副本，杜绝密钥泄漏到日志/event store。
   - 无 import 环（`ares_security` 仅依赖 stdlib）。
3. **`internal/ares_shutdown` → `cmd/ares runServe`** (`cmd/ares/serve.go`)
   - 用 `ares_shutdown.NewManager` 替换手写信号 goroutine：注册 4 阶段（PreShutdown 5s / Graceful 20s / Force 5s / Done 1s）。
   - 真实钩子：`PhasePreShutdown=httpSrv.Shutdown`、`PhaseGraceful=comp.MCP.Stop + mgr.Stop`（runtime）。

### 剩余 5 个（⏳）的关键障碍（决定不能"盲目接上"）
经代码核查，用户"已接线的 postgres/core，挂上即生效"对默认路径不成立，故这 5 个需要共享基础设施改造 + 决策，盲目接=空转/坏代码：
- **`ares_eval`**：repo 仅 PG（`NewPGEvalResultRepository(db postgres.DBTX, pool *sql.DB)`，`repository.go:48`），**无内存实现**。`Bootstrap` 默认不接 PG（`bootstrap.go:74-95` EventStore/Memory 默认内存），故 eval 需先在 `Bootstrap` 暴露可选 `*postgres.Pool`（从 `cfg.Storage` 构造，仅 `Type=="postgres"` 时）。
- **`ares_experience`**：`DistillationService` 需 `embedding.EmbeddingClient`，而**全仓无已接线的 embedding client**（grep 无 `NewClient`/`EmbeddingClient`）。只有 `NewRankingService()`/`NewConflictResolver()` 无依赖可构造；distillation 卡住。
- **`storage/postgres/query`**：`QueryCache` **无消费方**。`retrievalservice` 用的是自家本地 cache（`services/retrieval_service.go`），非此包。接上无人调用=空转，除非把 retrieval 路径改吃此 cache。
- **`memoryservice` / `retrievalservice`**：可凭内存 repo 构造（`NewMemoryRepository()`，无 PG 依赖），挂 HTTP 即生效；但挂哪个 HTTP 服务器有分歧——`dashboard.APIv2`（设计扩展点，但**只被 `ares start` 服务**，`ares serve` 走 monitoring console 插件，不吃 APIv2 路由）。

### 建议的 Phase 2 接法（待确认）
- `Bootstrap` 增可选 `ProvidePostgres`（cfg.Storage→`*postgres.Pool`），Components 暴露 `DB`。
- `Bootstrap` 增 `ProvideServices`：`memoryservice`/`retrievalservice` 用内存 repo 构造并暴露；`ares_eval` 仅当 `DB!=nil` 时构造（PG repo）并暴露；`ares_experience` 构造 ranking+conflict-resolver 暴露，distillation 留 nil（缺 embedding）；`query` 构造内存 `QueryCache` 暴露。
- HTTP 暴露：`dashboard.APIv2` 增 `SetEval/SetMemory/SetRetrieval` + `MountGinRoutes`/`Handler()` 挂载（经 `api/handler`）。→ 经 `ares start` 可达；`ares serve` 需另接 monitoring server（更大改动）。
- 决策点（待用户定）：① 是否接受"eval 仅当 PG 配置才生效"？② experience distillation 是否要先接 embedding client？③ query cache 是否要改造 retrieval 路径去消费？④ HTTP 挂 `ares start`(APIv2) 还是 `ares serve`(monitoring)？

### 已接（Phase 2，已 `go build ./...` 相关包验证通过）

用户决策（AskUserQuestion）：**「接可行的子集（推荐）」** —— 不强行接 Bootstrap/postgres 默认路径，只在 `api_impl.StartService`（可达的 `ares start` 启动器）里接可行子集，不破坏现有路径。

1. **`memoryservice` / `retrievalservice` → HTTP（`ares start` 的 `dashboard.APIv2`）**
   - 两 service 用各自 `NewMemoryRepository()` 构造（无 PG 依赖），经 `api/handler` 的 `MemoryHandler`/`RetrievalHandler` + `api/router.Router.RegisterMemoryEndpoints/RegisterRetrievalEndpoints` 注册到独立 `*http.ServeMux`。
   - 通过 `dashboard.APIv2.SetMemoryMux/SetRetrievalMux` 在 `MountGinRoutes` 中转发（dashboard 已在 `/api` 组下，故路径 `/api/v1/sessions/*`、`/api/v1/knowledge/*` 与 mux 路由一致）。
   - 前置：给 `memoryservice.Service` 补 `UpdateSession`（`service.go:127`）使其满足 `core.MemoryService`，并给 `APIv2` 加 `SetXMux` + `MountGinRoutes` 转发（已在 Phase 1 末完成）。
2. **`ares_eval/service` → HTTP（条件触发）**
   - `ServiceConfig` 增可选 `Postgres{Enabled,Host,Port,User,Password,Database,SSLMode}`（`config.go`）。
   - 仅当 `cfg.Postgres.Enabled`：`postgres.NewPool` → `NewPGEvalResultRepository(pool.GetDB(), pool.GetDB())` → `evalapi.NewService` → `evalapi.NewHandler` → `evalapi.RegisterRoutes(router, handler)` → `SetEvalMux`。
   - 健壮性：PG config 非法 / 池建连失败 / eval init 失败 / route 注册失败，任一环节**仅 warn 跳过 eval 接线**，不拖垮整个 service 启动（符合"不破坏现有路径"）。
   - PG 池存入 `s.pgPool`，`Stop` 时 `Close()`。
3. **`ares_experience/service` → 构造暴露**
   - `experience.NewRankingService()` + `experience.NewConflictResolver()` 构造并存入 `s.experienceRanking`/`s.experienceConflicts`。distillation 仍待 embedding client（无消费、无 HTTP，保持原状）。
4. **`storage/postgres/query` → 构造暴露**
   - `query.NewMemoryQueryCache()` 构造存入 `s.queryCache`（自带 cleanup goroutine），`Stop` 时 `Close()` 防泄漏。无独立 HTTP（与 memory/retrieval 的转发 mux 解耦，避免空转）。

> 验证：`go build ./internal/api_impl/... ./internal/dashboard/... ./internal/memoryservice/... ./internal/retrievalservice/... ./internal/ares_eval/... ./internal/ares_experience/... ./internal/storage/postgres/... ./cmd/ares/...` → exit 0。
> 注：本 Phase 不触及 `ares serve`（monitoring console）路径；新路由仅经 `ares start` 可达，与既定方案一致。


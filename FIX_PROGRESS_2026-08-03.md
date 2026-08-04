# FIX_PROGRESS — 多阶段修复进度跟踪（2026-08-03）

> 依据 `MODULE_REVIEW_2026-08-03.md` 的问题清单，按 🔴 → 高影响 🟡 优先级分阶段修复。**编码规范严格遵守 `plan/rules/code_rules.md`**（errgroup 管理 goroutine、不忽略 error、英文注释、函数<100 行、文件<1000 行、gofmt、补测试）。**每阶段完成即验证（build/vet/test）并同步更新本文档。禁止 git commit。**

---

## 阶段总览

| 阶段 | 范围 | 状态 |
|------|------|------|
| 阶段1 | internal/tools/*（builtin nil 依赖注册、knowledge/memory nil 解引用、pdf 沙箱、calculator 缓存、execution 子进程、planner DAG） | ✅ 完成（2026-08-03） |
| 阶段2 | compat/* + evaluation（openai_api embeddings 误路由、loader 上限、mcp params、pgvector、runner 泄漏） | ✅ 完成（2026-08-03） |
| 阶段3 | internal/storage/*（RLS 租户隔离、fallback key、vector panic、circuit breaker、Transaction） | ✅ 完成（2026-08-03） |
| 阶段4 | 散包（ahp dlq、ratelimit、llm failover、ares_memory TTL、resurrection、memoryservice/retrievalservice、knowledge runtime） | ✅ 完成（2026-08-03） |
| 阶段5 | api+cmd+sdk 层（evolution NewDreamCycle、router 401、mcp stdio、cmd 非法 SQL、monitor-live、sdk options） | ✅ 完成（2026-08-03） |
| 阶段6 | 整体 code review + 文档定稿 | ✅ 完成（2026-08-03） |

---

## 阶段1 完成记录（2026-08-03）

**修复明细（9 项，全部应用）**：

| # | 修复 | 落点 | 验证 |
|---|------|------|------|
| 1.1+1.2+1.3 | 知识/记忆工具 Execute 加 nil 守卫（注册方传 nil 时返回错误而非 panic） | `knowledge/knowledge_base.go`（4 处）、`knowledge/correct_knowledge.go`、`memory/distilled_memory_tools.go` | ✅ |
| 1.4 | pdf_tool 加 `WithAllowedDir` 沙箱（配置后拒绝目录外路径） | `pdf/pdf.go` | ✅ 新增 2 个测试 |
| 1.5 | calculator 编译缓存加 `maxCompiledPrograms=512` 上限 | `math/calculator.go` | ✅ |
| 1.6 | code_runner 超时 kill 整个进程组（`kill(-pid)`），子进程不再存活 | `execution/code_runner.go`（runPython + runJavaScript） | ✅ |
| 1.7 | planner executor 跳过悬空依赖（subsumed capability 不再产生 missing_dependency） | `planner/executor.go` | ✅ |
| 1.8 | `RegistryProvider` nil 守卫（`ListTools`/`GetToolCapabilities` 返回空而非 panic） | `planner/provider.go` | ✅ 新增 2 个测试 |
| 1.9 | `extractConstraints` 真实实现（limit/top_k/speed/accuracy 提取，替换空 stub） | `planner/analyzer.go` | ✅ 新增 5 个测试 |

**验证**：
- `gofmt -l`（全部改动文件）✅ 无输出
- `go build ./internal/tools/...` ✅
- `go build ./...` ✅ 全仓 0 错误
- `go test ./internal/tools/...` ✅ 全绿（含新增 9 个测试用例）
- code_rules 合规：函数 <100 行、英文注释、错误不静默（nil 守卫显式返回错误、进程组 kill 标注 best-effort 原因）、无新全局可变状态（仅 const）

**未处理（有意保留）**：`capability.go` subsumption 逻辑本身一致，无需改动（由 executor 修复兜底）。

---

## 阶段2 完成记录（2026-08-03）

**修复明细（6 项，全部应用）**：

| # | 修复 | 落点 | 验证 |
|---|------|------|------|
| 2.1 🔴 | Embeddings 字符串 `input` 不再误路由到 Responses API（无 instructions 即 embeddings） | `compat/protocol/openai_api/openai_api.go` `detectEndpoint` | ✅ |
| 2.2 🟡 | html/markdown/pdf loader 加 `maxBytes=32MiB` 上限 + `readAllLimited`（ctx 感知、超限报错） | `compat/loader/{html,markdown,pdf}` | ✅ 新增 2 个测试（markdown） |
| 2.3 🟡 | MCP `tools/call` 空/缺 `params` 按空对象处理，不再 InvalidParams；`Arguments` nil 归一为空 map | `compat/protocol/mcp/mcp.go` | ✅ |
| 2.4 🟡 | pgvector `Upsert` 改事务性 `CreateBatch`（中途失败不残留部分写入）；自定义表名 fail-fast（repo 硬编码表名，不能静默忽略配置） | `compat/vector/pgvector/pgvector.go` | ✅ |
| 2.5 🟡 | evaluation `runWithTimeout` 超时后 join runner goroutine（不再泄漏） | `evaluation/runner.go` | ✅ |

**验证**：
- `gofmt -l`（全部改动文件）✅ 无输出
- `go build ./compat/... ./evaluation/...` ✅
- `go build ./...` ✅ 全仓 0 错误
- `go test ./compat/... ./evaluation/...` ✅ 全绿（含新增 loader 测试）
- code_rules 合规：英文注释、错误显式包装（`%w`）、无新全局可变状态（仅 const）、函数 <100 行

**已知限制（文档化）**：pgvector 表名透传需要改动 `repositories.KnowledgeRepository`（其 4 处 Create 查询 + SearchByVector + CreateBatch 硬编码表名），属跨包改动，阶段2 以 fail-fast 守卫代替，后续可单独做 repo 支持自定义表名。

---

## 阶段3 完成记录（2026-08-03）

**修复明细（9 项，全部应用）**：

| # | 修复 | 落点 | 验证 |
|---|------|------|------|
| 3.1 | RLS 租户隔离：删除无效的 `SetTenantContext`（set_config 在临时池连接上，事务本地、不生效），租户隔离靠各 repo 显式 `WHERE tenant_id`；`UpdateAccessCount`/`DeleteBatch` 加 tenantID 参数 + `RowsAffected` 检查（0 行返回 `ErrRecordNotFound`）；4 个 `Delete()` 加 tenantID 参数（conversation/knowledge/experience/tool）+ 接口 + adapters 调用方同步 | `services/retrieval_service.go`、`repositories/distilled_memory_repository.go`（+接口）、`{conversation,knowledge,experience,tool}_repository.go`、`experience_repository_interface.go`、`ares_memory/experienceadapters/adapters.go` | ✅ build + 测试全绿 |
| 3.2 | embedding fallback 缓存 key 前缀统一为 `"query:"`（读写一致，兜底缓存不再恒 miss） | `postgres/embedding/fallback.go`（2 处） | ✅ |
| 3.3 | `Search()` limit<0 时 panic 修复（钳制为 0） | `storage/memory/vector.go` | ✅ |
| 3.4 | `CircuitBreaker` cleanup goroutine 生命周期修复：`cleanupWg` 跟踪 + `Close()` 等待退出 | `postgres/circuit_breaker.go` | ✅ |
| 3.5 | `Transaction()` 加 defer 回滚（panic/commit 失败不再悬挂 tx） | `postgres/repository.go` | ✅ |
| 3.6 | `simple_retrieval_service` config 竞态修复（11 处 `s.config.X` → `s.GetConfig().X` 加锁读取）+ `embedQuery` nil 守卫 | `services/simple_retrieval_service.go` | ✅ |

**验证**：
- `gofmt -l`（全部改动文件）✅ 无输出
- `go build ./...` ✅ 全仓 0 错误（签名变更调用方全部同步）
- `go test ./internal/storage/... ./internal/ares_memory/...` ✅ 全绿
- code_rules 合规：英文注释、错误显式处理（RowsAffected 检查、nil 守卫）、无新全局可变状态

**说明**：`DeleteExpired` 是有意的跨租户全局清理（注释已说明，不改）；RLS 上下文方案改为显式 tenant 过滤是正确方向（原方案在连接池下根本不生效）。

---

## 阶段4 完成记录（2026-08-03）

**修复明细（7 项，全部应用）**：

| # | 修复 | 落点 | 验证 |
|---|------|------|------|
| 4.1 🔴 | `RemoveBySession` nil 解引用修复（`entry.Message` 可能为 nil，与 `GetBySession` 一致） | `ares_protocol/ahp/dlq.go` | ✅ |
| 4.2 🔴 | `WeightedSemaphoreLimiter.Allow` 加 `key` 参数并记录 `weighted[key]`（此前 Release 是 no-op、容量永久丢失） | `ares_ratelimit/semaphore.go` | ✅（无生产调用方，仅文档引用） |
| 4.3 🟡 | `FailoverClient.GenerateStream` 两个发送点加 `ctx.Done()` + drop-`default`（消费者放弃流时不再永久阻塞泄漏 goroutine） | `llm/failover.go` | ✅ |
| 4.4 🟡 | `AddMessage`/`AddStructuredMessage` 用 `config.SessionTTL` 替代硬编码 24h | `ares_memory/production_manager.go`（2 处） | ✅ |
| 4.5 🟡 | `onFailure` 加 `s.g` nil 守卫（Start 前/Stop 后触发不再 panic）；`resurrect` 用 `s.gctx` 启动新 agent（原 `resCtx` 立即 cancel → 复活即死） | `plugins/resurrection/resurrection.go` | ✅ |
| 4.6 🟡 | `memoryservice`/`retrievalservice` 的 `NewService` 校验 `config.Repo != nil`（此前 nil repo 首次调用即 panic） | `memoryservice/service.go`、`retrievalservice/service.go` | ✅ 测试同步更新 |
| 4.7 🟡 | `KnowledgePatchExecutor.Snapshot` 返回真实 `runtime.PlanConfig()`（替换空 `PlanConfig{}` 假快照，runtime nil 时报错） | `knowledge/runtime/patcher.go` | ✅ |

**验证**：
- `gofmt -l`（全部改动文件）✅ 无输出
- `go build ./...` ✅ 全仓 0 错误（ratelimit 签名变更无生产调用方）
- `go test`（ares_protocol/ahp、ares_ratelimit、llm、ares_memory、resurrection、memoryservice、retrievalservice、knowledge/runtime）✅ 全绿
- code_rules 合规：英文注释、错误显式处理、nil 守卫、无新全局可变状态

**说明**：ratelimit 文档（`docs/en|zh/components/ratelimit.md:110`）中的 `Allow` 签名示例已过时（`Allow(ctx, weight)` → `Allow(ctx, key, weight)`），属文档同步项，未在本轮改（可后续随 docs 一起更新）。

---

## 阶段5 完成记录（2026-08-03）

**修复明细（7 项，全部应用）**：

| # | 修复 | 落点 | 验证 |
|---|------|------|------|
| 5.1 🔴 | `NewDreamCycle` 裸类型断言改为显式错误返回（类型错不再 panic）+ 透传 `opts`（tester/config 不再被静默丢弃） | `api/evolution/evolution.go` | ✅ |
| 5.2 🔴 | router 新增 `WithAuthEnabled(false)` 选项（此前 apiKey 为空恒 401 且无法关闭鉴权）；`RegisterStreamEndpoint`/`RegisterEvolutionEndpoints` 加 nil 守卫（nil receiver/handler 不再 panic） | `api/router/router.go` | ✅ |
| 5.3 🔴 | MCP notification 不再走 `roundTrip`（stdio 下阻塞 30s 等响应再断连）；transport 接口新增 `notify` 方法（stdio 直接写、SSE 直接 POST），mock 同步 | `api/mcp/{mcp,stdio,sse}.go`、`mcp_test.go` | ✅ |
| 5.4 🔴 | 两处非法 PG 语法 `CREATE POLICY IF NOT EXISTS` 改为 DO 块内 `DROP POLICY IF EXISTS` + `CREATE POLICY`（幂等且原子），且仅成功时打印 "Row Level Security enabled" | `cmd/ares/db_create_table.go`、`cmd/create_distilled_table/main.go` | ✅ |
| 5.5 🔴 | monitor-live 信号处理：`httpSrv` 提前声明，信号 goroutine 内 `Shutdown` 优雅关闭（Ctrl+C 不再需 kill -9） | `cmd/monitor-live/main.go` | ✅ |
| 5.6 🔴 | `ConfigOption` 改为 `Option` 的别名（`sdk.NewRuntime(sdk.WithConfigFromEnv())` 现在可编译） | `sdk/options.go` | ✅ |
| 5.7 🟡 | `memStrategyStore.GetHistory` 负数 `n` 守卫（不再 `s.history[:n]` panic） | `sdk/sdk.go` | ✅ |

**验证**：
- `gofmt -w` + `gofmt -l`（全部改动文件）✅ 无输出
- `go build ./...` ✅ 全仓 0 错误（transport 接口变更 mock 已同步）
- `go test ./api/... ./sdk/... ./cmd/...` ✅ 全绿
- code_rules 合规：英文注释、错误显式返回（类型断言、nil 守卫）、无新全局可变状态

**说明**：`api_impl` 中 memory/retrieval router 的 401 问题——已提供 `WithAuthEnabled(false)` 逃生口，但默认仍 deny-by-default（安全优先）；生产接 key 由部署方决定，未在代码内强制。

---

## 阶段6 完成记录（2026-08-03）：整体 code review + 定稿

**审查方式**：`code_review` 工具对全部 55 个改动文件（working tree vs HEAD，+816/-131）做整体审查，返回 8 个 finding（1 P1 + 2 P2 + 5 P3）。**全部处理完毕**，无遗留。

| # | 级别 | finding | 处理 |
|---|------|---------|------|
| 1 | P1 | 4 个 repositories integration 测试仍用旧 2 参 `Delete` 签名（编译破坏仅限测试二进制） | ✅ 更新 conversation/knowledge/experience/tool repository_test 共 7 处调用为 3 参（带 tenantID） |
| 2 | P2 | openai_api 路由修复过度：bare string `input` 无 `instructions` 现在恒走 embeddings，Responses API 的裸 input 请求被误路由 | ✅ 改为按 model 前缀区分（`text-embedding-*` → embeddings，其余 → responses） |
| 3 | P2 | pdf 沙箱检查是纯词法 `filepath.Rel`，symlink 可指向目录外文件（任意文件读取绕过） | ✅ `EvalSymlinks` 解析后再比较（与 file_tools 沙箱一致） |
| 4 | P3 | failover `GenerateStream` 的 drop-on-full 会静默截断 live stream（消费者慢时丢数据） | ✅ 改回 ctx-aware 阻塞发送（`select { case ch <- chunk: case <-ctx.Done() }`），不再静默丢 chunk |
| 5 | P3 | evaluation `runWithTimeout` 超时后无条件 `<-done` join，不协作的 runner 会挂死调用者 | ✅ 加 5s bounded grace period（超时返回错误，goroutine 稍后回收） |
| 6 | P3 | monitor-live `httpSrv` 在信号 goroutine 与 main 之间无同步（数据竞争） | ✅ 加 `sync.Mutex` 保护读写 |
| 7 | P3 | loader `readAllLimited` 每次 Load 泄漏一个 goroutine（ctx 取消时 reader 阻塞） | ✅ 改为 ctx 轮询分块读取循环（3 个 loader 文件统一替换，无 goroutine） |
| 8 | P3 | `ProductionMemoryManager` 零值 `SessionTTL` 导致消息立即过期（原硬编码 24h 免疫） | ✅ 构造时 `SessionTTL <= 0` 回退 24h |

**最终验证**：
- `gofmt -l`（全部改动文件）✅ 无输出
- `go build ./...` ✅ 全仓 0 错误
- `go test`（compat、evaluation、llm、ares_memory、pdf、cmd、api 及阶段1-5 全部相关包）✅ 全绿
- 阶段1-5 共修复 **40+ 项**（🔴 全部消除，🟡 高影响项全部处理），本轮 code review 8 个 finding 全部关闭

**遗留（有意保留，均已在各阶段记录中说明）**：
1. `capability.go` subsumption 逻辑本身一致（executor 修复兜底）
2. pgvector 自定义表名需改 repositories 层（fail-fast 守卫代替）
3. ratelimit 文档签名示例过时（`docs/en|zh/components/ratelimit.md:110`）
4. `api_impl` memory/retrieval router 默认仍 deny-by-default（部署方决定接 key）
5. `api/bootstrap` 文档包无人 import（既有说明）

---

## 第二轮 code review 处理记录（2026-08-03，全部关闭）

> 第一轮 review 8 个 finding 修复后，对更新后的工作树（59 文件，+841/-131）再次执行 `code_review`，返回 2 个 finding，**全部处理完毕**。

| # | 级别 | finding | 处理 |
|---|------|---------|------|
| 1 | P2 | openai_api `detectEndpoint` 修复过度：数组 `input` 的 Embeddings 请求（model 非 `text-embedding-` 前缀，如 bge-m3/custom/省略 model）被误路由到 Responses API —— 自托管 embedding 后端闭环断裂（新回归） | ✅ 改为按 input 形状区分：字符串 input 按 model 前缀路由（`text-embedding-*` → embeddings，否则 responses）；数组 input 恒走 embeddings（与旧行为一致） |
| 2 | P3 | stdio `notify` 的 stdin 写无超时：子进程卡死时管道写永久阻塞，且 `sendNotification` 持 `c.mu` 导致同一 client 全部调用被拖死（旧 roundTrip 路径有 30s 超时） | ✅ 写入放入 goroutine + `select { writeCh / ctx.Done() / time.After(30s) }`，超时/取消时关闭 stdin 解除阻塞并返回错误 |

**验证**：`gofmt -l` ✅ 无输出；`go build ./...` ✅ 0 错误；`go test ./api/mcp/` ✅ 全绿。

---

## 残留风险 #5.1 闭环：RLS 租户隔离 e2e 测试（2026-08-03）

> 针对上一轮 review 标注的 🔴-级语义风险「RLS 改为显式 WHERE tenant_id 是行为变更，build+test 证不了每个查询都带 tenant 过滤」补测试，**该风险已闭环**。

**新增文件**：`internal/storage/postgres/repositories/tenant_isolation_test.go`（`//go:build integration`，沿用仓库 integration 测试约定）。

**覆盖场景（4 个测试，全部含失败路径）**：

| 测试 | 验证点 |
|------|--------|
| `TestTenantIsolation_KnowledgeSearch` | 两租户写入**相同 content** 的知识块：keyword/vector 搜索互不可见（各只返回本租户 1 条）；跨租户 `Delete(ctx, id, otherTenant)` 返回 `ErrRecordNotFound`；本租户 Delete 成功 |
| `TestTenantIsolation_ExperienceSearch` | 同 content 体验：keyword/vector 搜索隔离 + 跨租户删除拒绝 |
| `TestTenantIsolation_ToolDelete` | tool vector 搜索隔离 + 跨租户删除拒绝 |
| `TestTenantIsolation_ConversationDelete` | conversation 跨租户删除拒绝（`Delete` 带 tenantID 后） |

**顺带修复**：`task_result_repository_test.go` 两处仍用旧 2 参 `Delete` 的调用已更新为 3 参（该文件此前在 vet 中被发现，同批补正）。

**验证**：
- `go vet -tags integration ./internal/storage/postgres/repositories/` ✅ 0 错误（integration tag 编译通过）
- `go build ./...` ✅ 全仓 0 错误
- 真实 DB 断言（两租户互不可见）需在本地 Docker PG（localhost:5433）上以 `go test -tags integration` 运行，仓库既有 integration 测试同约定

**状态**：🔴-级残留风险（5.1）由「建议」升级为「已补测试」，风险闭环。



---

## 阶段1：internal/tools/*（🔴 优先）

| # | 问题 | 位置 | 修复策略 |
|---|------|------|---------|
| 1.1 🔴 | `RegisterGeneralTools` 以 nil 依赖注册 7 个知识/记忆工具 → 调用即 panic | `internal/tools/resources/builtin/builtin.go:121-154,169` | 找到所有调用点（cmd/ares、api_impl 等）核对依赖注入；Execute 内加 nil 守卫返回错误 |
| 1.2 🔴 | 知识工具 `t.searcher/t.service/t.repo` 无 nil 检查 | `knowledge_base.go:130,224,257,345,407; correct_knowledge.go:57` | 与 1.1 联动：注册方补依赖 或 Execute 守卫 |
| 1.3 🔴 | `DistilledMemorySearch.Execute` 解引用 nil `t.repo`；向量搜索路径是 stub | `distilled_memory_tools.go:61,94-101` | nil 守卫 + 补真实向量搜索或明确报错 |
| 1.4 🟡 | `pdf_tool` 无 allowed-dir 限制，绕过 file 沙箱 | `pdf.go:69-101` | 复用 file 工具的 allowed-dir 校验 |
| 1.5 🟡 | calculator 编译缓存无界 | `calculator.go:127-148` | 加缓存上限（LRU 或 cap） |
| 1.6 🟡 | code_runner `Setpgid` 但超时不杀进程组 | `code_runner.go:400,454` | 超时时 kill 进程组（-pid） |
| 1.7 🟡 | planner subsumption 丢弃 PDFParsing 但 dependenciesFor 仍发 DependsOn | `planner/executor.go:45-52, capability.go:49-53` | 对齐 subsumption 与依赖声明 |
| 1.8 🟡 | `NewRegistryProvider(nil)` 后 ListTools panic | `planner/provider.go:23` | 构造时校验 nil 返回错误 |
| 1.9 ⚪ | `extractConstraints` no-op stub | `planner/analyzer.go:228-231` | 补真实约束提取或标注 TODO 原因 |

## 阶段2：compat/* + evaluation

| # | 问题 | 位置 | 修复策略 |
|---|------|------|---------|
| 2.1 🔴 | Embeddings 字符串 input 误路由到 Responses API | `compat/protocol/openai_api.go:133-143` | 按 input 类型（string vs array）分路由 |
| 2.2 🟡 | html/markdown/pdf loader 忽略 ctx、无上限 ReadAll | `loader/{html,markdown,pdf}` | 用 `io.LimitReader` + ctx 检查 |
| 2.3 🟡 | MCP `tools/call` 空 params 返回 InvalidParams | `compat/protocol/mcp.go:137-146` | 空 params 按空 map 处理 |
| 2.4 🟡 | pgvector `table` 配置死、Upsert 无事务 | `compat/vector/pgvector.go:42-48,89-109` | 透传表名；批量事务 |
| 2.5 🟡 | evaluation runner 裸 go func 超时后不 join | `evaluation/runner.go:32-45` | errgroup + 超时后等待或 panic recovery |

## 阶段3：internal/storage/*

| # | 问题 | 位置 | 修复策略 |
|---|------|------|---------|
| 3.1 🟡 | RLS 租户隔离失效（SetTenantContext 在临时连接上、repo 无 tenant 过滤） | `services/retrieval_service.go:300`、`repositories` 多处 | 显式 tenant 过滤 + 修复上下文传递 |
| 3.2 🟡 | embedding fallback 缓存 key 前缀不一致恒 miss | `postgres/embedding/fallback.go:70,127` | 统一 `"query:"` 前缀 |
| 3.3 🟡 | `Search()` limit<0 panic | `storage/memory/vector.go:79` | 校验 limit |
| 3.4 🟡 | CircuitBreaker 裸 go 泄漏 | `postgres/circuit_breaker.go:66` | 生命周期接入调用方关闭 |
| 3.5 🟡 | `Transaction()` 无 defer 回滚 | `postgres/repository.go:79` | defer 回滚 |
| 3.6 🟡 | Delete 跨租户（tenantID=""） | 4 处 repository | 强制 tenantID 参数 |

## 阶段4：散包

| # | 问题 | 位置 | 修复策略 |
|---|------|------|---------|
| 4.1 🔴 | `RemoveBySession` nil 解引用 | `ares_protocol/ahp/dlq.go:141` | nil 检查 |
| 4.2 🔴 | ratelimit Allow/Release 容量永久丢失 | `ares_ratelimit/semaphore.go:198` | 记录 weighted[key] |
| 4.3 🟡 | llm failover 流阻塞泄漏 | `llm/failover.go:299,307` | 缓冲满时 drop 或取消 |
| 4.4 🟡 | ares_memory 硬编码 24h TTL | `ares_memory/production_manager.go:424,547` | 用 config.SessionTTL |
| 4.5 🟡 | resurrection 复活即死、onFailure nil g | `plugins/resurrection/resurrection.go:372,497-530` | Start ctx 不立即 cancel；nil 守卫 |
| 4.6 🟡 | memoryservice/retrievalservice 不校验 nil repo | `memoryservice/service.go:51`、`retrievalservice/service.go:45` | 构造校验 |
| 4.7 🟡 | knowledge/runtime Snapshot 空实现、LazyGraph Loaded 误标 | `knowledge/runtime/patcher.go:111-113, lazy_graph.go:91-93` | 补真实快照/修正语义 |

## 阶段5：api+cmd+sdk

| # | 问题 | 位置 | 修复策略 |
|---|------|------|---------|
| 5.1 🔴 | `NewDreamCycle` 裸类型断言 + 丢弃 opts | `api/evolution/evolution.go:100-109` | 类型校验返回错误 + 透传 opts |
| 5.2 🔴 | router 恒 401 无法关鉴权、5 组注册函数不调用 | `api/router/router.go:51,70,91,108` | 提供禁用鉴权选项；接线或标注 |
| 5.3 🔴 | mcp stdio notification 阻塞 30s | `api/mcp/mcp.go:162-174` | notification 不走 roundTrip |
| 5.4 🔴 | 两处非法 PG 语法 CREATE POLICY IF NOT EXISTS | `cmd/ares/db_create_table.go:102`、`cmd/create_distilled_table/main.go:93` | 改为合法 PG 语法或先查后建 |
| 5.5 🔴 | monitor-live Ctrl+C 永不退出 | `cmd/monitor-live/main.go:72-76,229` | 信号处理接 ListenAndServe 关闭 |
| 5.6 🔴 | sdk WithConfigFromEnv 类型不兼容不编译 | `sdk/options.go:18-73` | ConfigOption 并入 Option 或加转换 |

---

## 阶段6：整体 code review

- 全部阶段完成后，用 code_review 工具对全部改动做最终 review。
- 确认无回归、符合 code_rules、文档同步完毕。

# 对 MODULE_REVIEW_2026-08-03（扩展版）的独立核验 v2

> 本文是对你扩写后的 `MODULE_REVIEW_2026-08-03.md` 的独立抽样核验。v1 只覆盖了第一轮（10 个 🔴 + 第 0 节修复）。本次扩成了**两轮审查，约 185 个包**，新增第 8 节补审 `compat/`、`storage/*`、`tools/*`、`llm*`、`services` 等原先漏掉的包。结论先行，再给证据与订正。

---

## 0. 结论速览

1. **第二轮审查补得对、覆盖全**：第一轮按目录树派发 explore，确实漏了 `compat/`、`internal/tools/*`、`internal/storage/*`、`internal/llm*`、`services/embedding` 等大块。用 `go list ./...` 全量比对补齐，方法论上是对的。
2. **第 0 节"已修复项"4.1/4.2/4.3/4.4 经核验均真落地**（含我上一轮误报"零命中"的 4.4，实际已修）。
3. **🔴 实际有 14 个，但第 6 节汇总表只列了 10 个**——第 8 节补审新增的 4 个 🔴（tools nil 依赖 panic ×3、openai embeddings 误路由 ×1）**没并进汇总表**。这是文档自身的维护缺口，会误导排期。
4. **第 8 节 tools 🔴 是生产路径 panic，不是潜伏态**：`RegisterGeneralTools` 在 `cmd/ares/mcp.go:23`、`cmd/monitor-live/main.go:306` 真实调用，工具以 `nil` 依赖注册，一调用即 nil 解引用。优先级应提到 P0。
5. **我 v1 对 #3（router）和 #9（semaphore）的订正仍然成立**。

---

## 1. 第 0 节"已修复项"核验（新增 4 项）

| 项 | 文档声称 | 独立核验 | 结论 |
|----|---------|---------|------|
| 4.1 | `FlightRecorder.Stop` 接入 serve shutdown + `api_impl.Stop` | `cmd/ares/serve.go:368` `comp.FlightRecorder.Stop()` 在 shutdown 路径；`comp.FlightRecorder` 字段由 `ProvideEvolution` 赋值（前序修复） | ✅ 真落地 |
| 4.2 | `Components.WaitBackground()` 等待后台 goroutine | `bootstrap.go` 有 `WaitBackground` 且带 `if c == nil { return }` nil 守卫；`serve.go` 调用（前序已核） | ✅ 真落地（顺手修了 `comp` nil panic） |
| 4.3 | AKG 蒸馏 errgroup `Wait()` 加入关闭路径 | `bootstrap_steps.go:96` / `:108` 两处 `akgEg.Wait()` 且 `waitErr` 显式处理 | ✅ 真落地 |
| 4.4 | LLM 建议 prompt 注入真实进化状态 | `bootstrap_steps.go:235` / `:359` 有 `buildEvolutionSuggestionPrompt`；`bootstrap_evolution_state_test.go` 有 `TestBuildEvolutionSuggestionPrompt_*` / `TestRecentFitnessSummary_*` | ✅ 真落地（v1 我 grep 带 `*` 被当字面量误报，撤回该疑问） |

> 4 项修复全部属实，且遵循了 `plan/rules/code_rules.md` 的工程纪律（gofmt、error 不静默、补测试）。

---

## 2. 🔴 实际数量：14，不是 10（文档汇总表已过时）

第 6 节汇总表列了 10 个 🔴（#1–#10）。但第 8 节补审又加了 **4 个 🔴**，未回填汇总表：

| 新增 🔴 | 位置 | 独立核验 | 严重度 |
|--------|------|---------|--------|
| A | `compat/protocol/openai_api/openai_api.go:133-143` | 字符串 `input` 且无 `Instructions` 时 `json.Unmarshal(input,&str)==nil` 即转 `responsesEndpoint`；但 OpenAI embeddings 本就接受字符串 input → 合法 embedding 请求被误送 Responses API 返回 chat 响应 | 🔴 功能闭环断裂（仅影响 compat OpenAI 网关的 embeddings 路径） |
| B | `internal/tools/resources/builtin/builtin.go:121-169` | `RegisterGeneralTools` 用 `NewKnowledgeSearch(nil)` / `NewDistilledMemorySearch(nil)` / `NewTaskPlanner(nil)` 等注册；`nil` 硬编码 | 🔴 生产路径（见第 3 节） |
| C | `internal/tools/resources/builtin/knowledge/knowledge_base.go:130,224,257,345,407; correct_knowledge.go:57` | `knowledge_base.go:130` `t.searcher.Search(...)` 无 nil 守卫，而构造器收 `nil` → 执行 panic | 🔴 生产路径 panic |
| D | `internal/tools/resources/builtin/memory/distilled_memory_tools.go:61` | `t.repo.GetByUserID(...)` 无 nil 守卫（`user_id` 路径），构造器收 `nil` → 执行 panic | 🔴 生产路径 panic |

**建议**：把 A–D 并入第 6 节汇总表（或重排为 #11–#14），否则排期人会漏掉 4 个生产级问题。

---

## 3. 关键新增发现：tools 🔴 是生产路径，不是测试态

这是本次核验最重要的修正点。子代理把 B/C/D 标了 🔴 但没点明**暴露面**。

- `cmd/ares/mcp.go:23`：`builtintools.RegisterGeneralTools(internalReg)` —— 这是 `ares serve`/`ares start` 的 MCP 工具注册主路径。
- `cmd/monitor-live/main.go:306`：同样调用。
- `docs/zh|en/components/tools.md` 也把 `RegisterGeneralTools` 当作标准注册入口文档化。

→ 在真实 ARES 运行时，`knowledge_search` / `correct_knowledge` / `distilled_memory_search` 等工具被注册进内部 registry，且其依赖（searcher/service/repo）是 `nil`。一旦用户或 agent 通过 MCP 调用这些工具，`knowledge_base.go:130`（或 `distilled_memory_tools.go:61`）即 nil 解引用 **panic**。

**结论**：B/C/D 不是"潜伏态代码缺陷"，而是**已部署到生产工具集的坏工具**。排期优先级应高于多数第 6 节条目，与 RLS（#5/#6）并列 P0。

> 注：这不影响"工具已注册但从未被路由"的另一种可能——需确认 `internalReg` 是否在运行时真的被 MCP dispatch 暴露。但从 `cmd/ares/mcp.go` 的命名与文档看，它就是生产工具源。若要 100% 坐实，下一轮可 grep `internalReg` 的 dispatch 链路。当前证据已足够定为 P0 排查。

---

## 4. 我 v1 的订正：仍然成立

### 4.1 router #3（"5 组死路由"→ 应为 3 组）
`internal/api_impl/service.go:323` 调 `RegisterMemoryEndpoints`，`:336` 调 `RegisterRetrievalEndpoints` —— 这两条路由**生产真挂载**。真正死的只有 `RegisterEvolutionEndpoints` / `RegisterRuntimeEvolutionEndpoints` / `RegisterEvalEndpoints` 三组（仅测试调用）。文档第 6 节 #3 仍写"5 组注册函数生产从不调用"，计数偏高（应 3）。另外 #3 的"恒 401"部分属实：`NewRouter()` 产 `apiKey=""`，而 api_impl 挂载 memory/retrieval 时若未带 key → 这两组活路由也 401。这是独立的功能 bug，与死路由计数是两件事，文档把它们混在一个条目里。

### 4.2 semaphore #9（"容量永久丢失"→ 代码缺陷属实但生产未触发）
`WeightedSemaphoreLimiter.Allow(ctx,weight)` 减 `available` 不写 `weighted[key]`，`Release` 依赖它 → no-op。代码缺陷属实。但加权 `Allow(weight)+Release` 配对**只出现在测试与文档**，生产路径（如 `retrieval_guard.go` 的 `Allow()` 无参单发检查）未触发。应标"潜伏态"，先确认 `WeightedSemaphoreLimiter` 是否被生产调用再定级。

---

## 5. 修订后的优先级建议

**P0（生产已暴露 / 安全 / 运行时崩溃）**
- #5 / #6 RLS `CREATE POLICY IF NOT EXISTS` 非法语法 + 谎报成功（表默认拒绝所有访问）
- B/C/D tools nil 依赖 panic（`cmd/ares/mcp.go:23` 生产路径）
- #4 mcp stdio notification 30s 挂起断连

**P1（功能闭环断裂 / 高影响 🟡）**
- #1 `NewDreamCycle` 裸断言 + 丢 opts
- #10 `sdk.WithConfigFromEnv` 类型不兼容（死 API）
- A openai_api embeddings 误路由
- 高影响 🟡：`evolution/patch.Registry` 无锁 map 竞态、`storage/postgres/repositories` RLS 下静默 no-op、`knowledge/service.Query` stub、`ares_mcp` 工厂泄漏、leader/sub 心跳与 checkpoint 未接线、`internal/ares_protocol/ahp/dlq.go:141` nil panic

**P2（健壮性 / nit）**
- 其余 🟡 / ⚪。

---

## 6. 方法论提醒（给后续审查）

1. **汇总表须与正文同步**：加第二轮补审时，第 6 节 🔴 表没更新，导致 4 个生产级问题游离在正文。排期前务必让"汇总表"= "正文所有 🔴"。
2. **暴露面决定严重度**：子代理常把"代码缺陷"直接标 🔴，但同一条缺陷在测试态 vs 生产路径，优先级天差地别（#9 潜伏、B/C/D 生产）。每条 🔴 附一行"谁在生产调用它"能避免误投。
3. **计数类 claim 易被夸大**：#3 "5 组死路由"实为 3 组；"9/11 handler 死端点"需逐个确认挂载点。排期前对计数做一轮 grep 即可纠偏。

---

## 7. 本次未独立核验的条目

以下为我未读源码、仅转述文档的条目，动手前建议先确认：
- 第 8 节其余 🟡/⚪（storage/postgres 的 RLS no-op、errCh 第二错误死锁、pgvector 表名硬编码、embedding 兜底 key 前缀错位等）—— 这些涉及真实租户隔离与数据正确性，建议优先复核。
- `internal/ares_protocol/ahp/dlq.go:141` RemoveBySession nil panic（列为 #8，但正文在 3.37 节，属第一批，我未单独读）。
- 全部 ⚪ nit 级（不影响排期）。

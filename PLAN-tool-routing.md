# 计划：工具面的按需披露与按任务路由

> 状态：草案（2026-10-19）。按 `plan/rules/code_rules_v2.md` 开发，每阶段以 `make check` 通过为交付条件。

## 1. 问题

LLM 每轮都会收到**全部工具 schema**（`planner_cognition.go` 里 `c.binder.GetToolSchemas()` 的结果直接展开成 `llmTools`）。工具越多：

- prompt 越大（schema 是纯 token 成本，且每轮重复）；
- 选择越容易错（候选集越大，选错概率越高）；
- 新增 MCP server 会**线性**放大前两项。

而仓库里已经具备按需披露的**先例**：skills 常驻块只放 `name + 一行描述`，全文按 ID 现取（`manager_impl.go` 的 `SetSkillsRegistry`）。

## 2. 已有零件（已回源码核实）

| 零件 | 位置 | 状态 |
|---|---|---|
| 工具 tag 字段 | `core.ToolSchema.Tags map[string]string`（`internal/tools/resources/core/tool.go`） | ✅ 存在，但**无人消费于广告** |
| 按 tag 查工具 | **`Registry.FindByTags`**（`core/registry.go:307`，`map[string]string`，**值支持 `"*"` 通配**，注释有示例） | ✅ 存在 |
| ⚠️ 同名陷阱 | `ToolRepository.ListByTags`（`internal/storage/postgres/repositories/tool_repository.go:692`，签名 `[]string, tenantID, limit`） | **是另一个 API、另一层**。按旧名去找会打开错文件 |
| 静态白名单 | `Registry.SetActiveTools`（`core/registry.go`） | ✅ 存在，但**零生产调用**，且是**全局字段** |
| **L1 工具类图**（`toolClassDAG`） | **构建**：`buildToolClassDAG(toolBinder.GetToolSchemas())`，`cmd/ares/serve_peer.go:538/645`；**存放**：`plannerCognition.l1`（`*engine.MutableDAG`）；**默认**：`enabled="true"` / `budget="0"` / `prior=""`（全放行、无先验） | ✅ 存在。**但它的 `enabled` 在 `growToolNodes` 里生效**（`planner_cognition.go:543`），即"选完之后拦下生长"，**不影响广告面** |
| MCP server 注册 | `ares_bootstrap/provide_mcp.go` | ✅ 有，但工具平铺，无 server→tool 层级 |

**两处已更正的错误**（错误都属于"范围给窄了就下结论"，记在这里防止重犯）：

1. 曾断言 `toolClassDAG` **不存在** —— 错。它存在且就是工具图（`cmd/ares/serve_peer.go`）。当时的 grep 只覆盖了 `internal/`，而 `cmd/` 是另一片地盘。
2. 曾把 `isToolEnabled` 的作用说成"planner 读它决定能不能用" —— 不准确。它在 **`growToolNodes`（选完之后）** 生效，语义是"**不生长节点**"，不是"不出现在提示词里"。

⇒ **广告面（模型看到什么）与生长门（选中后是否执行）是两条不同的缝**，本计划的分工就建立在这条区分上。

## 3. 设计决策（六条硬约束）

1. **在广告处过滤，不用 `SetActiveTools`。**
   `SetActiveTools` 改的是 registry 的全局字段，而 `drain` 会并发跑多个任务；两个任务先后设置会互相覆盖。更重要的是：过滤的**消费者是 planner**，在消费点过滤语义最直白，也不会因为异常路径没恢复而让后续任务"静默只看到几个工具"。

2. **默认空 = 全量，行为一字不变。**
   任何过滤都必须显式开启。未配置的部署保持现状。

3. **fail-open 且可见。**
   tag 命中 0 个工具时**退回全量**并记 Warn。错误的路由必须退化成"和今天一样"，而不是"能力消失"——本仓已多次踩过静默降级。

4. **过滤不是安全边界。**
   `ToolAllowlist` 仍是执行期护栏。过滤只用于减少 prompt 与选择面，**不得**成为阻止调用可用工具的机制。

5. **广告面与生长门各司其职，不互相替代。**
   - **广告面**（本计划的 `ToolTagFilter`）：决定模型**看得见什么**；影响 token 与选择错误率；
   - **生长门**（L1 `enabled`，`growToolNodes`）：决定**选中后是否执行**；影响策略与安全；
   - 只做生长门的后果是"模型选了 → planner 静默跳过 → 模型收到空结果"，**token 没省、轨迹更难读**。所以两者都要，但不要互相冒充。

6. **绝不按任务临时改写共享 L1 图。**
   `c.l1` 是**进程级共享**（planner 注释：*one shared planner serves every session*）。任务 A 把某 ToolClass 设 `enabled=false` 再用完恢复，会遇到：任务 B 被 A 的约束误伤（后写者赢）、以及**未恢复时全局永久收窄**。`NodeMetadata` 的锁只防数据竞争，**不防跨任务的语义干扰**。L1 只接受**长期策略**（GA 冷路径写），不接受 per-task toggling。

## 4. 阶段划分

### P1 · 广告处可选过滤（✅ 已完成 2026-10-19）

- `plannerCognition` 增加 `toolTagFilter map[string]string`（nil = 全量）；
- 新增 `advertisedToolSchemas()`：对 `binder.GetToolSchemas()` 按 `Tags` 过滤；命中 0 → 返回全量 + Warn；
- **只改广告点**（`ExecuteStep` 里构建 `llmTools` 的那一处）；
- `l1ToolClassID` / `l1Priors` **保持读全量**——它们用于 L1 键与 prior 字符串，过滤会让已禁用的工具"查不到"从而走 permissive 默认，绕过 `enabled=false`。

**验收**

- **一个**测试函数 `TestPlannerCognition_AdvertisedToolSchemas`（`planner_tool_filter_test.go:40`），**5 条断言**覆盖 4 组行为 + nil 边界：
  1. 无 filter → 全量（行为不变）；
  2. 命中 `domain=code_read` → 只剩 2 个；
  3. **未知 tag → fail-open 到全量**；
  4. nil binder → 空、不 panic；
- 反证：把 `if len(c.toolTagFilter) == 0` 改成 `if true` → 第 2 组失败。

### P1.5 · 让 L1 的 `enabled` 也作用于广告面（✅ 已完成）

**问题**：GA 把某个 ToolClass 关掉后，模型**仍然看得见它**、仍会选，然后被 `growToolNodes` 静默跳过——token 没省，还多了一次无效回合。

**改法**：`advertisedToolSchemas()` 同时应用 L1 `enabled=false`（判定复用 `isToolEnabled` 的语义：图缺失/节点缺失/字段缺失 = **放行**）。

**验收**

- 禁用某 ToolClass → 该工具**不在**广告集合里；未禁用 → 正常出现；
- L1 为 nil 或节点未知 → 全量（fail-open，与 `isToolEnabled` 一致）；
- 与 tag 过滤**叠加**：两者都无匹配 ⇒ fail-open 到全量；
- 反证：去掉 L1 判定 → "禁用后仍出现"。

### P2 · 任务打标 + 可观测（✅ 已完成：关键词打标、caps 写回 task payload、广告处 Info 日志含 caps/advertised/total）

**两条先定的约束（不写清就会白做）：**

- **入口必须是 per-quantum，不是 per-planner。**
  `toolTagFilter` 是**构造期字段**（`PlannerDeps.ToolTagFilter`），而 planner 全进程共享（§3.6）。P2 不能改这个共享字段，而应在**广告点**（`plannerCognition.ExecuteStep`，`planner_cognition.go:296` 起）**每次从 `task.Payload` 现算**子集——`ExecuteStep` 已经持有 `task`，读它不写共享状态。
- **tag 语义定为"布尔能力 flag + 并集"。**
  `ToolSchema.Tags` 是 `map[string]string`，**每个 key 只能有一个值**。若用 `domain=weather|code_read` 表达多域任务，`tagsMatch`（按 key 全匹配 AND）会永远匹配 0 个 → fail-open 回全量，P2 等于没做。因此：
  - 工具侧声明 `cap_weather: "true"`、`cap_code_read: "true"`（一个工具可带多个 flag）；
  - 任务侧得到一组 flag，**取并集**（任一命中即广告）；
  - 保持 `map[string]string` 不变，`FindByTags` 的 `"*"` 通配仍可用。

- 关键词快路（0 LLM）：`weather / code_read / http / git / shell / sql` 等；
- 可选 LLM 语义路（**默认关**，一次调用）；
- 打标结果写入 task payload + 事件，使"当时为什么只给了这几个工具"可回答；
- 每次广告记录 `tags` / 选中数 / 全量数 / 是否 fail-open。

### P3 · MCP 工具的能力来源（✅ 已完成，落点与计划不同）

**已知边界（必须写清）**：MCP 工具不带 `cap_*` tag（`mcpToolAdapter` 只有 `caps` 字段），因此**游离于 kernel 的 cap 路由之外**——对"主要靠 MCP 工具"的部署，按需披露基本不生效，只能靠 L1 `enabled` 单独管。这是设计选择，不是遗漏。

**降级说明**：分类只在**注册时**做一次（`sdk/sdk.go`），server 重连/描述更新后 caps 不刷新；计划原先写的"缓存 + 可刷新"未实现。

**落点修正**：MCP 工具**不经 kernel 的 `core.Registry`**，而是经 `sdk/sdk.go` 注册进 SDK 的工具表；因此 P3 做在 `mcpToolAdapter.Capabilities()`（原恒为 nil），由 `classifyMCPToolCapabilities` 在**注册时**按 `tools/list` 的 name+description 分类一次，映射到 planner 的宽类目（`capdef.go`）。kernel 侧走 `core.ToolSchema.Tags` + `cap_*`，**两套词表不互通**。

### P3-旧 · 倒排索引 + MCP 按需发现（未做，已被上面的落点取代）

- bootstrap 时用 `registry.GetSchemas()`（已带 `Tags`）建 `tag → toolNames`；
- MCP server 连接时读一次 `tools/list` 的 description 做分类并缓存（新增工具可刷新，无需重启）。

### P4 · 逃生口与质量反馈（✅ 逃生口已做；prior 改为"工具声明"）

- `need_tools(tag)`：LLM 在子集不足时可**主动**申请扩面；没有它，错误路由不可恢复；
- 之后再谈用 `l1.prior` 表达工具质量。**已核实的事实（2026-10-19）**：
  - L1 图由 `buildToolClassDAG` 建成后经 `SetToolClassDAG` 存入 `NewEvolutionComponents`，只经 `ToolClassDAG()` 被**读取**（`cmd/ares/peer_assembly.go:321` → planner）；
  - 生产里的两处 `SetNodeMetadata` 都**不写 L1**：`workflow_genome.go:324`（进化中的工作流 DAG）、`dag_patcher.go:198`（live 任务 DAG，即 L2）；
  - 因此 `enabled` 恒为 `"true"`、`budget` 恒为 `"0"`、`prior` 恒为 `""`——**`prior` 目前没有任何信号**；
  - `PatchSetNodeMetadata` 的注释（`patch.go:54`）说它"是 ToolStep 节点 enabled/budget/prior 的 action surface"，那是**设计意图**；要让它成真，需新增一个把 metadata patch 路由到 `toolClassDAG` 的 executor，**不能复用 live-DAG 的 graphExec**。

## 5. 不做的事

| 不做 | 原因 |
|---|---|
| 运行时让 LLM 给工具"现打 tag" | 同一工具两次不同 tag ⇒ 行为不可复现、无法回归。tag 声明在工具侧，任务侧才允许动态判定 |
| 用全局 `SetActiveTools` 做路由 | 并发覆盖；且异常路径会留下全局污染 |
| 让过滤承担安全职责 | 已有 `ToolAllowlist`，两套机制会互相掩盖 |
| 用"任务级改 L1 metadata + 用完恢复"做路由 | `c.l1` 进程级共享：并发任务互相干扰；未恢复即全局永久收窄（见 §3.6） |

## 6. 风险

| 风险 | 缓解 |
|---|---|
| 路由过窄，模型该用的工具看不到 | fail-open + P4 的 `need_tools` 逃生口 + 广告日志可查 |
| tag 声明与工具实际能力不符 | tag 由工具作者声明（已有 `WithToolTags`）；P2 起用真实调用结果校对 |
| 三处 `GetToolSchemas()` 被误改 | P1 只改广告点；L1 相关两处保持全量，并有注释说明原因 |

## 7. 交付标准

每个阶段：`gofmt` 干净、`make check` **exit 0**（0 issues / 0 单元 FAIL / anchors PASSED）、行为变更都有测试与反证、默认值保持原行为。

## 8. 勘误记录（本计划自身）

| 日期 | 错误 | 纠正 |
|---|---|---|
| 2026-10-19 | 断言 `toolClassDAG` 不存在（grep 只覆盖 `internal/`） | 它存在：`cmd/ares/serve_peer.go:538/645`，输入就是工具 schema |
| 2026-10-19 | 把 `isToolEnabled` 说成"planner 读它决定能否使用" | 它在 `growToolNodes` 中生效，是**选中后的生长门**，不改变广告面 |
| 2026-10-19 | 曾考虑用"task 级改 L1 + 恢复"实现路由 | 共享图 + 并发任务 ⇒ 干扰与"忘恢复即全局退化"；改为 per-planner 的 `ToolTagFilter` |

**教训（与仓库既有台账同一条）**：断言"不存在"之前，grep 必须覆盖 `internal/` 与 `cmd/`；断言"某字段在生效"之前，要找到**调用点**，而不是只找到字段定义。

## 9. 补记（2026-10-19，review 后追加）

**跨量子的另一半（容易漏）**：caps 写进 `task.Payload` **不等于**能活到下一个量子。checkpoint 重打包用的是**提交时**的 `meta.Payload`（`copyPayloadMap` 的防污染设计），executor 写入会被丢弃。真正让 caps 跨量子生效的是 `internal/kernel/scheduler_quantum.go` 的 **`carryTaskScopedPayload`**：它只把 task-scoped 白名单键（`taskScopedPayloadKeys`，目前只有 `tool_capabilities`）从 executor 侧覆盖回 envelope，quantum-scoped 键（如 `checkpoint`）仍然丢弃。测试在 `internal/kernel/scheduler_payload_test.go`，含**真实 encode/decode 往返**与反证。

只读 `applyNeedTools` 会以为"写进 payload 就生效了"，漏掉这半环。

**prior 的语义**：L1 的 `prior` 现在是**工具声明**（`tags["prior"]`）在建图时抄入，不是测量值；没有观测源不构成缺陷。

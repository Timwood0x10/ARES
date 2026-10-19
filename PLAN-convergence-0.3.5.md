# 收敛 0.3.5：把"能跑通"变成"能证明"

> 目标：agent OS 的成熟度不取决于功能数量，而取决于**每条关键路径都有证据**。
> 本计划只做三件事：补治理边界（MCP）、补端到端证据、补真实观测点。

## 1. 三个问题（均已回源码确认）

### 1.1 MCP 工具游离在 kernel 收窄之外

- `ProvideMCP`（`internal/ares_bootstrap/provide_mcp.go:203`）**自己 `core.NewRegistry()`**，把该 registry 交给 `MCPManager`；
- 而 kernel 的 `toolBinder` 读的是**另一个** registry（serve 装配里的 `internalReg`）；
- `SetupMCP`（`:222`）允许传入外部 registry，但**全仓零调用**（只有定义）。

⇒ MCP 工具因此**不进 kernel 的工具面**：既不参与 `cap_*` 收窄，也不受 L1 `enabled` 约束。
对 agent OS 而言这是结构性缺口：MCP 是工具增长最快的来源，却整个游离在策略之外。

### 1.2 路由链路只有单测证据

已有测试证明的：`advertisedToolSchemas` 的过滤逻辑、`toolCapabilitiesForTask` 的派生、
`carryTaskScopedPayload` 的往返、`need_tools` 的并集。

**没有**测试证明的是：**任务进来 → 广告真的收窄 → 工具真的执行 → 答案真的产出**，
在 `NewExecution` 的真实装配下成立。当前所有证据都是"零件对"，不是"整机对"。

### 1.3 两个评分输入恒为 0

`MetricsSnapshot.TotalRecoveries` / `Avg|Min|Max|LastRecoveryTime` / `DataConsistencyRate`
被 `CalculateScore` / `calcConsistency` 读取，但生产里**从不调用** `RecordRecovery` /
`RecordConsistency` ⇒ 恒 0，评分长期走启发式兜底。0.3.4 已用 `Unmeasured` 标注，
但**标注不等于修复**。

## 2. 现有零件（已核实，避免重复造）

| 零件 | 位置 | 状态 |
|---|---|---|
| MCP → `core.Tool` 适配 | `internal/runtime/protocol/mcp/`（`MCPManager` 持 registry） | 存在，但写入**私有** registry |
| MCP 描述 → 能力分类 | `sdk/mcp_capabilities.go`（`classifyMCPToolCapabilities`） | 存在，仅 SDK 路径使用 |
| kernel 工具面 | `internal/agents/sub/tools.go`（`toolBinder`） | 存在，只含 `internalReg` 的工具 |
| 广告过滤（三层 + fail-open） | `planner_cognition.go` `advertisedToolSchemas` | 存在，已测 |
| 恢复入口 | `aresrecovery/recovery.go:555` `RecoverFromAgentDeath` | 存在，返回复活数量 |
| 评分采集器 | `arena/service.go`（`MetricsCollector`） | 存在，但只被 `RecordActionResult` 喂 |
| 装配测试 | `internal/ares_bootstrap/experiment_log_wiring_test.go` | 存在，可作为 e2e 夹具参考 |

## 3. 设计决策（四条约束）

1. **不新增第二条工具面。** MCP 工具必须进入 `toolBinder` 读的那个 registry，而不是再造一个
   "MCP 专用广告路径"。否则又会变成两套语义。
2. **tag 在注册时确定，运行时不改。** 与 `ToolSchema.Tags` 既有契约一致：同一工具两次不同 tag
   ⇒ 不可复现、无法回归。
3. **观测点必须便宜且不阻塞热路径。** `RecordRecovery` 在复活完成处同步记一次时长；
   不允许为它引入额外 goroutine 或网络调用。
4. **端到端测试只断言"接缝"，不断言 LLM 文案。** 用脚本化 `ChatClient`，断言的是
   "广告集合 / 工具节点 / 答案节点"三件事，避免测试因提示词变化而脆断。

## 4. 阶段

### P1 · MCP 能力桥（治理边界）

**做法**

- 让 serve 侧 MCP 装配使用**同一个** `core.Registry`（`ProvideMCP` 接受外部 registry，
  而不是内部 `NewRegistry`）；`SetupMCP` 已是这个形状，把它变成唯一入口；
- MCP 工具注册时，用 `classifyMCPToolCapabilities(name, description)` 得到能力，
  写成 `cap_<name>: "true"` 的 tag（复用 `sdk/mcp_capabilities.go` 的分类器，不复制表）；
- 分类结果**缓存于注册时**，与描述一起变化（重连才刷新）。

**验收**

- 一个描述含 `http` 的 MCP 工具 → 注册后 `Tags` 含 `cap_network: "true"`；
- 任务带 `network` → 该 MCP 工具进广告集合；任务不带 → 不进；
- L1 对该 ToolClass `enabled=false` → 即使命中 cap 也不出现；
- 反证：去掉 tag 写入 → 第一条失败。

**风险**：MCP 工具名可能与内置工具冲突 → 注册需 handler 级的重名检测（已有 `Register` 错误）。
**不做**：不改变 MCP 协议层、不做"按需发现"（P3-旧 已作废）。

### P2 · 端到端链路验证

**做法**

- 在 `internal/agentruntime` 起一个最小 `Execution`：真实 fabric + 真实 registry（带 tag 的假工具）
  + 脚本化 `ChatClient`（第一次返回 tool call，第二次返回答案）；
- 断言三件事：
  1. 广告集合被收窄（通过 `AdvertisementSink` 或 binder 记录）；
  2. L2 图长出预期的 tool 节点；
  3. 产出 answer 节点且内容来自第二次模型回复；
- 再加一条**反证**：去掉 caps → 广告集合变全量（证明收窄真的发生在链路里，而不是测试夹具里）。

**验收**：测试在以上任一处接缝被切断时失败（三处各做一次 mutation）。

### P3 · 恢复观测点

**做法**

- 在 `RecoverFromAgentDeath`（`recovery.go:555`）成功复活处记录时长：起点是死亡检测时间，
  终点是复活完成；
- 通过一个**最小接口**（`RecoveryObserver`）注入 `MetricsCollector`，避免 `aresrecovery`
  反向依赖 `arena`；
- `DataConsistencyRate`：只在**有真实校验点**时记录（例如 checkpoint 解码失败计数），
  不造数；没有校验点就继续保持 `Unmeasured`。

**验收**

- 一次成功复活后 `snapshot.TotalRecoveries == 1` 且 `AvgRecoveryTime > 0`；
- `Unmeasured` 不再包含 `recovery_timing`；
- 反证：去掉记录调用 → 测试失败；
- **若找不到诚实的 consistency 来源**：明确记录"该输入仍无生产者"，不假装已修。

## 5. 不做的事

- 不新增 per-task 修改 L1 的路径（共享图 + 并发干扰，见 `PLAN-tool-routing.md` §3.6）；
- 不为 MCP 单独造一套广告逻辑；
- 不用"估算的恢复时长"充数——没有观测点就不写。

## 6. 风险

| 风险 | 缓解 |
|---|---|
| P1 改动 serve 装配，可能影响现有 MCP 行为 | 先加开关（默认沿用私有 registry），再切换；有回滚路径 |
| P2 的 `Execution` 依赖多，测试夹具重 | 复用 `experiment_log_wiring_test.go` 的构造方式；必要时抽一个 `newTestExecution` |
| P3 的死亡时间点不一定可得 | 若拿不到起点，只记"复活完成"事件而不记时长，并在 `Unmeasured` 里说明原因 |

## 7. 交付标准

- `make check` 通过（0 issues / 0 单元 FAIL / 锚点 OK）；
- 每个阶段至少 1 条新测试 + 1 次反证；
- 计划与本文件同步更新；台账（`docs/defects/00-INDEX.md`）记录判定与证据；
- 三处改动均可独立回滚（不互相依赖）。

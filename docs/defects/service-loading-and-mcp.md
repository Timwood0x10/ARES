# 缺陷与边界记录 · 服务发现 / MCP 加载 / 工具注册

> 本文档**只记"会咬你"的坑与已知边界**。
> 每条带源码锚点（v0.3.3 行号可能随 dev 分支漂移，定位以符号为准）。
> 性质标记：`缺陷`（建议修）/ `隐患`（规模/时间/并发下暴露）/ `取舍`（设计权衡，知边界即可）/ `已做`（有保护，确认非缺陷）。
>
> **一句话总纲**：三层（discovery 引擎 / MCP Manager / tools Registry）单看都"能用"，
> 但**工具名撞车是 first-wins（已有 warn，但 `winner` 标签误导）**、**一个 MCP 服务器挂了 = 它的工具静默消失（LLM 不知道）**、
> **discovery 默认关**、**health check 标了但没人消费**——"静默/半可见"是主要坑。

---

## S1 · 工具名撞车：first-wins，"我用的是谁家的 search" 无透明 〔缺陷 · 已部分缓解〕

两层各自处理：

| 层 | 撞名行为 | 锚点 |
|---|---|---|
| `Registry.Register` | **拒绝**：`ErrToolAlreadyRegistered`（`registry.go:106-109`，定义 `:368`） | 后到的不注册 |
| `MCP Manager.registerTools` | **跳过**：`registerTools skips name conflicts`（`manager.go:215-216` 注释 "existing entry"） | 先注册的赢 |

- **现状（v0.3.3 实测，比初稿好一点）**：撞名时 `registerTools` **已打 warn 日志**：
  `log.Warn("mcp: tool name conflict, keeping builtin tool and skipping MCP tool", ...)`
  （`manager.go:451-459`）。**不再是"完全静默"**。
- **但仍有一个坑**：该日志**恒写 `"winner":"builtin"`**——当冲突发生在**两个 MCP server 之间**时，
  真正赢家是另一个 server 而非 builtin，这条日志**具误导性**（排障时会看错"谁赢了"）。
- **坑（剩余）**：两个 MCP 服务器都暴露 `search`（或 `read_file`）→ **只有先注册的那个活下来**，
  后到的被 skip；LLM 调 `search` 时**不知道用的是 server-A 的**（capability 标签可能不同）；
  一个 server 先连、一个后连 → **连接顺序决定了谁的工具活着**。
- **建议**：修正日志里的 `winner` 字段（区分 builtin vs 另一个 server），或**加 per-server 前缀**
  （`mcp-serverA-search`）彻底消歧。

---

## S2 · MCP 服务器挂了 → 它的工具"静默消失"，LLM 不知道 〔缺陷 · 最该修〕

`MCP Manager.Start`（`manager.go:109`）：

```go
// manager.go:109-111（注意：初稿片段里的 `continue` 实际不存在，循环本身没有 abort，效果等价）
if err := m.ConnectServer(ctx, sc.Name); err != nil {
    log.Error("mcp: failed to connect to server", "server", sc.Name, "error", err)
}
```

- **坑**：server-X 连接失败 → **它的工具全没进 Registry** → LLM 看到"工具集里少了
  server-X 的那些"，**但 LLM 不知道自己少了什么**。
  - 如果 LLM 的 prompt 里已经见过 server-X 的工具（比如上一轮 session 里用它调过）→
    这轮 server-X 挂了 → LLM 说"我调 `server-x-search`" → **报 tool not found** → 困惑 / 多轮重试。
  - 没有"degradation notice"告诉 LLM "你的某工具因 server 故障暂不可用，别调它"。
- **RefreshTools 也类似**（`manager.go:189/303-348`）：刷新失败 `log.Warn`，**工具集其实会"失败即恢复"到上次好的那份**（`:312` 先 unregister 旧集，`:316-333` 失败时 re-register 上一份），净效果 = 保留上次好的；但也**没告诉 LLM"某些工具刷新失败了"**。（初稿写"工具列表不回滚"，与 `:316-333` 的实际恢复动作相反。）
- **建议**：一个 MCP 服务器连不上时，**注入一条系统提示**进 LLM 上下文（"以下工具当前
  不可用：[list]，原因是 server-X 连接失败"），别让 LLM 对着不存在的工具白调。
  或至少**在 Registry 里标"不可用"而非"不存在"**（两种状态对 LLM 的提示不同）。

**修复（2026-10-13，已核实 → 按"标不可用而非不存在"落地）**

- **实证**：连接失败时 `manager.go` 只 `log.Error`；工具不进 Registry，**无任何"缺失说明"**。
- **修法**：新增**每 server 一个可用性单元** `ServerHealth`（`mcp/health.go`，单源真相，含状态转移表）：
  连接失败 → `MarkUnavailable("connect failed: …")`；成功 → `MarkAvailable()`。
  `MCPServerStatus` 增 `health_note`，故 **"连不上"从"not connected"升级为可读的原因 + 起始时刻**。
- **模型侧**：可用性标注走**工具描述**（`MCPTool.Description` 遮蔽 `BaseTool.Description`）——
  工具列表本来就是模型读到的东西，所以"工具还在、但它的 server 挂了"这句话不需要新增 prompt 管道即可到达模型；
  被调用时返回**点名 server + 原因 + 起始时刻**的拒绝（非 raw "connection closed"）。
- **边界（未做）**：不主动往 LLM 上下文**注入系统提示**（那要动 planner 的 prompt 组装，属另一条路径）；
  本修的效果是"模型从工具列表就能看出不可用"，而非"被告知它少了什么"。

---

## S3 · discovery 引擎默认关 → 工具集是"启动时定死的" 〔取舍〕

`Discovery.Enabled` 默认 `false`（前面 §19.1 已确认）。

- **含义**：不开 discovery，工具集 = 启动时 MCP 配置里写的那些。**运行中新增/删除 MCP
  服务器不会自动被发现**（`FilesystemProvider` 扫 IDE 配置 + `SyncGitSources` 全不跑）。
- **对比**：开了 discovery → 周期扫 + health check + 事件 → 工具集**动态变**。
- **代价/边界**：生产默认关是**对的**（稳定优先），但要知道"你现在没有自动发现能力，
  工具集只随 MCP config 热加载（fsnotify）变，不随网络发现变"。
- **建议**：文档/README 写清"discovery 默认关，要开需要 `Discovery.Enabled: true` +
  配 provider"。别让人以为"系统会自动发现新 MCP 服务器"。

---

## S4 · health check 标了 `svc.Healthy` 但没人消费 〔隐患〕

`Engine.CheckHealth`（`internal/discovery/engine.go:236-279`）：

```go
status, err := e.health.CheckHealth(ctx, svc)
oldHealthy := svc.Healthy
svc.Healthy = status.Healthy   // ← 更新了（:256-257）
if oldHealthy != svc.Healthy {
    // 发 EventHealthChanged 事件（:267-275；事件名是 discovery.health.changed，
    // 定义在 internal/discovery/events.go:12，初稿写的 EventServiceHealthChanged 名称不符）
}
```

**坑（"标了但没人管"）**：
- health check 把 `svc.Healthy` 更新成 false 了，**但 MCP Manager 没有订阅
  `EventHealthChanged` 去"摘掉这个不健康服务的工具"**（对 `internal/runtime` 全包 grep 该事件 0 命中；唯一消费者是 `provide_discovery.go:62-64/95-108` 的桥接，只写 EventStore）。
- 即：一个 MCP 服务器**"半死"状态**（进程在、进程可 health-check 但服务慢/错）→
  discovery 标了 `Healthy=false`，但**工具还挂在 Registry 里**（没被摘）→
  LLM 仍然可以调它 → **调到慢的/错的 server 上**。
- **对比**："完全断连"有 S2 那条路（连不上→工具不进），但"半死"（在但慢）→
  **没有对应"摘工具"的机制**。
- **建议**：MCP Manager 订阅 `EventHealthChanged`（`discovery.health.changed`）：unhealthy → **临时摘该 server 的工具**
  （或标"降级"给 LLM），healthy → 恢复。

**修复（2026-10-13，已核实"真路径"后接线）**

- **先核实这是不是真路径**（不然就是死接线）：`internal/discovery/health.go` **确有** `MCPHealthChecker`
  （连上去 initialize → list_tools → close 探活），事件载荷 `Event.Service.Identity.Name` **就是 MCP server 名**。
  所以订阅是**真消费方**，不是空壳。discovery 默认关是 S3 那条已记录的取舍，与本修复无关。
- **修法**：`MCPManager.SetServerHealth(name, healthy, reason)`（unhealthy → 标不可用 + `notifyToolChange`，
  healthy → 恢复）；由 `SubscribeMCPHealthToDiscovery(engine, mgr)`（`provide_mcp.go`）订阅
  `EventHealthChanged` 并在 `bootstrap_builder.go` 装配。**这是"半死"（进程在但慢/错）唯一能看见它的信号** ——
  manager 自己的连接此时完全正常，所以复用该探测而非自造探活（§0.2）。
- **未管理服务不误报**：discovery 报告所有服务，非本 manager 的服务返回哨兵 `ErrServerNotManaged`，
  接线处显式跳过（**不是**静默忽略，是显式分支）。
- **选择"标不可用"而非"摘工具"**：摘掉会让 `healthy → 恢复` 变成"重新注册"，进而与 S1 的撞名/注册竞态纠缠；
  标记则是同一格子的翻转，且给出可诊断的拒绝（见 S5）。
- **测试**：`health_test.go` 的 `TestSetServerHealth_ConsumesExternalVerdict`（翻转生效 + 通知 + 外来服务被拒）。

**复核修正（2026-10-13 第二轮自审发现：join key 断在命名空间 —— 上面的"已修"当时是超前的）**

- **断裂**：discovery 用 **endpoint 派生身份**（`normalizeEndpoint`：命令 basename 或 `scheme://host/path`），
  manager 用 **运维手填的 config name**；`SetServerHealth(evt.Service.Identity.Name, …)` 直接拿一边的名字查另一边，
  中间**没有映射层**。于是自动发现的 MCP server → `knowsServer` 查不到 → `ErrServerNotManaged` → 被接线处
  `errors.Is` 当 "not mine" **静默丢弃**。只有当运维恰好把 config 的 name 写成与归一 endpoint 完全一致时才生效；
  这个隐式契约**无文档、无校验、无测试**。
- **被掩盖的原因（如实记录）**：`TestSetServerHealth_ConsumesExternalVerdict` 用的是**同名** config server，
  手工对齐的测试正好盖住了生产 join 的断裂；全仓没有"自动发现名 → 命中 config"的测试。
- **修法**：`discovery.ServiceKey(endpoint)` **导出**（join 复用同一条归一规则，而非在 MCP 侧重抄，避免漂移）；
  `MCPManager.ServerEndpoints()` 暴露每个受管 server 的 **transport endpoint（纯数据）**；
  `mcpHealthJoin.resolve` 按 **endpoint key → config name** 映射（配置热重载 → 每次事件重建表，避免陈旧表再次静默失配），
  并保留**直接同名**这条 join（运维按 endpoint 命名的情况仍然可用）。
- **未管理服务的处理（不再静默）**：先核实 discovery **只会产出 MCP 服务**（`binary.go` 有
  `if !metadata.isMCP { return }`，filesystem provider 只解析 MCP 客户端配置）→ 所以"对不上"必是**真·未管理的
  MCP server**，告警不是噪声。上报**每个身份一次**、并**上限 32 条**（超限时明确说"后续已抑制"，让沉默本身有解释）。
  管理侧仍只标记自己运行的 server —— "自动纳管发现的服务"属更大特性（S3/P2-14）。
- **测试（端到端，不再手工对齐）**：`provide_mcp_health_join_test.go` —— stdio 端点 join（`search` ↔ `/usr/local/bin/grep-mcp`）、
  SSE URL join、直接同名 join、未管理服务不误用且只报一次、超限抑制；断言走**公开 status API**
  （`ListServers().HealthNote`），即运维与模型实际读到的那份。

---

## S5 · 主动摘工具有 4 处，但"运行中 transport 自动断连"没有 〔缺陷 · 已坐实〕

`unregisterTools`（`manager.go:471`）只有 **4 个直接调用点**（grep 坐实）：

| 调用点 | 触发 | 摘不摘 |
|---|---|---|
| `Stop`（`:127`） | 全部停 | ✅ 全摘 |
| 热重连（`:224`） | 重连前先摘 stale | ✅ |
| `DisconnectServer`（`:286`） | operator 主动断 | ✅ |
| `RefreshTools`（`:312`） | 刷新工具集前先摘旧集 | ✅ |

> 注：hot-reload（`ApplyConfig`，`:522/:530`）是**经 `DisconnectServer` 的间接路径**，不是独立的第 5 个直接调用点（初稿把 `:312` 错标成 `DisconnectServer`：`DisconnectServer` 只在 `:286`，`:312` 是 `RefreshTools`）。

**但没有任何 transport 层（stdio 进程死 / streamable 断开）→ 自动 `unregisterTools` 的 wiring**。

- **坑（坐实）**：某个 MCP server **运行中自动断**（非 operator 主动断、非热加载、非重连）
  → **它的工具全留在 Registry 里**（没人摘）→ LLM 仍看到这些工具、仍会调
  → 调了才在 execute 时报 `connection refused` / "connection closed"（`client.go:321`）。
- **结果**：Registry 里**多出一堆"存在但调不通"的僵尸工具**——和 §dynamic-dag 的
  "L1 全拦静默提前交差"、§recovery 的"任务静默坐着"同族：**系统里静默的不一致**。
- **建议**：transport 层加 `onDisconnect` 回调 → 自动 `unregisterTools`（或标"不可用"），
  别让断连后的工具在 Registry 里变僵尸。

**修复（2026-10-13，已核实 → 已修，且实机反证）**

- **实证**：`client.go` 的 receiveLoop 退出分支只 `connected.Store(false)`（注释自称 "zombie that
  looks healthy"），**不发事件、不回调 manager**，故 4 个 `unregisterTools` 调用点全都不覆盖它。
- **修法（三层，一个单元格）**：
  1. `MCPClientConfig.OnDisconnect` + `Health`；transport 自行死亡（`ctx.Err() == nil` 才成立，
     即**不是** `Close()`/生命周期取消）→ `health.MarkUnavailable("transport closed: …")` + 回调；
  2. 回调由 `connectWithTransport` 提供 → 记一条**带 server 名**的 Warn + `notifyToolChange()`
     （消费者据此重新读工具列表）；
  3. `MCPTool.Execute` **先查可用性**：不可用则返回点名 server/原因/起始时刻的拒绝，
     `Description()` 在工具列表里标注 `[UNAVAILABLE: …]`。
- **为什么不 `unregisterTools`**：摘掉会把可诊断的"宕机"退化成 "tool not found"（正是 S2 抱怨的那种困惑），
  且恢复时要走重新注册（S1 撞名/注册竞态面）。标记 = 同一格子翻转，且**恢复免费**。
- **实机反证（规范 §7.1）**：临时停用 `MarkUnavailable(reason)` → `TestTransportDeathMarksServerUnavailable`
  **失败**（"a dead transport must mark the server unavailable"）；恢复后通过。非推断。
- **测试**：`health_test.go` 的 `TestTransportDeathMarksServerUnavailable`（真实 mock transport 关闭流）、
  `TestToolReportsUnavailableServer`（描述标注 + 拒绝点名）、`TestServerHealth_StateTransitions`
  （含"重复标记不重置 since 时钟"与"恢复必须清空说明"两条易错边）。

---

## S6 · 工具 schema 规模 vs LLM 上下文 〔取舍 · 已知边界〕

工具全量注册进 `Registry`（§27），**渐进披露**只"喂 LLM 相关子集"（§19）——但**Registry 本身
没有上限**。

- **坑**：20+ MCP 服务器 × 每服务器 10 工具 = 200+ tool 全进 Registry。
  对 Registry 没影响（内存 map），但对**渐进披露的"相关性筛选"**是挑战：
  200 个工具里找"和当前任务相关的 5 个"，**相关性判断本身变难**（envcap 检索也受规模影响）。
- **含义**：工具越多，**LLM 可选集越大 → 选择准确率可能反降**（"大 catalog 稀释"效应）。
  这是 RAG 同族问题（工具 = "可检索的知识"）。
- **建议**：工具集到一定规模后**做分组/裁剪**（按 capability 域、按优先级），别让 LLM
  面对全量 200 个工具做选择。

---

## 已做（确认非缺陷）

| 保护 | 现状 | 锚点 |
|---|---|---|
| 多 server 连接隔离（一个挂不连一个） | 循环无 abort（`:109-111` 记 `log.Error` 后自然进下一个 server；**无字面 `continue`**） | ✅ |
| 手动注册不被 discovery 误删 | #51：`hasRegisterRecord` → `removed` 分支专门保护（`engine.go:104-130`，定义 `:189-202`） | ✅ |
| 热重连先删旧再注册新 | `unregisterTools(stale)` before `registerTools(new)`（`manager.go:215-226`） | ✅ |
| Registry 拒绝重复 | `ErrToolAlreadyRegistered`（判定 `registry.go:106-109`，定义 `:368`） | ✅ |
| discovery 默认关 | 稳定优先 | ✅ |
| 事件桥接 | `forwardDiscoveryEvent`（added/removed/updated → EventStore） | ✅ |

---

## 性质小结

| 项 | 性质 | 优先级 |
|---|---|---|
| **S1 工具名撞车 first-wins** | 缺陷 | 中（已有 warn 但 winner 标签误导 / 加前缀消歧） |
| **S2 MCP 挂了工具静默消失 LLM 不知** | 缺陷 | 高（注入 degradation notice） |
| S4 health 标了没人消费 | 隐患 | 中（MCP Manager 订阅 health 事件摘工具） |
| **S5 transport 自动断连不摘工具** | 缺陷 | 中（transport 层加 onDisconnect → 自动 unregisterTools） |
| S3 discovery 默认关 | 取舍 | 知边界（文档写清） |
| S6 工具规模稀释 LLM 选择 | 取舍 | 知边界（大集做分组/裁剪） |
| 连接隔离 / 手动注册保护 / 热重连 / 去重 / 默认关 | ✅ 已做 | 无需动 |

---

## 面试串法（把"服务加载"讲对）

> "工具加载三层（discovery / MCP Manager / Registry）单看都能用，但**'静默'是主调**：
> ① **工具名撞车**（两个 server 都有 `search`）→ first-wins，后到的 skip（已有 warn 日志，但 `winner` 恒写 builtin、对 server-vs-server 冲突有误导）；
> ② **server 挂了** → 它的工具直接没进 Registry，LLM 不知道自己少了什么，会对着
> 不存在的工具白调；
> ③ **半死 server**（health 标了 `unhealthy` 但工具还挂着）→ 没人消费 health 事件摘工具；
> ④ **discovery 默认关** → 工具集启动时定死，不随网络动态发现。
> 这些和前面 '协作/工厂' 那份的 '静默'是一族——**整系统的'坏'都是静默的**（任务卡死、
> 工具消失、认知丢失、GA 漂），不是'响的'。要补的是**一族可观测/告警**。"

# ares 与 prime-agent 对比：借鉴分析报告

> 目的：确定 ares（goagent）需要增强的方向，借鉴 prime-agent 的成熟机制。
> 依据：双方本地源码（`/Users/scc/go/src/goagent` 与 `/Users/scc/go/src/prime-agent`），非臆想。
> 语言差异：ares 为 Go，prime-agent 为 TypeScript。对比聚焦**设计模式与可借鉴机制**。

---

## 〇、定位前提（重要）

**ares 是通用 agent 基础设施 / 基础框架（agent OS / runtime）**，不是某个具体应用（如 coding agent）。

证据（`api/ARCHITECTURE.md`、`ares-0.3.0-implementation-plan.md`）：
- 三层架构（Client/Service/Core/Internal）+ `sdk.New` 组合全部模块成 Runtime —— 平台层。
- 设计哲学：**运行时不自我修改，一切变更走离线候选管道**；**共享上下文 vs 不共享上下文（=线程 vs 进程）** 两种多 Agent 模式；**显式预算与终止条件**。

**由此建立的借鉴筛选标准**：只有**与应用场景无关、是 agent OS / runtime 平台层该提供的通用基础能力**才值得借鉴。prime-agent 是 coding 应用，其**具体实现**（IPython、git、Python skill 包）不能搬，但其背后的**通用模式**（budget、lease、state externalization、progressive disclosure、peer comm、snapshot）若属于运行时基础设施范畴，则可抽象借鉴。

下文每一节都标注该机制是【通用 agent OS 能力】还是【特定应用机制】。

---

## 一、四维度详细对比

> 定位标注：【通用】= agent OS / runtime 平台层应提供的通用能力（值得借鉴）；【特定】= 绑定 coding 应用的具体实现（只取抽象模式或不应搬）。

### 维度 1：自主进化 / 自我改进【通用】

| 对比项 | **prime-agent** | **ares (goagent)** |
|--------|----------------|-------------------|
| **核心机制** | Continual Harness（`refinement.ts`）+ `/refine` 在线小步进化 | 离线 GA（`internal/ares_evolution`）+ 运行时 DreamCycle + candidate 管线（`internal/evolution`） |
| **进化对象** | **补充状态外置**：supplemental prompt / memory / skill / subagent spec（`RefinementKind`） | **工作流结构**：DAG 拓扑 / 工具集 / prompt 参数（genome mutation/crossover） |
| **基座保护** | 不可变 base system prompt（`validateEdit` 拒绝改 base） | 不可变基座策略 + live-DAG 替换（`UpdateLiveDAG`） |
| **更新方式** | 小步证据更新：plan→apply→rollback，每 edit 带 reason | 遗传变异 + 选择压 + (1+λ) 进化 |
| **回滚** | 快照回滚（`rollbackProposal`，before/after 逆向重放，global 存 jsonl） | 策略级快照回滚（patch registry）+ canary 部署回滚 |
| **验证闸门** | 证据驱动 + `reviewAutoRefine` 自动触发闸门 | 三扇门验证（regression/verification/coverage，`regression.go`） |
| **冲突防护** | `baselineState` 并发写检测（kernel/多会话） | 分布式协调（coordinator 锁） |
| **范围** | local（session）+ global（跨会话）双 scope，merge | tenant 级隔离 |
| **ares 强项** | — | 结构演化（DAG/工具/参数系统性搜索）、多候选验证、canary 部署安全 |
| **ares 弱项** | — | **不改记忆/技能/上下文状态**，进化对象偏结构，缺"上下文在线小步进化" |

### 维度 2：Agent 通信 / 多智能体协作【通用】

| 对比项 | **prime-agent** | **ares (goagent)** |
|--------|----------------|-------------------|
| **通信模式** | **对等 + 程序化**：`rlm()` 子代理、`agent_message.send` 直接交换、retained subagents 对等网络 | **层级 leader-sub**：`internal/agents/leader` 管理 sub-agents |
| **子代理调用** | `await rlm(...)` 程序化 spawn，返回结果 | `leaderAgent.SendMessage` / Handoff 角色切换 |
| **消息路由** | agent 间直接发现 + 交换（不经用户/leader） | 经 leader 消息队列集中调度 |
| **上下文传递** | 子代理结果程序化返回，reusable subagent spec | Handoff 结构化上下文（不含原始消息体） |
| **恢复** | daemon 后台 + attach 重连保留 agent | `EventRecovery` 事件溯源恢复 leader 状态（含 failover） |
| **ares 强项** | — | 层级职责清晰、事件溯源可靠恢复、handoff 精简上下文 |
| **ares 弱项** | — | **全走 leader，单点**；子代理之间无法直接对等通信/发现 |

### 维度 3：省 Token / 上下文管理【通用】

| 对比项 | **prime-agent** | **ares (goagent)** |
|--------|----------------|-------------------|
| **压缩** | **`compaction.ts`**：文件操作跟踪（read/modified 集合）+ 分支摘要 + 压缩摘要，LLM 摘要化保留可复用上下文 | `internal/truncate`：仅 rune-safe 截断（`WithEllipsis`/`Plain`），**无智能摘要** |
| **状态外置** | **IPython 内核命名空间外置**（`state-snapshot.ts`：变量存内核，不进上下文）+ persistent goals | 无"运行时状态外置"；记忆走 `ares_memory`（蒸馏/清理） |
| **记忆/蒸馏** | Continual Harness memory + skill 复用 | `ares_memory` distillation（KeepBoth 冲突处理）+ knowledge 库 |
| **上下文清理** | `context-tree` + 压缩 | `ares_memory/context` cleaner |
| **目标持久化** | `/goal` persistent goals 跨轮保留 | 无显式 goal 持久化 |
| **ares 强项** | — | 结构化知识库 + 蒸馏 + RAG 检索 |
| **ares 弱项** | — | **`truncate` 太薄（无摘要压缩）**、无状态外置、无 goal 机制 |

### 维度 4：自愈 / 恢复【通用】

| 对比项 | **prime-agent** | **ares (goagent)** |
|--------|----------------|-------------------|
| **进程隔离** | **daemon/worker/kernel 三层**（`kernel/`），生命周期隔离 + 恢复 | 无进程级隔离（单进程），ares_runtime PluginBus |
| **状态快照** | **kernel 命名空间 dill 快照**（per-variable 容错 + 原子写 + 256MB 上限），resume 恢复 | 无运行时状态快照；workflow 有 CheckpointStore |
| **故障注入** | —（无 chaos） | ares_runtime `chaosWrappedAgent`（`manager_chaos.go`，生产活跃）故障注入边界 |
| **策略自愈** | — | `RecoveryPatchExecutor` 策略补丁（max_retries/backoff）+ `ApplyEmergency` 紧急自愈 + `RecoverStaleTasks` 孤儿恢复 |
| **会话恢复** | attach/reattach/resume + doctor 诊断修复 | EventRecovery 事件溯源重建 |
| **ares 强项** | — | 策略级自愈（自动改重试/回退）+ 事件溯源 + 混沌演练 |
| **ares 弱项** | — | **无进程隔离、无运行时状态快照**（长会话/内核崩溃会丢状态） |

---

## 二、ares 需要增强的四个方向

> 结论：**agent 通信、智能压缩、自主进化、进程隔离** 是 ares 相对 prime-agent 的主要短板，也是可互补借鉴的切入点。

### 方向 1：Agent 通信 —— 从"层级集中"到"对等 + 可发现"

**现状**：ares 的 `internal/agents/leader` 是严格的 leader-sub 层级。子代理之间不能直接通信/发现，所有交互经 leader 集中调度（`SendMessage` 队列 + Handoff）。

**可借鉴（prime-agent）**：
- `rlm(...)` 程序化子代理：子代理作为"函数调用"产生，结果程序化返回。
- retained subagents：可跨会话存活的保留子代理。
- 对等发现 + 直接消息交换：agent 之间可直接通信，不必全经 leader。

**增强思路**：
1. 在 ares 增加**子代理注册表/发现机制**（agent registry + 能力发现），让子代理可被其他 agent 发现。
2. 引入**对等消息通道**（peer-to-peer message bus），允许子代理间直接 `SendMessage`，leader 只做编排而非全量转发。
3. 支持**保留子代理**（跨会话存活），复用 prime-agent 的 retained subagent 模式。
4. 保留 ares 的层级优势（职责清晰）+ 事件溯源恢复，补上对等灵活性。

### 方向 2：智能压缩 —— 从"基础截断"到"摘要压缩 + 状态外置"

**现状**：ares 的 `internal/truncate` 只有 rune-safe 截断（`WithEllipsis`），无智能摘要、无状态外置、无目标持久化。

**可借鉴（prime-agent）**：
- `compaction.ts`：文件操作跟踪（read/modified 集合）+ 分支摘要 + 压缩摘要，用 LLM 把长上下文压成可复用摘要，而非粗暴截断。
- IPython 状态外置：大量运行时状态存内核（DB/内存），不进模型上下文。
- persistent goals：目标/进度跨轮持久化，避免重复描述。

**增强思路**：
1. 改造 `internal/truncate` → 引入 **`internal/compaction`**：实现文件操作跟踪 + LLM 摘要压缩管线（分支摘要、压缩摘要），替代粗暴截断。
2. 引入**状态外置**：长会话的中间变量/结果存 storage，不进上下文；在 workflow 上下文构建时按需检索。
3. 增加**持久化目标**（goal）机制：目标 + 进度跨轮保留，避免模型重复描述目标。
4. 与现有 `ares_memory`（蒸馏/清理）+ `knowledge`（RAG 检索）协同，形成"摘要压缩 + 检索增强 + 状态外置"的多级省 token 管线。

### 方向 3：自主进化 —— 补上"上下文/记忆/技能的小步在线进化"

**现状**：ares 的进化（`internal/ares_evolution` + `internal/evolution`）是**离线 GA + 运行时 (1+λ)**，进化对象是**结构**（DAG/工具/参数），三扇门验证 + canary 部署很成熟。但**不改记忆/技能/上下文状态**。

**可借鉴（prime-agent）**：
- Continual Harness：把记忆/技能描述/子代理 spec 存为持久补充状态。
- `/refine`：小步证据驱动更新（plan→apply→rollback），不可变基座 + baseline 冲突防护 + 双 scope（local/global）。

**增强思路**：
1. 在 ares 的 `ares_memory`/`knowledge` 之上引入 **Harness 式补充状态**（memory/skill/subagent spec），支持在线小步进化。
2. 实现 **refine 管线**：plan（LLM 产出 proposal）→ apply（带 baseline 冲突检测）→ rollback（before/after 快照逆向）。
3. 复用 ares 已有的**三扇门验证 + canary 部署 + patch registry**（比 prime-agent 更强），把"小步上下文进化"纳入同一验证/回滚体系。
4. 不可变基座：补充状态可改，base system prompt 不可改（ares 已有 live-DAG 不可变基座，扩展到记忆/知识）。

### 方向 4：进程隔离 —— 补上"运行时状态快照 + 崩溃恢复"

**现状**：ares 无进程级隔离、无运行时状态快照。长会话/内核崩溃会丢运行时状态。ares 有 `chaosWrappedAgent` 故障注入 + CheckpointStore + EventRecovery，但缺"运行时状态快照恢复"。

**可借鉴（prime-agent）**：
- daemon/worker/kernel 三层进程隔离：单点崩溃不影响整体。
- `state-snapshot.ts`：内核用户命名空间 dill 快照（per-variable 容错 + 原子写 + 256MB 上限），resume 时恢复。

**增强思路**：
1. 在 ares 增加**运行时状态快照**：长运行 agent/workflow 的关键状态（中间变量、结果、DAG 进度）定期快照到 storage，崩溃后恢复。
2. 结合现有 **CheckpointStore**（workflow 已用）+ **EventRecovery**（事件溯源）+ **state snapshot**（运行时变量），形成"事件溯源 + 检查点 + 状态快照"三级恢复。
3. 进程隔离是**大改动**（涉及架构），可先做**状态快照 + 恢复**（低风险、高价值），进程隔离作为后续演进。

---

## 三、建议实施优先级

| 优先级 | 方向 | 切入点 | 风险 | 价值 |
|--------|------|--------|------|------|
| **高** | 智能压缩 | 新增 `internal/compaction`（摘要压缩 + 状态外置 + goal） | 低（新增模块，不影响现有） | 高（直接降 token 成本） |
| **高** | 自主进化 | `ares_memory`/`knowledge` 上加 Harness 式小步进化 + refine 管线 | 中（复用现有验证/回滚体系） | 高（补上上下文进化短板） |
| **中** | Agent 通信 | leader 加对等发现 + 子代理注册表 + 保留子代理 | 中（改 agents 架构） | 中（提升多智能体灵活性） |
| **中** | 进程隔离 | 先做运行时状态快照 + 恢复，进程隔离后置 | 中（快照低风险，隔离大改动） | 中（增强长会话韧性） |

---

## 四、结论

ares 与 prime-agent 是**互补**的两套设计：
- **ares 强**：结构演化（GA + 三扇门 + canary）、策略级自愈 + 事件溯源 + 混沌演练、层级可控的 agent 管理、知识库/蒸馏/RAG。
- **prime-agent 强**：上下文/记忆/技能的小步在线进化（Continual Harness + /refine）、对等 agent 通信（rlm/retained subagents）、智能摘要压缩 + 状态外置 + 持久目标、进程隔离 + 运行时状态快照。

**ares 需增强的四方向**：agent 通信（对等化）、智能压缩（摘要+外置+goal）、自主进化（上下文小步进化）、进程隔离（状态快照+恢复）。其中智能压缩和自主进化切入点最清晰、风险最低，建议优先推进。

---

## 五、其他值得学习和完善的点（补充维度）

> 除前四维外，对 prime-agent 完整代码面（`src/core/*`、`docs/*`）进一步审查，找出 ares 尚未覆盖、可借鉴的机制。已用 grep 确认 ares 侧现状。

### 5.1 Skills 系统 —— 渐进式能力包披露（高优先）

| | prime-agent | ares 现状 |
|--|------------|-----------|
| 核心文件 | `src/core/skills.ts`（`loadSkills`/`formatSkillsForPrompt`/`SkillFrontmatter`） | 无（grep 0 命中） |
| 机制 | 多源发现（全局/项目/内置）→ 校验（name/description 规范）→ **渐进式披露**（仅 description 常驻 system prompt，完整 SKILL.md 靠 agent 按需加载）→ 注册 `/skill:name` 命令 → **Python 可执行能力包**（pyproject + editable 安装进持久 kernel）→ **skill-creator 自我创建闭环** | — |

**借鉴点**：
- **渐进式披露**：把能力"仅描述常驻 + 详情按需加载"应用到 ares 的 knowledge/tool 系统，进一步省 token。
- **可执行能力包**：能力不只描述，还能作为可执行包分发/安装。
- **自我创建闭环**：agent 能自己写并注册新 skill（类似 skill-creator），与 ares 的进化系统结合。

### 5.2 Autonomous 预算模型 + 质量门 + 快照指纹（高优先）

| | prime-agent | ares 现状 |
|--|------------|-----------|
| 核心文件 | `src/core/autonomous.ts`（`AgentAutonomousConfig`/`shouldAutonomouslyContinue`/`captureGitWorktreeSnapshot`） | 无（grep 0 命中） |
| 机制 | 无人值守运行：预算模型（`maxContinuations`/`maxTurns`/`maxTokens`/`timeoutMs`）→ **质量门**（gate 命令失败返回截断输出继续修）→ **git worktree 快照指纹**（工作区未变则不重跑失败 gate，避免无效重试）→ 预算刻意**排除 cache-read**（防缓存读耗尽预算）→ 终止理由分类（`missing_terminal_evidence`/`gate_failed`/`limit_reached`） | — |

**借鉴点**：
- **有界无人值守**：ares 的 `agentloop`/`dream_cycle` 需要类似的"有界自主执行 + 可证明完成"预算模型。
- **快照指纹防无效重试**：ares 的 `ares_arena` regression 重试可用"环境未变则不重跑"优化（与 `regression.go` 的提前停止互补）。
- **排除 cache-read 的 token 预算**：ares 的 LLM 调用预算应区分缓存读与真实生成。

### 5.3 Session Lease + 动作存储（中高优先）

| | prime-agent | ares 现状 |
|--|------------|-----------|
| 核心文件 | `src/core/session-lease.ts`（`SessionLease`/`acquireSessionLease`）、`src/core/session-action-store.ts`（`ActionStore`） | ares 有 `internal/storage/postgres/session.go`、`internal/core/models/session.go`（session 持久化），但**无租约（lease）并发控制**、**无动作存储（action store）** |
| 机制 | session 租约（并发控制，防多 daemon 同时改）+ 动作持久化（可回放/恢复的 action log） | — |

**借鉴点**：
- **Session 租约**：ares 长会话/多 daemon 并发时需租约防冲突（配合 coordinator 锁）。
- **动作存储**：记录 agent 的 action 序列，支持回放/审计/恢复（与 ares 的事件溯源互补）。

### 5.4 Output Guard（输出校验守卫）（中优先）

| | prime-agent | ares 现状 |
|--|------------|-----------|
| 核心文件 | `src/core/output-guard.ts` | 无（grep 0 命中） |
| 机制 | 在 agent 输出进入执行前校验（格式/安全/结构），不合格则拦截/修正 | — |

**借鉴点**：ares 的 agent 输出（工具调用/代码执行）可加结构校验守卫，防止脏输出进入执行（与 `internal/agents` 的输出处理协同）。

### 5.5 标准化外部协议（ACP/RPC/SDK）（中优先）

| | prime-agent | ares 现状 |
|--|------------|-----------|
| 核心文件 | `docs/acp.md`、`docs/rpc.md`、`docs/sdk.md`、`src/modes/`、`src/core/sdk.ts`（`defineTool`） | ares 有 `internal/ares_mcp`（MCP 集成）、`api/`、`sdk/`，但**无 Agent Client Protocol（ACP）**标准 |
| 机制 | 用标准 ACP/RPC 暴露 agent 能力，供 IDE/外部工具接入 | — |

**借鉴点**：ares 若需对接外部 IDE/工具，可评估引入 ACP 标准（当前 MCP 偏工具侧，ACP 偏 agent 侧）。

### 5.6 其他低优先但可参考的机制

| 机制 | prime-agent | ares 现状 | 借鉴点 |
|------|------------|-----------|--------|
| **Cron/心跳调度** | `src/core/cron-jobs.ts`、`src/core/rlm-heartbeat.ts`（daemon 定时任务 + 心跳） | ares 有 `internal/workflow/scheduler.go`（工作流调度），但**无 daemon 级定时 agent 任务** | 定时触发 agent 任务（如定时巡检） |
| **Agent 级追踪** | `src/core/agent-traces.ts` | ares 有 `internal/ares_observability`（OTel，基础设施级）+ `internal/ares_flight`（飞行记录） | ares 的 OTel 更标准，此点 ares 已有，不必借鉴 |
| **Side question** | `src/core/side-question.ts`（主任务外插问） | ares 无 | 异步插问，低优先 |
| **Bash 沙箱** | `src/core/bash-executor.ts` | ares 有 `internal/tools`（含 shell 工具） | ares 已有类似，不需借鉴 |

### 5.7 汇总：新增借鉴清单

| 优先级 | 机制 | ares 落地建议 |
|--------|------|--------------|
| 高 | Skills 渐进式披露 + 能力包 + 自我创建 | 扩展 `internal/knowledge`/`internal/tools`，加"仅描述常驻 + 详情按需加载" + skill-creator |
| 高 | Autonomous 预算模型 + 质量门 + 快照指纹 | 给 `internal/agentloop`/`internal/ares_evolution` 加有界自主执行预算 + 环境指纹防无效重试 |
| 中高 | Session Lease + 动作存储 | 在 `internal/storage`/`internal/agents` 加租约并发控制 + action log |
| 中 | Output Guard | 在 `internal/agents` 加输出结构校验守卫 |
| 中 | ACP 标准 | 评估 `internal/ares_mcp` 之外的 agent 侧标准协议 |
| 中低 | Daemon 级定时任务 | 在 `cmd/ares` 加 cron/heartbeat 定时 agent |
| 低 | Side question | 异步插问机制 |

**关键结论**：除前四维（agent通信/智能压缩/自主进化/进程隔离）外，**Skills 渐进式披露**和 **Autonomous 有界自主执行 + 快照指纹防无效重试** 是另外两个高价值借鉴点。其中"快照指纹防无效重试"可以直接改进 ares 现有的 `ares_arena` regression 和 `agentloop` 迭代逻辑，风险低、收益明确。

---

### 5.8 工具按需发现/加载（本机工具发现 + 实时规划）【通用】

> 用户新提借鉴点：本机工具有 `--help`/自描述，可**运行时发现 + 按需加载**，agent 实时按任务规划工具调用，而不是把所有工具描述全量塞进上下文。

#### prime-agent 的机制（本地代码）

| 机制 | 位置 | 说明 |
|------|------|------|
| **活跃工具子集** | `agent-session.ts:4109` `setActiveToolsByName`、`getActiveToolNames` | 只把**当前活跃工具**暴露给模型，非全量 |
| **全量 vs 活跃分离** | `getAllTools()` / `getActiveTools()`（`extensions/types.ts:1160-1166`） | 全量表（含 schema/描述）与活跃子集分离 |
| **子代理工具过滤** | `system-prompt.ts:120` `tools.filter(...)` | 子代理 doctrine 只注入其活跃工具（如 `ipython`/`bash`/`edit`） |
| **运行时按需激活** | `agent-session.ts:2049` | 按需把 `ipython` 加入活跃集并**重建 system prompt** |
| **动态工具发现** | `extensions/loader.ts` `discoverExtensionsInDir`/`discoverAndLoadExtensions` | 从项目本地/全局/配置多来源发现扩展并动态 `registerTool` |

#### ares 现状（已核查）

| 项 | ares 现状 | 差距 |
|----|----------|------|
| 工具注册 | `internal/tools/resources/core/registry.go`（静态 `Register`/`Get`/`List`/`Execute`） | 无运行时发现 |
| 工具→LLM | `registry.go:228` `GetLLMTools()` **一次性把全部工具转 LLM tool** | **无活跃工具子集，全量塞上下文** |
| 工具描述 | `ToolSchema`（name/desc/category/parameters/tags） | 无"仅描述索引常驻 + 详情按需加载"的渐进式披露 |
| 本机工具发现 | 无 | 无探测 `--help`/man 动态注册 |
| 工具选择/规划 | `internal/agents/planner`（基于规则 + fallback） | 偏规则，未对接按需工具加载 |

#### 借鉴点（agent OS 通用能力）

1. **活跃工具子集（Active Tools）**：`Registry` 增加 `SetActiveTools(names)`/`GetLLMTools()` 改为只返回活跃子集。agent 可运行时切换活跃工具并重建上下文。
2. **本机工具发现**：增加发现器，探测本机命令（`command -v` + `--help` 解析），动态注册为工具（带 SSRF/安全边界，参考 `internal/tools/web_search.go` 的 allowlist 模式）。
3. **渐进式披露**：工具只注入"描述 + 参数概要"常驻，完整 schema 按 agent 请求按需加载。
4. **工具规划联动**：`internal/agents/planner` 的规划结果映射到"活跃工具集"，让 agent 实时按任务规划工具调用。

**这与用户描述完全一致**：本机 `xxx --help` 探测 → 动态注册；agent 根据任务实时规划要用的工具 → 只暴露活跃子集。属于 agent OS 平台层的通用能力（与应用无关，任何场景都适用）。风险低（纯新增，不破坏现有 `GetLLMTools` 全量路径，可先做活跃子集再叠加发现）。

---

## 六、定位视角下的最终筛选（agent OS / runtime 视角）

> 以"ares 是通用 agent 基础设施"为前提，对上述所有借鉴点做**最终过滤**：
> **【通用】= agent OS 平台层应提供的通用能力 → 值得借鉴（剥离具体实现后落地）**
> **【特定】= 绑定 coding 应用 → 不搬具体实现，只取其通用抽象模式**

### 6.1 最终筛选表

| 借鉴点 | 定位 | 判断依据 | 处置 |
|--------|------|---------|------|
| **自主进化（Continual Harness + /refine）** | 【通用】 | 自改进是 agent OS 的运行时能力；ares 0.3.0 已有"在线/离线分离 + 候选管道"，prime-agent 的"小步上下文进化 + 双 scope"是同类通用能力 | ✅ 借鉴（抽象为 Harness 式进化原语） |
| **Agent 对等通信 + 保留子代理** | 【通用】 | 多 Agent 协作是 agent OS 基础；ares 当前用"共享上下文/Handoff（=线程模式）"，对等+保留子代理是"进程模式"的补充 | ✅ 借鉴（作为多 Agent 的第二种模式） |
| **智能压缩 + 状态外置 + goal** | 【通用】 | 上下文管理是 agent OS 核心；`compaction`/`state externalization`/`persistent goals` 是通用模式 | ✅ 借鉴（剥离 IPython 实现） |
| **进程隔离 + 状态快照** | 【通用】 | 运行时韧性与恢复是 agent OS 平台能力 | ✅ 借鉴（抽象为状态快照原语，进程隔离后置） |
| **有界自主执行预算 + 终止条件** | 【通用】 | 对应 ares 0.3.0 第 10 章"循环失控"解法（显式预算+终止），是 agent OS 的自主执行控制 | ✅ 借鉴（budget/termination 原语） |
| **Session Lease + 动作存储** | 【通用】 | 并发控制与可恢复性是 agent OS 平台能力 | ✅ 借鉴（租约 + action log 原语） |
| **Output Guard（框架级输出校验）** | 【通用】 | 输出契约是 agent OS 边界校验 | ✅ 借鉴（抽象为框架级输出校验） |
| **渐进式披露（能力描述常驻 + 详情按需加载）** | 【通用】 | 上下文按需加载是通用省 token 模式 | ✅ 借鉴（抽象模式；剥离 Python 包） |
| **环境指纹防无效重试** | 【通用】 | 防无效重试是通用机制（有副作用环境都适用） | ✅ 借鉴（抽象为"环境未变不重跑"，剥离 git 特定） |
| **工具按需发现/加载（Active Tools + 本机发现）** | 【通用】 | 工具注册表 + 活跃子集 + 运行时发现是平台层能力 | ✅ 借鉴（`Registry.SetActiveTools` + 本机命令探测，见 5.8） |
| **Skills 系统（Python 可执行包 + pyproject + skill-creator）** | 【特定】 | 绑定 coding/REPL（editable install 进 IPython 内核） | ⚠️ 只取"渐进式披露 + 自我创建"抽象，**不搬 Python 包分发** |
| **IPython 内核状态外置** | 【特定】 | 绑定 REPL 运行时 | ⚠️ 只取"状态外置"抽象，落地为"运行时变量存 storage 不进上下文" |
| **git worktree 快照指纹** | 【特定】 | 绑定 git/工作区 | ⚠️ 只取"环境指纹"抽象，落地为通用 side-effect 检测 |
| **Bash executor / coding 工具** | 【特定】 | 绑定 coding 场景 | ❌ 不借鉴（ares 已有 tools） |
| **ACP / RPC 协议** | 【通用】 | 对外标准协议属于平台层接口 | ◻️ 可选（ares 已有 MCP，ACP 偏 agent 侧，按需评估） |

### 6.2 agent OS 视角下的最终结论

ares 作为**通用 agent OS / runtime**，真正值得借鉴的是以下 **7 个通用原语**（全部剥离 prime-agent 的 coding 具体实现）：

| # | 通用原语 | 借鉴自 | 在 ares 的落点 |
|---|---------|--------|---------------|
| 1 | **Harness 式小步进化**（补充状态外置 + 双 scope） | Continual Harness | `internal/ares_evolution`/`internal/ares_memory` |
| 2 | **对等多 Agent 通信**（peer 发现 + 保留子代理） | retained subagents | `internal/agents` |
| 3 | **智能上下文压缩**（摘要 + 状态外置 + goal） | compaction/state-snapshot | 新增 `internal/compaction` |
| 4 | **有界自主执行 + 环境指纹防无效重试** | autonomous | `internal/agentloop`/`internal/ares_arena` |
| 5 | **运行时状态快照 + 恢复**（租约 + 动作存储） | kernel snapshot/session-lease | `internal/storage`/`internal/workflow` |
| 6 | **框架级输出校验**（渐进式披露） | output-guard/skills | `internal/agents`/`internal/knowledge` |
| 7 | **工具按需发现/加载**（Active Tools 子集 + 本机 `--help` 发现 + 实时规划） | agent-session/extensions/loader | `internal/tools` + 新增 discovery（见 5.8） |

**不借鉴**：Python skill 包分发、IPython 外置、git worktree、coding 工具（绑定应用的实现不应进入 agent OS 平台层）。

**核心原则**：agent OS 平台层只应包含**与应用无关的通用原语**；具体应用（coding/REPL/特定工具）的实现属于上层应用，不进 ares 核心。这也符合 ares 0.3.0 的设计哲学——**运行时稳定，能力以原语方式提供，应用通过组合这些原语构建**。

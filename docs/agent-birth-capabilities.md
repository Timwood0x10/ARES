# Agent 出生自带能力（Capability Inventory）

> 更新日期：2026-09-13（v0.3.1）
> 范围：`ares serve` 启动装配 peer agent 时（`cmd/ares/peer_assembly.go` + `cmd/ares/serve_wiring.go` + `internal/agentruntime` + `internal/runtime/memory`）**自动注入**、无需额外配置即可使用的能力。不含运行时按需加载的能力（如 Skill 激活后的 MCP 连接、按需拉取的 SKILL.md）。
> 对照：英文版见 [agent-birth-capabilities.en.md](agent-birth-capabilities.en.md)。

---

## 一、总览（四层）

| 层 | 职责 | 代表能力 |
|----|------|----------|
| 架构层 | 执行骨架 | 共享 L2 执行内核 / 内核调度器 / 任务织物状态机 / 输出守卫 |
| 能力层 | 知识技能 | SkillCatalog 全家桶（多源索引 / FTS5 / Experience / MCP 懒连接） |
| 原语层 | Agent OS 原语 | peer 直连注册表（发现契约）/ 会话租约 / 上下文清理 |
| 控制层 | 运行控制 | 预算 / GA 策略源 / 进化反馈 / 沙箱工具 |

> v0.3.x 变更：Leader/Sub 执行模型已删除。出生能力中的 leader 任务规划器、AHP 消息队列、ActionLog 审计路径均随之下线（`internal/agents/sub/agent.go` 注释记录了这些删除——peer 直连通道在生产中一直为 nil 队列，ActionLog 无生产构造点）。

---

## 二、执行与验证（共享 L2 内核）

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 共享执行内核 | `agentruntime.NewExecution`（`peer_assembly.go` 装配） | serve 与 SDK 共用：会话注册表 + 计划投影编译 + 路由认知 + 提交器（v0.3.1 convergence） |
| 任务状态机 | `internal/fabric/task` | Create/Acquire/Yield/Complete/Checkpoint + DAG 依赖 + epoch 隔离 + 租约 |
| 内核调度器 | `internal/kernel` | 能力评分、drain 循环、租约心跳、优先级抢占、恢复提名 |
| 输出守卫 | `outputguard.NewGuard().ValidateResult`（sub 事件路径） | 拒绝结构不一致的 agent 结果 |
| Prompt 记忆增强 | `resolveServePromptEnricher` → `Submitter`（v0.3.1） | serve 提交路径注入跨轮会话记忆（fail-open） |
| 跨重启恢复 | `fabric.RestoreFromStore`（PG 模式，v0.3.1） | 进程崩溃后任务带 checkpoint 回折续跑 |

---

## 三、知识 / 技能（Capability Fabric）

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 常驻技能块 | `skills_wiring` → `SetSkillsRegistry` → `BuildContext` | 出生即带 Level-0 metadata（name + 一句话描述），SKILL.md 按需加载（渐进披露） |
| 多源技能索引 | config `[[skill_sources]]`：project / user / registered / git / http / oci | 只扫声明源，零全盘扫描 |
| FTS5 全文检索 | `FTS5Index`（Discovery 优先 FTS5，失败回退关键词匹配） | 检索排序 |
| 技能目录工具 | `ares_skills.CatalogTools`（serve_wiring） | skill_search / skill_load 等注册为一等工具，LLM 自主驱动渐进披露 |
| Experience 持久化 | Experience store（skill outcome writer 挂终端事件流，v0.3.1 闭环） | task 终态 → {capability → 成功率} 先验写回 |
| MCP 懒连接 | `SetMCPConnector(comp.MCP)` | 仅 Skill 激活时连接声明的 MCP server（`Catalog.Activate`） |
| listChanged 增量重索引 | `MCPManager.SetToolChangeHandler` → `Catalog.Refresh` | MCP tools/listChanged 触发 hash 增量重索引 |
| 变更检测 | `DetectIndexChanges` + `Catalog.Refresh` | 按 ID+Source+Hash 分类 Added / Modified / Removed |

---

## 四、工具集

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 内置工具 | `api_tools.RegisterBuiltinTools(WithFileSandboxDir)` | web_search / calculator / regex / json / file 等（沙箱目录限定） |
| 本机命令 | `registerNativeTools`（`ARES_NATIVE_TOOLS` allowlist） | 仅 allowlist 内命令经 `command -v` + `--help` 探测后暴露 |
| MCP 工具 | `setupMCP` → `internalReg` | 已连接 server 的 tools/list 注册进工具注册表 |
| 能力检索工具 | `registerCapabilitySearch` | tools / skills / commands 统一检索（envcap） |
| 规划器桥接 | `newPlannerBridge(internalReg)` | agent 工具回退解析 |

---

## 五、通信 / 协作（Agent OS 原语）

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 演化 IPC 桥 | `wireEvolutionIPC`（serve_peer） | 对等消息总线 + 死信可观测（30s 周期计数/原因快照，只观察不自动重投） |
| 协作反馈通道 | `bridge.ipc.Bus().WithCollaborationObserver`（armed 时） | ask_agent / 协作收据进进化反馈源 |
| Peer 注册表 | `buildPeerRegistry`（发现契约） | 当前无生产 agent 暴露 SendMessage 面——注册表恒为空，非演化 ask_agent 发送失败提示"未注册"（保留契约，见 TODO） |

---

## 六、持久化 / 审计 / 状态

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 事件存储 | `WithEventStore`（sub）+ `fabric.WithEventStore` | 全量事件落库（`internal/ares_events`，内存/PG） |
| 反馈记录 | `FeedbackRecorder`（ares_evolution） | 策略结果 → experience 反馈系统（成功/失败计数） |
| Session Lease | `memoryManager.AcquireSessionLease` | 并发会话访问控制（TTL 租约，owner 校验） |
| 上下文清理 | `ContextCleaner`（memory/context） | turn 分组 + 工具语义摘要差分压缩 |
| 会话存储上限 | `SessionMemory.WithMaxMessages`（默认 500 条） | 长会话消息切片有界（v0.3.1 后补） |

---

## 七、资源控制 / 进化接线

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 运行预算 | `sdk.WithMaxTokens` / `sdk.WithTimeout` | token 上限 + 墙钟超时 |
| 治理预算 | `agentfabric.Governance`（TokenBudget/ToolBudget/Deadline，peer_assembly） | 每个 peer 的认知执行预算，从出生起有界 |
| GA 策略源 | `strategySrc.GetActiveStrategy`（fabric 策略戳） | 任务提交时打上活跃策略 ID，fitness 归因跨 promote 一致 |
| 策略戳 | `fabric.WithStrategyStamp` | 每次 Create 一个 2s 超时的 store 读 |
| 演化反馈环 | `RunScoredFeedbackLoop`（10s 周期） | 执行归因 → 置信度回注 tracker + 零 LLM 分数写回策略 store |

---

## 八、接线索引（源码位置）

- 组装入口：`cmd/ares/peer_assembly.go`（fabric/调度器/L2 内核/系统调用装配）、`cmd/ares/serve_wiring.go`（工具链/控制面）、`cmd/ares/serve_peer.go`（演化接线/面板）
- 执行内核：`internal/agentruntime/`（execution/session/submit）、`internal/kernel/`（scheduler 分片）、`internal/fabric/task/`（任务织物）
- 原语实现：`internal/agents/{peer,lease,outputguard}/`、`internal/agentipc/`
- 能力实现：`internal/runtime/protocol/skills/`（Catalog / Discovery / Loader / Experience / FTS5 / git/http 源 / changes）
- 内存接线：`internal/runtime/memory/`（manager_impl + context/cleaner + context/session）

---

## 九、说明

- 本清单为 **serve 启动即注入** 的能力；`Catalog.Activate` 后的 MCP 连接、按需加载的 SKILL.md / references 属运行时按需能力，不在此列。
- 已下线的出生能力（不要找）：leader 任务规划器、AHP 消息队列（`ahp.NewMessageQueue`）、ActionLog 审计（`WithActionLog`）、`internal/agents/actionlog` 包、体验定位器（`WithExperienceLocator`）——均随 Leader-Sub 删除或因零生产调用点移除。

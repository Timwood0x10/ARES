# Agent 出生自带能力（Capability Inventory）

> 日期：2026-08-15
> 范围：`serve` 启动创建 leader / sub agent 时（`cmd/ares/{serve,agents,tools}.go` + `internal/agents/sub` + `internal/ares_memory`）**自动注入**、无需额外配置即可使用的能力。不含运行时按需加载的能力（如 Skill 激活后的 MCP 连接、按需拉取的 SKILL.md）。
> 对照：英文版见 [agent-birth-capabilities.en.md](agent-birth-capabilities.en.md)。

---

## 一、总览（四层）

| 层 | 职责 | 代表能力 |
|----|------|----------|
| 架构层 | 执行骨架 | 任务规划 / 调度 / 队列 / 输出验证 |
| 能力层 | 知识技能 | SkillCatalog 全家桶（多源索引 / FTS5 / Experience / MCP 懒连接） |
| 原语层 | Agent OS 原语 | peer 直连 / actionlog / lease / snapshot / outputguard |
| 控制层 | 运行控制 | 预算 / GA 策略源 / 进化反馈 / 沙箱工具 |

---

## 二、执行与验证（TaskExecutor 核心）

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 输出结构验证 | `output.NewValidator(WithSchemaType)`（leader + sub） | 结果须符合配置 schema，结构性不一致在边界拒绝 |
| 输出守卫 | `outputguard.NewGuard().ValidateResult`（sub 事件路径 `processScheduledEvent`） | 拒绝结构不一致的 agent 结果 |
| 任务规划 | `leader.NewTaskPlannerWithConfig` + `plannerOpts` | 根据 sub 配置规划任务分发 |
| 调度器 | `leader.WithDispatcherAgentID` / `WithDispatcherEventStore` | 事件驱动单路径执行（§5.1） |
| 消息队列 | `ahp.NewMessageQueue`（leader/sub，MaxSize 500） | 异步消息缓冲 |
| 带验证执行器 | `sub.NewTaskExecutorWithValidation` | sub 每个任务经验证后执行 |

---

## 三、知识 / 技能（Capability Fabric）

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 常驻技能块 | `wireSkillCatalog` → `SetSkillsRegistry` → `BuildContext` | 出生即带 Level-0 metadata（name + 一句话描述），SKILL.md 按需加载（渐进披露） |
| 多源技能索引 | config.toml `[[skill_sources]]`：project / user / registered / **git / http / oci** | 只扫声明源，零全盘扫描 |
| FTS5 全文检索 | `FTS5Index`（Discovery 优先 FTS5，失败回退关键词匹配） | 检索排序 |
| 经验定位 | `leader.WithExperienceLocator(skillLocator)` | 任务匹配技能 + Experience 相关度先验 |
| Experience 持久化 | `~/.ares/experience.json`（原子写 tmp→rename） | 学习到的 task→skill 先验跨重启保留 |
| MCP 懒连接 | `SetMCPConnector(comp.MCP)` | 仅 Skill 激活时连接声明的 MCP server（`Catalog.Activate`） |
| listChanged 增量重索引 | `MCPManager.SetToolChangeHandler` → `Catalog.Refresh` | MCP tools/listChanged 触发 hash 增量重索引 |
| 变更检测 | `DetectIndexChanges` + `Catalog.Refresh` | 按 ID+Source+Hash 分类 Added / Modified / Removed |

---

## 四、工具集

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 内置工具 | `api_tools.RegisterBuiltinTools(WithFileSandboxDir)` | filesystem 等内置工具（沙箱目录限定） |
| 本机命令 | `registerNativeTools`（`ARES_NATIVE_TOOLS` allowlist） | 仅 allowlist 内命令经 `command -v` + `--help` 探测后暴露 |
| MCP 工具 | `setupMCP` → `internalReg` | 已连接 server 的 tools/list 注册进工具注册表 |
| 统一环境检索 | envcap 桥接（`SeedRegistry` → `envcap.Searcher`） | tools / skills / commands 统一检索（kindRank 排序） |

---

## 五、通信 / 协作（Agent OS 原语）

| 能力 | 接线点 | 说明 |
|------|--------|------|
| Peer 直连 | `buildPeerRegistry` → `SetPeerRegistry` / `NotifyPeer`（leader） | agent 间直接消息，不绕 leader（补充通知通道） |
| 消息队列 | `ahp.NewMessageQueue` | 见架构层 |

---

## 六、持久化 / 审计 / 状态

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 事件存储 | `WithEventStore(comp.EventStore)`（leader + sub） | 全量事件落库（`internal/ares_events`） |
| Action Log | `WithActionLog`（sub 任务三个结果出口） | 任务审计 + 回放（Append 幂等、Replay 从 startID 之后） |
| 反馈记录 | `WithFeedbackService` + `FeedbackRecorder.WithRefiner` | 结果反馈 → refine 小步进化（`strategy:<id>` key） |
| 状态快照 | runner_checkpoint persist / load（`state-snapshot/<execID>`） | 运行状态跨重启恢复（schema-version 守卫） |
| Session Lease | memoryManager 会话租约 | 并发会话访问控制（TTL 租约） |
| 上下文清理 | memoryManager `ContextCleaner` | turn 分组 + 工具语义摘要差分压缩 |

---

## 七、资源控制 / 进化接线

| 能力 | 接线点 | 说明 |
|------|--------|------|
| 运行预算 | `sdk.WithMaxTokens` / `sdk.WithTimeout`（agentloop.Request 透传） | token 上限 + 墙钟超时 |
| GA 策略源 | `WithStrategySource(comp.NewEvolution)` | 运行中读取部署的 prompt / params |
| 回归验证 | arena 回归（fingerprint 缓存） | 环境未变则跳过重跑 |
| 无参执行容错 | `internal-llm-and-utils` 采样参数经 requestOverrides 透传 | Temperature / TopP / Penalty |

---

## 八、接线索引（源码位置）

- 组装入口：`cmd/ares/serve.go`（`createAndRegisterServeAgents`、`wireSkillCatalog`、`buildPeerRegistry`、`registerNativeTools`、`setupMCP`）
- agent 构造：`cmd/ares/agents.go`（`createLeaderAgent`、`createAgents`、`createSubAgents`）
- 原语实现：`internal/agents/{peer,actionlog,lease,outputguard}/`、`internal/ares_runtime/state_snapshot.go`、`internal/ares_evolution/refine/`
- 能力实现：`internal/ares_skills/`（Catalog / SourceManager / Indexer / Discovery / Loader / Resolver / Experience / FTS5 / git/http 源 / changes）
- 内存接线：`internal/ares_memory/manager_impl.go`（skills 常驻块 + lease + ContextCleaner）
- 设计文档：`analysis-reports/ares-capability-fabric-design.md`

---

## 九、说明

- 本清单为 **serve 启动即注入** 的能力；`Catalog.Activate` 后的 MCP 连接、按需加载的 SKILL.md / references 属运行时按需能力，不在此列。
- Capability Fabric 分四批实施（核心 → config/MCP/Experience/envcap/hash → git/http/oci/FTS5/listChanged → code_review 并发与一致性修复），详见设计文档状态节。

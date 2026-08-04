# Runtime 统一协调与组件彻底闭环计划

> 日期：2026-08-04  
> 性质：收敛计划，不新增产品功能  
> 目标：所有启用组件均由 Runtime 统一装配、依赖解析、启动、协调、观测和关闭；组件之间只通过明确契约与共享数据面协作，不允许旁路、重复实例、stub 成功或静默空转。  
> 约束：不修改 `plan/AKG.md`（接口 SSOT）；不执行 git commit；任何源码改动均需用户逐阶段授权。

---

## 1. 最终目标与完成定义

### 1.1 目标架构

本计划中的 **Runtime** 不是单指当前 `ares_runtime.Manager`，而是系统级控制面（下称 **System Runtime**）。现有 `ares_runtime.Manager` 保留为 Agent 生命周期子系统。

System Runtime 负责：

1. 根据配置声明组件及 Required / Optional / Degraded 策略；
2. 构建依赖图并在启动前完成依赖校验；
3. 为所有组件提供同一个根 context、EventStore、EvidenceStore 及共享运行时引用；
4. 按拓扑顺序执行 Construct → Bind → Start → Ready；
5. 维护组件状态、失败原因、关键共享实例标识和健康快照；
6. 按逆拓扑执行 Stop → Wait → Close；
7. 为 `ares serve`、`ares start`、SDK 提供同一套装配/生命周期内核。

数据面保持现有职责：

- EventStore：运行事件；
- EvidenceStore：fitness 与决策证据；
- StrategyStore：活动策略与历史；
- KnowledgeStore：AKG 知识事实；
- MemoryManager：上下文、蒸馏与 RAG；
- Patch Registry / live executors：运行态配置与 DAG 热更新。

### 1.2 “彻底闭环”的硬性定义

只有同时满足以下条件才算完成：

- **唯一所有权**：每个生产组件仅有一个 owner、一个实例来源、一个关闭入口；
- **无旁路**：入口层不再手工补 `SetEventStore`、重复创建 MCP/Flight、Start 后再补 live binding；
- **无伪成功**：启用组件缺依赖不得返回 Ready；no-op / nil / fallback 必须显式呈现为 Disabled、Degraded 或 Failed；
- **真实数据流**：事件、证据、策略、知识均由生产实例写入，并由同一 Runtime 内的消费者读取；
- **真实控制流**：GA patch 命中 live Memory / Knowledge / DAG / Scheduler / Recovery；不是合成对象；
- **真实消费**：活动策略、RAG 片段、工具结果必须进入下一次 Agent 执行；
- **完整生命周期**：启动失败可逆序回滚；正常关闭无孤儿 goroutine、子进程、连接或未 flush 数据；
- **入口等价**：相同配置下 `ares serve`、`ares start`、SDK 的核心组件图和状态语义一致；
- **证据化验收**：所有结论都有可查询状态、测试断言或运行产物，不以日志“看起来成功”为准。

---

## 2. 当前状态与关键差距

### 2.1 已经成立的基础

- `ares_bootstrap.Bootstrap` 已是主要装配中心（`internal/ares_bootstrap/bootstrap.go:103-107`）；
- EventStore、EvidenceStore、FlightRecorder 已收敛为共享实例；
- GA 五路 Source 契约和 AKG 写读链路已在代码层成立；
- `serve` 已复用 Bootstrap 的 MCP / Flight / KnowledgeRuntime / StrategyStore；
- 现有模块测试、GA/AKG E2E、RLS integration、shutdown 测试资产较丰富；
- 40+ 高危/高影响问题已修复并经 build/test review 验证。

### 2.2 控制面仍未统一

| 差距 | 证据 | 影响 |
|---|---|---|
| 当前 Runtime 只拥有 Agent 生命周期 | `internal/ares_runtime/manager.go:35-61` | MCP、Flight、GA/AKG 后台任务、HTTP、工具仍由入口/Bootstrap 各自管理 |
| Runtime 构造时没有 MemoryManager | `provide_runtime.go:9-10` 传 `nil`；Memory 在 Bootstrap 后构造 | `serve.go:143-145` 再手工 `SetEventStore`，存在旁路 |
| 构造与启动混合 | `ProvideMCP` 内直接 `Start`；Bootstrap 内直接启动 Flight 和 ticker | 无法统一回滚、Ready 和故障归因 |
| Bootstrap 存在裸 goroutine + 私有 WaitGroup | `bootstrap_steps.go:69-114,201-259` | 生命周期不由 System Runtime 统一托管，且违反项目结构化并发规范 |
| live DAG 在 Runtime 启动后补绑定 | `serve.go:293-301` | Ready 前可能短暂使用合成 executor；入口外无法保证同样绑定 |
| 配置 `enabled` 与真实运行不一致 | Bootstrap 没有读取 `cfg.Evolution.Enabled` / `cfg.Memory.Enabled` | “配置关闭但组件仍构造/运行”，状态语义不可信 |
| AKG/RAG 以 best-effort 静默缺环 | `knowledge_akg.go:151-185`、`retriever_wiring.go:31-39` | 启用了 Retrieval/RAG 仍可能仅 store-only 或无 retriever，却不阻止 Ready |
| Tools 注册 nil 依赖 | `builtin.go:121-175` | 已防 panic，但工具仍可能“注册成功、调用失败” |
| MCP 连接失败不影响 Start | `ares_mcp/manager.go:72-88` | Required MCP 不可用时仍报告启动成功 |
| api_impl 有独立服务、路由和关闭所有权 | `internal/api_impl/service.go:165-306,388-452` | 与 serve/SDK 状态、资源、关闭语义不完全一致 |
| SDK 自建另一套 Runtime 资源图 | `sdk/sdk.go:126-159,630-731` | 同能力双轨实现，后续容易漂移 |
| Deployment staging 恒通过 | `deployment_wiring.go:10-32` | 启用安全部署后仍是名义门禁 |
| Track A outcome 写侧为空 | `provide_distillation.go:67-81` | 经验提示能读，策略效果未写回经验库 |

---

## 3. 设计原则与非目标

### 3.1 原则

1. **控制面收敛，数据面保持**：不重写 GA、AKG、Memory、MCP、Agent；统一其生命周期与契约。
2. **构造无副作用**：Provider 只 Construct，不启动 goroutine、网络连接或 ticker。
3. **先 Bind，后 Start**：所有 live 引用必须在 Start 前完成；Ready 后禁止关键依赖补丁式注入。
4. **配置即契约**：启用即要求依赖满足；允许降级必须由配置显式声明。
5. **fail-loud**：配置/部署级缺失不能静默 fallback；只允许文档化的运行时降级。
6. **一个组件一个 owner**：禁止 `serve` / api_impl / SDK 重复创建或重复关闭同一资源。
7. **可观测状态而非日志猜测**：组件必须报告状态、原因、依赖和实例一致性。
8. **逐阶段可回滚**：每阶段只改变一个边界，保留兼容适配层后再删除旁路。

### 3.2 非目标

- 不增加新的 genome、provider、MCP tool 或产品 API；
- 不扩展新业务能力；
- 不重写 Agent 执行模型；
- 不以覆盖率数字代替行为测试；
- 不修改 `plan/AKG.md`；
- 不在本计划中做 git commit。

### 3.3 “所有组件”的范围

阶段 0 必须以 `go list ./...`、生产入口调用图和配置项三方交集生成完整清单，不允许凭印象挑组件。至少覆盖：

- 配置、密钥与校验；
- EventStore、Archive/Compaction、EvidenceStore；
- Storage、PostgreSQL、VectorStore、Embedding、缓存；
- LLM 主客户端、fallback、scoring client；
- Memory、Session/Profile、Experience distillation、RAG；
- KnowledgeStore、KnowledgeRuntime、AKG DistillBridge、providers/retrievers；
- GA、Genome/Diff/Patch registry、Coordinator、StrategyStore、Deployment/Rollback；
- Agent Runtime、leader/sub agents、live DAG、recovery/resurrection、workflow/scheduler；
- ToolRegistry、builtin tools、Planner、MCP clients/transports；
- Discovery、Eval、Query cache、Memory/Retrieval API services；
- Dashboard、Monitoring、EventBridge、HTTP/API；
- Shutdown、background workers、observability。

对于仓库中存在但任何生产配置和入口均不可达的包，必须标为 `Dormant/Dead` 并单独决策；不得伪装为 Runtime 已协调组件，也不得在本计划中顺手新增其功能。

---

## 4. Runtime 组件契约

### 4.1 生命周期接口

实现阶段应以小接口表达能力，不强迫所有组件实现一个大接口：

```go
type Component interface {
    Name() string
    Dependencies() []string
}

type Binder interface {
    Bind(ctx context.Context, deps Resolver) error
}

type Starter interface {
    Start(ctx context.Context) error
}

type ReadinessChecker interface {
    Ready(ctx context.Context) error
}

type Stopper interface {
    Stop(ctx context.Context) error
}

type Waiter interface {
    Wait() error
}
```

状态机：

```text
Declared → Constructed → Bound → Started → Ready
                         ↘ Failed
Ready → Degraded / Failed
Ready|Degraded → Stopping → Stopped
```

每个状态至少记录：`name`、`mode`、`state`、`reason`、`dependencies`、`started_at`、`instance_id`。

### 4.2 组件模式

- **Required**：启用后依赖缺失、Bind/Start/Ready 失败即整体启动失败；
- **Optional**：未启用时状态为 Disabled，不构造、不启动；
- **Degraded**：仅配置明确允许时可降级；必须报告能力缺失和原因，不能冒充 Ready。

建议默认：

| 组件 | 启用时默认模式 |
|---|---|
| EventStore、Agent Runtime、LLM、Memory core | Required |
| GA（`evolution.enabled=true`） | Required |
| AKG read/write（`knowledge.retrieval_enabled=true`） | Required；如只读必须显式配置 read-only |
| RAG（`memory.enable_rag=true`） | Required；至少一个配置声明的 retriever Ready |
| Embedding（任一启用组件依赖） | Required |
| MCP server | 每 server 独立 Required/Optional，不再仅靠 Enabled/AutoStart |
| Dashboard/HTTP | 按入口配置 Required/Optional |
| Discovery、LLM scoring | Optional；启用后 Required |
| Deployment pipeline | 启用后 Required，staging 不得恒通过 |

---

## 5. 目标组件图与共享实例约束

### 5.1 启动拓扑

```text
Config/Secrets
  → EventStore / Storage / Embedding / LLM
  → MemoryManager / KnowledgeStore / ExperienceRepo / ToolRegistry
  → KnowledgeRuntime / MCPManager / FlightRecorder
  → EvidenceStore / Genomes / PatchRegistry / StrategyStore
  → GA Coordinator / Deployment / Distillation subscribers
  → Agent Runtime Manager + live DAG binding + StrategySource
  → Agents
  → Monitoring / Dashboard / HTTP
  → Runtime Ready
```

### 5.2 必须证明为同一实例

- Runtime、Memory、Flight、Dashboard/bridge 使用同一 EventStore；
- Flight、Memory retriever、KnowledgeRuntime、五个 Genome 使用同一 EvidenceStore；
- DistillBridge 写与 StoreProvider / KnowledgeRetriever 读使用同一 KnowledgeStore 和 namespace；
- GA 部署写与 Agent StrategySource 读使用同一 StrategyStore；
- KnowledgePatchExecutor 与 AKF tools 使用同一 KnowledgeRuntime；
- workflow/scheduler/recovery executor 绑定 Runtime 中 live DAG / policy，不使用合成对象进入 Ready；
- MCP manager、Agent ToolBinder、Dashboard MCP adapter 使用同一 ToolRegistry 视图。

这些一致性必须由 Runtime 状态或集成测试断言，不能只靠注释。

---

## 6. 分阶段实施计划

## 阶段 0：冻结闭环契约与建立基线

**目的**：先建立可验证基线，避免边改边改变“闭环”定义。

### 工作项

1. 新增独立的 Runtime 闭环测试包/测试套件；不先重构生产代码；
2. 生成当前组件图快照：组件、实例、依赖、状态、入口；
3. 为每个组件确定 Required/Optional/Degraded 策略；
4. 建立失败注入矩阵：DB 不可达、Embedding 超时、MCP 断连、LLM 失败、Store 写失败、shutdown 超时；
5. 将现有 build/test/race/integration 命令固化为阶段门禁；
6. 记录现有旁路，禁止新增旁路。

### 交付

- Runtime 组件清单与依赖 DAG；
- 闭环验收矩阵；
- 基线测试报告；
- 现有失败分类清单。

### 完成门禁

- 所有生产组件都有 owner、模式、依赖、Start/Stop/Ready 定义；
- 当前不一致项被测试明确暴露，而不是被 mock 掩盖；
- 不修改业务功能。

---

## 阶段 1：建立 System Runtime 控制面

**目的**：创建统一组件目录和生命周期编排；先纳管，不改变数据算法。

### 工作项

1. 在合适包中建立 System Runtime（建议新类型，避免把组件职责塞进 Agent Manager）；
2. 实现组件注册、依赖拓扑排序、状态机、统一根 context；
3. 实现启动失败的逆序 rollback；
4. 实现正常关闭的逆序 Stop → Wait → Close；
5. 将 bootstrap `Components` 变为 Runtime 的只读组件视图/兼容 façade；
6. 将 bootstrap 私有 `wg` 和裸 goroutine迁入 Runtime 管理的 errgroup/worker；
7. 增加组件状态快照 API（先内部 API，不新增产品功能）。

### 第一批纳管

- EventStore；
- Agent Runtime Manager；
- MemoryManager；
- MCPManager；
- FlightRecorder；
- Bootstrap 后台任务（distillation subscriber、GA ticker、LLM suggestion ticker）。

### 完成门禁

- Provider 构造期间不启动任何 goroutine/连接；
- Runtime 启动后所有纳管组件达到 Ready 或明确 Degraded/Disabled；
- 任一阶段失败，已启动组件全部逆序停止；
- shutdown 后 Runtime 管理的 goroutine 归零；
- `serve.go` 不再拥有这些组件的独立 Stop/Wait 逻辑，只调用 System Runtime Stop。

---

## 阶段 2：统一配置门控与依赖语义

**目的**：让“配置启用”与“真实运行状态”一致，消灭 nil 多义和静默空转。

### 工作项

1. Runtime 解析 `memory.enabled`、`evolution.enabled`、`knowledge.retrieval_enabled`、`embedding.enabled`、MCP server 策略；
2. 修正当前始终构造 NewEvolution / Memory 等与 Enabled 不一致的路径；
3. 将 AKG、RAG、LLM scoring、PG strategy store 的 fallback 分类为 Disabled / Degraded / Failed；
4. 启用 AKG 时明确选择：`full`（读写）或 `read_only`，不得因缺 embedding 自动变成 store-only Ready；
5. 启用 RAG 时校验所需 retriever，并在 Ready 检查中验证；
6. MCP server 增加 Required/Optional 语义；Required 连接失败应阻止 Runtime Ready；
7. 配置错误在启动前 fail-loud，运行期故障才进入 Degraded。

### 完成门禁

- 配置关闭：组件为 Disabled，零 goroutine、零网络、零 store 写；
- 配置启用：依赖缺失明确失败，不能只有 Warn；
- Runtime 状态能准确解释每个组件为何 Ready/Degraded/Disabled/Failed；
- 不再用 nil 判断业务状态。

---

## 阶段 3：所有 live binding 在 Ready 前完成

**目的**：消灭合成 executor、Start 后补线和入口差异。

### 工作项

1. 调整装配顺序：Memory 构造完成后再构造 Agent Runtime，直接传入真实 MemoryManager；删除 `serve.go` 手工 `SetEventStore`；
2. Agent 与其 live DAG 在 Runtime Start 前注册；
3. workflow/scheduler/recovery PatchExecutor 在 Bind 阶段直接绑定 live DAG/policy；
4. 删除或隔离仅用于测试的 minimal synthetic DAG，不允许其进入生产 Ready；
5. KnowledgePatchExecutor 在 Bind 阶段绑定唯一 KnowledgeRuntime；
6. StrategySource 在 Agent 构造前绑定唯一 StrategyStore；
7. ToolBinder / AKF tools / MCP bridge 在 Agent Start 前完成；
8. Ready 检查断言所有启用 genome 都有 live target。

### 完成门禁

- Runtime Start 前，五个 genome 的读写目标全部已绑定；
- 不再存在 `Runtime.Start()` 之后调用 `wireEvolutionLiveDAGs` 的生产路径；
- 对每种 patch 做一次 snapshot → apply → observe → rollback 集成测试；
- patch 对合成对象生效但 live 状态不变时，测试必须失败。

---

## 阶段 4：闭合所有真实数据反馈环

**目的**：证明组件间不是“有引用”，而是以真实数据完成输入→输出→反馈。

### 4.1 Event → Evidence → GA → Strategy → Agent

验收链：

1. Agent Runtime 产生成功、失败、恢复、检索事件；
2. Flight/Memory/Knowledge collector 写入五个 Source；
3. Genome 读取同一 EvidenceStore 并产生非默认 fitness；
4. Coordinator 产生 Apply/Drop/Delay/Reject；
5. Apply 写入同一 StrategyStore；
6. 下一次 Agent 执行读取 active strategy 并应用 prompt/参数；
7. outcome 再回到 Event/Evidence。

### 4.2 Task → Distillation → AKG → RAG → Agent

验收链：

1. TaskCompleted/Failed 触发 Experience 与 AKG distillation；
2. KnowledgeObject 经质量门后 Active；
3. 同一 KnowledgeStore 可查询到；
4. MemoryManager 的 RAG 检索获得真实 Relevance；
5. 下一次 prompt 包含知识片段；
6. Knowledge fitness 进入 EvidenceStore。

### 4.3 Strategy outcome → Experience → Guidance

1. 复用现有 Runtime outcome recorder；
2. 将实际策略结果写入 experience store；
3. GuidanceProvider 读取对应 tenant/task type 的 outcome；
4. 下一轮 mutation 使用该 hint；
5. 禁止 `RecordStrategyOutcome` 成功 no-op。

### 完成门禁

- 五路 fitness 都有非 stub 记录，且能定位产生它的事件；
- active strategy 变化能在下一次 Agent 请求中观测；
- KnowledgeObject 写、读、注入使用同 tenant/namespace；
- Track A 读写双向成立；
- 任何链路断点都使闭环测试失败。

---

## 阶段 5：Tools 与 MCP 进入真实依赖图

**目的**：工具不只是“不 panic”，而是启用即真实可用。

### 工作项

1. 将 `RegisterGeneralTools` 改为依赖注入注册；
2. 只有依赖 Ready 的工具才注册；不可用工具不进入 Agent ToolBinder；
3. Knowledge tools 绑定 KnowledgeRuntime/KnowledgeStore/Service；
4. Memory tools 绑定真实 MemoryManager/ExperienceRepo/Profile store；
5. Planner 绑定真实 RegistryProvider；Embedding tool 绑定真实 client；
6. MCP manager、内部 registry、公共 dashboard registry 收敛为一个源或受控视图；
7. Required MCP 连接、工具发现和一次调用纳入 Runtime Ready；
8. MCP 断连进入 Degraded，重连后恢复 Ready，状态可观测。

### 完成门禁

- 工具列表中不存在已知调用必失败的工具；
- `knowledge_search`、`distilled_memory_search`、`correct_knowledge`、`distill_memory` 返回真实结果；
- Agent、Dashboard、MCP 看见的工具集合一致；
- MCP 子进程/连接由 Runtime 唯一关闭。

---

## 阶段 6：统一 `serve` / `start` / SDK 入口

**目的**：三入口只负责适配配置与暴露 API，不再各自拥有核心装配逻辑。

### 工作项

1. 抽取唯一 Runtime Builder/Factory；
2. `ares serve`：只负责 CLI、信号、HTTP 展示和任务提交；
3. `ares start` / api_impl：只负责嵌入式 API 适配；
4. SDK：复用同一核心 Builder，通过 adapter 提供 SDK 友好接口；
5. 统一 EventStore、Memory、MCP、AKG、GA、ToolRegistry、关闭顺序；
6. 为差异化组件（Dashboard、HTTP）使用入口级 Optional component，不复制核心装配；
7. 增加“相同配置组件图等价”契约测试。

### 完成门禁

- 三入口的核心组件名称、模式、依赖关系、共享实例约束一致；
- 禁止入口层 new 核心组件或直接 Start/Stop；
- 三入口执行相同闭环场景得到等价结果；
- SDK `Close` 与 System Runtime `Stop` 使用同一生命周期内核。

---

## 阶段 7：真实部署门禁、故障恢复与长稳验收

**目的**：从“能闭环”提升到“可安全长期运行”。

### 工作项

1. 替换 staging 恒 `1.0`：使用现有 Snapshot/Apply/Evaluate/Rollback 能力做真实 shadow；
2. candidate 必须相对 baseline 达到配置阈值才 promotion；
3. Apply 后观察窗口内恶化自动 rollback；
4. 注入 DB、Embedding、MCP、LLM、Store、Agent 崩溃故障；
5. 验证 Runtime 状态迁移、降级边界、恢复和数据一致性；
6. 运行 race、资源泄漏、重复启停、6h/24h soak；
7. 建立发布门禁报告。

### 完成门禁

- deployment 不再恒通过；
- 所有外部依赖有超时、取消、状态和恢复策略；
- 进程关闭后 goroutine、MCP 子进程、HTTP listener、DB/Redis/LLM 连接归零；
- Evidence/Knowledge/Strategy 数据增长受控；
- 24h 内无持续 goroutine/内存/连接增长；
- 所有 Required 组件维持 Ready，否则发布失败。

---

## 7. 组件闭环验收矩阵

| 组件 | Runtime 输入 | 输出/副作用 | Ready 断言 | 闭环断言 |
|---|---|---|---|---|
| Config | YAML/env | 组件声明与模式 | 校验通过 | 启用状态与组件状态一致 |
| EventStore | Agent/Runtime 事件 | 订阅流、归档 | append/subscribe 可用 | Flight、Distill、Monitor 读同一实例 |
| MemoryManager | EventStore、retrievers | context/prompt、memory fitness | Start + retriever 状态符合配置 | 下一轮 prompt 含真实 memory/AKG 片段 |
| Embedding | 配置/网络 | vector | 探针成功 | 写侧、查询侧模型/维度一致 |
| Experience distill | task event、LLM、embedding、repo | experience | subscriber Ready | experience 被 GuidanceProvider 读取 |
| KnowledgeStore | DistillBridge | KnowledgeObject | backend + namespace Ready | StoreProvider/KnowledgeRetriever 读到同一对象 |
| KnowledgeRuntime | providers、store、evidence | graph/context、knowledge fitness | provider 集合完整 | AKF tools 与 patch executor 共用实例 |
| FlightRecorder | EventStore、EvidenceStore | 3 路 fitness | 已订阅 | 事件产生对应 Source 证据 |
| GA | 五路 evidence、patch targets | proposal/decision/strategy | 5 genome + live targets Ready | strategy 被 live Agent 消费 |
| Deployment | snapshot/eval/live target | promote/rollback | staging 非 nominal | 坏 patch 被拒，回退可恢复 |
| MCPManager | server configs、registry | tools | Required server connected | Agent/Dashboard 调用真实工具 |
| ToolRegistry | 真实依赖 | tool results | 无不可用已注册工具 | 工具结果进入 Agent 执行 |
| Agent Runtime | agents、factories、memory、DAG | events/tasks | Agent healthy | 产出事件并消费策略/RAG/tools |
| Monitoring | component states/events | health | 能读取 Runtime 状态 | 不依赖复制的数据源 |
| Shutdown | 依赖图 | Stop/Wait/Close | 可重复调用 | 资源归零、在途工作有界完成 |

---

## 8. 测试与发布门禁

### 8.1 L0：静态与单元门禁（每阶段）

```bash
gofmt -l <changed-files>
go vet ./...
staticcheck ./...
golangci-lint run
go test ./...
```

### 8.2 L1：Runtime 进程内集成

必须新增/收敛以下场景：

1. Runtime 完整启动和逆序关闭；
2. 启动中途失败的 rollback；
3. Required/Optional/Degraded 状态矩阵；
4. shared instance identity 断言；
5. live binding 在 Ready 前完成；
6. Event→Evidence→GA→Strategy→Agent；
7. Task→AKG→RAG→Prompt；
8. Tools/MCP registry 一致性；
9. 重复 Stop、并发 Stop、context cancel；
10. 不允许孤儿 goroutine。

### 8.3 L2：真实依赖 integration

- PostgreSQL + pgvector：

```bash
go test -tags integration ./internal/storage/postgres/repositories/... -count=1
```

- 真实 embedding 服务：写入和查询同模型/维度；
- 可控 LLM：蒸馏与建议 pipeline；
- stdio/SSE MCP fixture：连接、调用、断连、恢复、关闭。

### 8.4 L3：入口 E2E

对 `ares serve`、`ares start`、SDK 各跑同一验收场景并对比：

- 组件图；
- 状态；
- 五路 evidence；
- active strategy；
- KnowledgeObject；
- prompt 注入；
- 工具集合；
- shutdown 资源归零。

### 8.5 L4：并发、故障与长稳

```bash
go test -race ./internal/ares_runtime/... ./internal/ares_bootstrap/... ./internal/ares_flight/... ./internal/knowledge/... ./internal/ares_evolution/...
```

- 重复启停 100 次；
- 任务并发 + evolution + distillation；
- 6h 预验收、24h 发布验收；
- 定期采集 goroutine、heap、连接池、队列深度、Evidence/Knowledge/Strategy 增长。

### 8.6 发布放行条件

以下任一不满足即不放行：

- build/vet/lint/test/race 任一失败；
- Required 组件非 Ready；
- 有启用组件静默降级；
- 任一共享实例约束失败；
- 任一核心数据闭环无真实记录；
- 任一入口组件图不等价；
- shutdown 后资源未归零；
- deployment 仍是 nominal staging；
- 24h 指标持续单调增长且无法解释。

---

## 9. 实施顺序、依赖与停线点

```text
阶段 0 基线
  ↓
阶段 1 System Runtime 生命周期
  ↓
阶段 2 配置/依赖语义
  ↓
阶段 3 Ready 前 live binding
  ↓
阶段 4 真实反馈环
  ↓
阶段 5 Tools/MCP 真依赖
  ↓
阶段 6 三入口统一
  ↓
阶段 7 shadow/故障/长稳/发布
```

每阶段结束必须停线 review，不得跨阶段批量推进。出现以下情况立即停止：

- 需要改变 `plan/AKG.md` 的接口方向；
- 需要砍模块或改变产品行为；
- 无法保持入口兼容；
- 出现数据迁移或不可逆状态变更；
- Runtime ownership 无法唯一确定。

---

## 10. 建议首个执行批次（获得授权后）

只执行阶段 0，不改核心行为：

1. 新建 Runtime 组件清单与闭环验收测试骨架；
2. 为当前 Bootstrap 生成组件状态快照；
3. 写 4 个先失败的契约测试：
   - `Memory.Enabled=false` 时不应启动 Memory；
   - `Evolution.Enabled=false` 时 GA ticker 不应运行；
   - `Knowledge.RetrievalEnabled=true` 且写依赖缺失时不得报告完整 Ready；
   - Runtime Ready 前所有 GA executor 必须绑定 live target；
4. 写共享实例一致性测试；
5. 复用现有 shutdown 测试增加完整 Bootstrap 启停断言；
6. 输出阶段 0 差距报告，由用户确认阶段 1 的 Runtime 接口后再改生产代码。

该批次的价值：先把“彻底闭环”变成可执行测试契约，避免再次出现代码接通但生产路径未启动、启用但空转、或者入口之间状态不一致。

---

## 11. 最终验收场景（Golden scenario）

一次验收必须完整走通：

1. System Runtime 读取 full-closure 配置；
2. Required 组件全部 Ready，实例一致性检查通过；
3. Agent 执行一条成功任务和一条失败/恢复任务；
4. EventStore 出现完整事件；
5. Experience 与 AKG distillation 产生真实记录；
6. Memory/Knowledge RAG 把记录注入下一次 prompt；
7. EvidenceStore 出现 workflow/scheduler/recovery/memory/knowledge 五路 fitness；
8. GA 产生明确决策；Apply 时更新 active strategy；
9. 下一次 Agent 执行消费新策略；
10. strategy outcome 写回 experience，并影响下一轮 guidance；
11. MCP/Knowledge/Memory 工具真实调用成功；
12. 注入一次 MCP 断连和 Agent 故障，Runtime 状态正确降级并恢复；
13. 触发 shutdown，按逆拓扑停止并证明资源归零。

只有该场景在 `serve`、`start`、SDK 三入口都通过，且 24h 长稳无退化，才可宣布：

> 所有启用组件已通过 Runtime 统一协调工作，系统完成运行时级的彻底闭环。

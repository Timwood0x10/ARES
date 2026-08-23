# AresOS 0.3.0 全功能融合计划（Fusion Plan）

> 目标：把 0.2.x 时代散落在独立包/仅测试引用里的**全部**能力，接入 0.3.0 的
> Agent OS 生产主二进制（`cmd/ares`），并且**只保留一套接口与一套执行逻辑**。
> 功能上**禁止打折扣**——凡是旧版能做到的语义，融合后必须等价或更强。

---

## 北极星：用户只有两个动词

这是凌驾于一切之上的产品原则，所有融合动作都要用它做最终校验：

```go
// 一次性：写一份 ares.yaml（基础设施 + 策略声明）
//   llm:        provider/model/key
//   storage:    pg / embedding（决定蒸馏、经验库能否激活）
//   distill:    何时蒸馏（cadence / 触发阈值）
//   evolution:  是否开启空闲进化、节奏

// 代码里：只有两个动词
rt := sdk.NewRuntime(sdk.FromConfig("ares.yaml"))  // 起运行时（读配置）
// 注：FromConfig 是 LoadConfigFile+ToOptions 的薄糖，新增公共符号需计入
// v040 设计的 ≤10 符号预算，实现时不得夹带其他面。
agent := rt.NewAgent(...)                          // 造 agent
// 完。之后不需要在代码里接线任何子系统。
```

**边界要分清（这是对"零配置"的纠正）**：

- **允许且必要——声明式基础配置**：用户写一份 `ares.yaml` 配 LLM、存储、
  蒸馏节奏、进化开关等**基础设施与策略**。这是一次性的、声明式的，**天经地义要有**。
- **禁止——运行时保姆式接线**：配置之后，用户**不需要**在代码里 new 各种 manager、
  手动 wire 复活/编排/DAG/工具发现/MCP/经验注入，也不需要在运行中拧旋钮。
- **agent 只干两件事**：干活（执行任务）、反馈（回报结果与信号）。
- **其余全是水面下的自动器官**：复活、动态编排、动态 DAG、记忆蒸馏、进化、
  空闲自进化、工具发现、MCP、经验注入——**由 ares.yaml 声明式开关控制，运行时自动托管**，
  用户不在代码里接线、不在运行中操作。
- **对外只暴露"可观测性"**：一个能"看"的黑盒窗口（trace/event/metrics），
  **不是能"拧"的运行时旋钮**。用户配一次、观察运行，不做运行期干预。

**融合验收闸门（每个功能都要过）**：任何能力融合后：
- ✅ 允许增加：`ares.yaml` 里的**声明式配置项**（一次性、有合理默认值）；
  「运行时自动行为」；「可观测输出」。
- ❌ 判定失败：给**代码 API** 增加了必填参数、必须 new 的组件、必须主动调用的
  接线方法，或必须在运行期调用的操作旋钮。
> 例：P3 经验注入——用户绝不该看到任何"经验库"API 或 wire 调用；agent 重试时自动
> 回查、自动注入，用户顶多在 `ares.yaml` 里开关它、在 trace 里看到"本次重试引用了经验 #42"。

---

## 0. 铁律（贯穿所有阶段，验收时逐条核对）

1. **单引擎**：节点/任务的执行只有一条路径——内核 `kernelscheduler` 量子调度 +
   `agentfabric` 生命周期。任何"第二执行器"（`internal/workflow.Runner`
   `MaxParallel=1`、collaboration 自带循环等）一律收敛掉，不得并存。
2. **单接口**：同一能力只暴露一套 API。出现重复实现时，**保留最贴近内核、
   已被生产接线的那套**，其余删除或改成"零逻辑的薄转发 + Deprecated"，
   且转发在本计划结束前必须彻底删掉。
3. **接入生产**：判定"已融合"的唯一标准 = 被 `cmd/ares` 的**非测试**生产路径
   （`serve.go` → `createAndServeAgents` / `runKernelRecoveryLoop` /
   `wireEvolutionIPC` / `bootstrap`）真实调用并跑起来。包存在、有测试，**不算**。
4. **不打折扣**：agent 复活必须是"有状态认知复活"（复用 fabric 的
   `Recover`+`CognitiveState`），不得降级为纯任务重排；编排必须覆盖
   委托/流水线/编排三模式；DAG 必须支持条件边+路由+循环。
5. **每步可独立编译、独立测试、独立提交**；每步都有客观验收（编译/vet/race/引用扫描）。
6. **提交纪律**：每次 commit 由用户当次确认（code_rules_v2 铁律 #1），AI 不自行提交。

---

## 1. 现状裁定（融合前基线，均带证据）

| 能力 | 现状 | 证据 | 裁定 |
|------|------|------|------|
| 任务级恢复 | 已接线 | `createPeerAgents` 恢复循环、`runKernelRecoveryLoop` 的 `RequeueExpiredLeases` | **保留为恢复主干** |
| agent 级复活（策略） | 未接线 | `internal/plugins/resurrection`，仅 `api/service/runtime` 引用，`cmd/ares` 不走 | **策略并入恢复主干，删插件** |
| agent 复活（原语） | 已存在 | `agentfabric` `Recover`/`CognitiveState`/`SetCognitiveState` | **直接复用，不新造** |
| 编排三 API | 未接线 | `agentipc/collaboration.go:48/87/152`，仅测试引用 | **语义并入 sdk.Graph，删 API** |
| 编排语义（topic） | 已接线 | `evolution_ipc.go:95,163` topic 分发 | **改为驱动 sdk.Graph** |
| sdk.Graph（DAG 运行器） | 未进主二进制 | 仅 `examples/`、测试引用 | **提升为唯一图引擎并接线** |
| internal/workflow/graph | 生产在用 | `provide_new_evolution.go:29` genome DAG | **执行层收敛到 sdk.Graph** |
| api/graph | Deprecated | 全仓 0 import | **删除** |
| 记忆蒸馏 | 已接线 | `bootstrap.go:287`、`distiller.go:132`、`peer_mode.go:180` | **保持，纳入回归** |
| 进化系统 | 已接线 | `provide_new_evolution.go:94-155`、`serve_agents.go:79` | **保持，纳入回归** |

**待融合的三个缺口**：① agent 级认知复活策略；② 编排三模式统一到 sdk.Graph；
③ sdk.Graph 成为唯一图引擎并进主二进制。其余（蒸馏/进化/任务恢复）已在生产，只做回归保护。

---

## 阶段 A：恢复子系统统一（agent 认知复活，不打折扣）

**构想**：把"心跳监死→重建 agent→恢复认知快照"的策略，从未接线的
`internal/plugins/resurrection` 迁移进**已接线**的 `aresrecovery` 恢复循环，
底层直接调用 fabric 既有的 `Recover(agentID, CognitiveState)` 原语。
结果：**一个恢复子系统**（`aresrecovery`）、**一个接线点**（`runKernelRecoveryLoop`），
同时覆盖「任务不丢」+「agent 认知复活」两层，删掉 resurrection 插件这套并行实现。

### A1. 认知快照贯通（1.5 天）
**做什么**：确认 fabric `CognitiveState()` 快照在 agent 被 `Kill`/租约过期时可读取并持久化到
恢复子系统可访问处。**存储裁定（2026-08-22 评审修订）**：禁止裸 `map[agentID]CognitiveState`
——按 code_rules_v2 §6.1，快照必须走带 SchemaVersion 的信封封装（复用 agentfabric 既有的
认知 schema 版本机制，与 `DecodeCognitiveState` 同源）；每 agent 仅保留最后一份快照，
`Recover` 消费后清除、Agent 进入 Retired 终态时清除，防止长期运行内存无界。
**验收**：
- [x] 单测：agent 运行→写入认知→`Kill`→恢复子系统能取到该 agent 的最后认知快照（字段完整、SchemaVersion 正确）。
- [x] `go test -race ./internal/aresrecovery/ ./internal/agentfabric/` 全绿。
- [x] 快照读取零拷贝共享导致的 race：`-race` 下并发 Kill+快照读无告警。

### A2. 恢复循环扩展为"任务重排 + agent 复活"（2 天）
**做什么**：在 `aresrecovery.Recovery` 增加一条**与现有 `RequeueExpiredLeases` 并列**的
`RecoverDeadAgents(ctx)`：检测 fabric 中处于 dead/失联且带存活认知快照的 agent →
调用 `fabric.Recover(agentID, snapshot)` 原地复活（或 spawn 替身承接快照，取 fabric 语义）。
把这条挂进 `runKernelRecoveryLoop` 同一 tick，**不新增第二个后台循环**。

**双路仲裁规则（2026-08-22 评审修订，必须实现并在测试断言）**：
同一死亡事件只允许一条路径处理，优先级固定：
1. 存在认知快照 **且** 重启计数 < `maxRestarts` → **原地复活**（继承原 agentID 与
   provenance，审计连续；这是 Kernel mechanism，不是 cognition，不违反 Rule ①）；
2. 无快照或超限 → 走既有 W1 replacement 路径（新 ID，从 checkpoint 续跑任务）；
3. 二者在同一 tick 内互斥，禁止"既复活又派替身"双重处理。

**哲学边界（写入注释防翻案）**：§12 "Agent 是 disposable" 的含义是 *不必为救 agent 而救*
（任务永远优先）；带状态原地复活是降低重学成本的机制优化——任务仍由 durable intent 驱动，
复活与否由 Kernel 依策略裁决，Agent 认知不参与该决策。
**验收**：
- [x] 集成测试：spawn agent→写认知→模拟死亡→一个 tick 后 agent 状态回 Idle 且认知快照被还原（断言 `CognitiveState()` 等于死前）。
- [x] "任务不丢"回归：原 `RequeueExpiredLeases` 行为不变，既有恢复测试全绿。
- [x] 复活次数受策略上限约束（复用/新增 `maxRestarts`），超限则终态为 Retired，不无限复活。
- [x] `go test -race ./cmd/ares/ -run 'TestKernelRecovery|TestChaos'` 全绿。

### A3. 删除 resurrection 插件（0.5 天）
**做什么**：确认 `internal/plugins/resurrection` 的能力已被 A2 完全覆盖后，删除该包；
清理 `api/service/runtime/service.go` 内 `resurrection.New` 引用（改走统一恢复子系统或直接移除）。
**验收**：
- [x] 全仓引用扫描：`resurrection` 包零生产引用、零测试引用后删除。
- [x] `go build ./... && go test ./...` 全绿。
- [x] `handleChaos` 内关于恢复路径的注释更新为指向统一恢复子系统。

**阶段 A 出口**：恢复只剩 `aresrecovery` 一套，一个循环覆盖任务重排 + agent 认知复活，
resurrection 插件消失。agent 复活**无折扣**（有状态认知还原）。

---

## 阶段 B：图引擎统一（sdk.Graph 成为唯一 DAG 引擎）

**构想**：`sdk.Graph`（已支持条件边/router/循环/并行/子图，节点执行走内核 `Submit`）
提升为**唯一图引擎**。`internal/workflow/graph` 保留"图结构定义"（genome 仍需要它描述结构），
但**执行**不再走 `internal/workflow.Runner`（第二引擎），而是编译为 `sdk.Graph` 交内核执行。
`api/graph` 删除。

### B1. 图结构→sdk.Graph 编译器（2 天）
**做什么**：新增 `internal/workflow/graph` → `sdk.Graph` 的编译函数
（节点映射为 agent/func 节点，边映射为 sdk.Graph 边+条件）。**不改 genome 的结构定义**，
只把它的"执行"入口从 `workflow.Runner` 换成"编译成 sdk.Graph 再 `RunGraph`"。

**调度维度裁定（2026-08-22 评审修订，防铁律 4 打折扣）**：
实测 `workflow/graph` 携带可插拔调度器（FIFO/Priority/SJF/RR/WeightedFair），且 genome 把
scheduler 作为可进化维度（`SchedulerGenomeName`）、存在 `PatchChangeScheduler` 能力。
`sdk.Graph` 按 v040 设计不做调度器（就绪即全并行批次）。处置：
1. `sdk.Graph` 新增可选字段 `MaxRoundConcurrency int`（errgroup SetLimit，0=不限），
   承载旧调度器的核心价值——并发节流；
2. 四种排序型策略（FIFO/Priority/SJF/RR/Fair 中除节流外的排序语义)**明示退役**：
   全并行批次下不存在"挑选谁先跑"的决策点，保留即空转；
3. genome 的 `scheduler` 维度同步退役，代码留 `TODO(tech-debt): retired with workflow.Runner,
   see aresos-fusion-plan §B1` 痕迹（规范 §0.3）；
4. 以上三点作为 B1 验收新增项：退役后有引用扫描零残留断言。
**验收**：
- [x] 单测：一个含条件分支+并行分支的 workflow graph 编译为 sdk.Graph 后，`RunGraph` 结果与旧 `Runner` 语义等价（同输入同输出，且并行分支真并行）。
- [x] 进化路径回归：`provide_new_evolution.go` 消费的 WorkflowGenome 经新路径执行，进化相关测试全绿。
- [x] `go test -race ./internal/workflow/... ./evolution/...` 全绿。

### B2. 退役第二执行引擎（1 天）
**做什么**：确认所有 workflow 执行都走 sdk.Graph 后，删除 `internal/workflow.Runner`
的执行循环（或改成 B1 编译器的薄封装）；`MaxParallel=1` 串行语义由 sdk.Graph 的
就绪批次自然表达。
**验收**：
- [x] 全仓引用扫描：`workflow.Runner` 的执行入口零生产引用。
- [x] 无第二引擎：静态检查确认节点执行唯一路径为 `Runtime.Submit`。
- [x] `go build ./... && go test -race ./...` 全绿。

### B3. 删除 api/graph（0.5 天）
**做什么**：删除 Deprecated 且零引用的 `api/graph`；examples 若有引用迁到 `sdk.Graph`。
**验收**：
- [x] `api/graph` 删除后 `go build ./... && go test ./...` 全绿。
- [x] examples 全部编译运行通过。

**阶段 B 出口**：图能力只剩 `sdk.Graph` 一套执行逻辑；结构定义与执行分离但共用一个引擎；
`api/graph` 消失；进化系统改走 sdk.Graph 无回归。

---

## 阶段 C：编排三模式统一到 sdk.Graph 并接入生产

**构想**：委托/流水线/编排三模式全部用 `sdk.Graph` 表达（委托=单边、
流水线=链、编排=扇出扇入+router）。删除 `agentipc/collaboration.go` 的三 API；
`cmd/ares` 的 topic 分发 handler 改为**构建并运行一个 sdk.Graph**。

### C1. 三模式在 sdk.Graph 上的等价实现 + 示例（1.5 天）
**做什么**：`docs/cookbook`（或 `examples/graph_demo`）用同一套 Graph API 写出三模式；
逐一对照旧 `DelegateToSpecialist`/`NewPipeline`/`Orchestrate` 的语义，证明无缺失。
**验收**：
- [x] 三模式各一可运行示例，输出符合预期。
- [x] 语义对照表：旧 API 每个能力项在 sdk.Graph 有对应写法（委托目标选择、流水线传值、编排扇入聚合+router）。
- [x] 编排模式含"扇入聚合"用例（呼应 router 首完成契约，用 Join 节点聚合而非依赖 router 多完成）。

### C2. topic 分发 handler 改驱动 sdk.Graph（2 天）
**做什么**：`evolution_ipc.go` 的 `topicDelegateTask` 等 handler 内部改为构建对应 sdk.Graph
并 `RunGraph`，替换原有直接分发逻辑。保持 topic 协议对外不变（兼容现有 IPC 客户端）。
**验收**：
- [x] IPC 集成测试：委托/流水线/编排三类 topic 消息经 handler → sdk.Graph 执行，结果与改造前等价。
- [x] `wireEvolutionIPC` 生产路径回归全绿。
- [x] `go test -race ./cmd/ares/ ./internal/agentipc/...` 全绿。

### C3. 删除 collaboration 三 API（0.5 天）
**做什么**：确认 C2 覆盖全部语义后，删除 `collaboration.go` 的
`DelegateToSpecialist`/`NewPipeline`/`Orchestrate`（及其仅测试引用的测试）。
**验收**：
- [x] 全仓引用扫描：三 API 零生产引用后删除。
- [x] `go build ./... && go test ./...` 全绿。

### C4. sdk.Graph 任务图端点接入 cmd/ares（1.5 天）
**做什么**：在 `cmd/ares` 暴露一个"提交任务图"的生产入口（HTTP 端点或 IPC topic，
复用现有 `submitPeerTask` 鉴权/审计中间件），让外部可直接提交 sdk.Graph 描述的 DAG，
由内核执行——这是"sdk.Graph 进主二进制"的最终闭环。
**验收**：
- [x] 端点集成测试：提交一个多节点 DAG（含并行+条件），内核执行，返回各节点结果。
- [x] 复用鉴权：端点走既有 `checkAuth`+审计，无旁路（安全回归）。
- [x] 输入防御（code_rules_v2 §9）：payload 大小上限、节点/边数复用 Graph builder
      硬帽（1024/4096）、携带 `schema_version` 字段并做版本校验；非法输入 400 带原因。
- [x] `go test -race ./cmd/ares/` 全绿。

**阶段 C 出口**：编排只剩 sdk.Graph 一套 API；三模式无折扣；topic 协议兼容；
主二进制可直接消费 sdk.Graph。

---

## 阶段 D：全局收敛验证与文档（1 天）

**做什么**：全仓验证"单引擎/单接口/全接入"三铁律；更新 CHANGELOG 与设计文档；
产出最终"一套接口"清单。
**验收**：
- [x] 引用矩阵：resurrection 插件、workflow.Runner、collaboration 三 API、api/graph **全部为 0 引用且已删除**。
- [x] 单引擎自证：全仓搜索节点执行入口，唯一路径为 `Runtime.Submit`（内核量子调度）。
- [x] 全接入自证：阶段 A/B/C 每个能力都能从 `cmd/ares` 生产路径追踪到调用链（文件:行号）。
- [x] 全量门禁：`gofmt -l`（空）、`go vet ./...`、`go build ./...`、`go test -race ./...` 全绿；
      `staticcheck ./...` 无死代码/空转告警；`golangci-lint run ./...` 0 issues（= make check 全绿）。
- [x] CHANGELOG 记录本次融合；设计文档 §7/§9 更新收敛边界与删除清单。

---

## 2. 里程碑与顺序

> **执行结果（2026-08-22）**：A/B/C/D 全部完成。B 阶段实测发现 Runner 本就零生产
> 调用，删除范围扩大为 root 执行栈 + 三 API 岛 + workflow CLI；C2 以"内核 fabric
> DAG 直驱"实现（协议不变），未引入第二引擎。自证材料见
> `docs/fusion-convergence.md`。

```
A（恢复统一）─→ B（图引擎统一）─→ C（编排统一+接线）─→ D（全局收敛验证）
   ↑ 独立            ↑ B 为 C 前置        ↑ 依赖 A/B          ↑ 收口
```

- **A 可最先做**（与图无关，直接补齐 agent 复活缺口，价值最高）。
- **B 必须在 C 前**（C 的三模式与 topic 都要落到 sdk.Graph 上）。
- 每阶段结束都是一个可发布的自洽状态；总量约 **16 人天**。

## 3. 风险与红线

| 风险 | 对策 |
|------|------|
| 进化系统依赖 `internal/workflow/graph` 结构 | B 阶段只换执行、不动结构定义；进化测试作为每步门禁 |
| 认知快照并发读写 race | A1 用 `-race` 集成测试守护；快照合并单线程化 |
| topic 协议破坏现有 IPC 客户端 | C2 保持协议不变，仅换内部实现；集成测试对比改造前后 |
| 删包误伤 | 每次删除前全仓引用扫描（生产+测试），删后全量 build+test |
| "打折扣"回归 | 每阶段出口都有"语义等价"断言（复活认知还原、三模式对照、DAG 条件/并行） |

## 4. 完成定义（DoD）

融合完成当且仅当：
1. resurrection 插件、workflow.Runner 执行循环、collaboration 三 API、api/graph **全部删除**；
2. 恢复=1 套（aresrecovery，含 agent 认知复活）、图=1 套（sdk.Graph）、编排=1 套（sdk.Graph）；
3. agent 复活/动态编排/动态 DAG/记忆蒸馏/进化 **全部**可从 `cmd/ares` 生产路径追踪到；
4. 全量门禁 + staticcheck 全绿，无空转、无第二引擎、无重复接口；
5. 功能对照表逐项确认无折扣。

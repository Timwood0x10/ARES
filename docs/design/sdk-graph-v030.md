# sdk 动态图编排设计（v0.3.0 已交付）

> **状态：已实现（v0.3.0）** · 代码：`sdk/graph.go` + `sdk/graph_run.go` + `sdk/graph_test.go`
> 归属：v0.3.0（原计划 v0.4.0 M1，用户决策提前到 v0.3.0 随发布交付）
> 关联路线图：`docs/analysis-reports/v0.4.0-feature-suggestions-corrected.md` 方向一
> 背景：v0.3.0 将旧 `api/graph`（内部实现 `internal/workflow/graph`）标记废弃后，
> **sdk 没有提供图编排的等价物**。本设计将 graph 的精华以极简 API 回归 sdk，
> 而不是让这套绝妙的动态编排能力随废弃而消失。

---

## 1. 为什么要回归：旧 graph 不可丢弃的精华

旧 `internal/workflow/graph` 的「动态」是三层叠加的，缺一不可：

### 1.1 NodeRouter —— 动态路由（灵魂）

```go
// 节点执行完成后回调；返回非空节点 ID 则绕过静态边直接指定下一个节点；
// 返回 "" 则按 DAG 静态边（in-degree BFS）推进。
type NodeRouter func(ctx context.Context, currentNodeID string, state *State) string
```

路由决策权完全交给调用方：运行时可以**跳转、循环、回退**，图不只是静态拓扑，
而是「静态边 + 运行时决策」的混合骨架。这是旧设计最绝的部分。

### 1.2 Condition 条件边 —— 数据驱动的分支

```go
type Condition func(state *State) bool   // 边上的谓词，决定走哪条出边
func IfFunc(fn func(state *State) bool) Condition
```

每个量子后根据共享 State 动态选边，分支/合并天然可表达。

### 1.3 GraphPatchExecutor —— 运行时改图（进化的落点）

```go
// 进化系统对 DAG 打运行时补丁：InsertNode / RemoveNode / ReplaceNode /
// AddEdge / RemoveEdge / ChangeScheduler
type GraphPatchExecutor struct { graph *Graph }
```

**图结构本身可以在运行时被演化系统改变**——这是「动态图」的终极形态：
进化不只改参数（策略），还改拓扑（图）。

### 1.4 其余有价值的特性

| 特性 | 价值 |
|------|------|
| `SubGraphNode` | 节点内嵌子图，递归组合，层级化编排 |
| 5 种 Scheduler（FIFO/Priority/ShortJob/RoundRobin/WeightedFair） | 调度策略与图结构解耦，可插拔 |
| `CompileBound` / `Runner` | 编译绑定 + 统一执行引擎（checkpoint/事件/插件） |
| tracer / limiter / pluginBus / checkpointStore | 可观测、限流、钩子、断点恢复齐全 |

---

## 2. 设计目标与原则

1. **保留动态精髓**：NodeRouter + Condition + 可插拔调度 + 运行时改图，一个都不能少。
2. **API 极简**：sdk 是唯一公共入口，新增符号控制在 10 个以内（含类型与方法），
   与现有 `sdk` 风格一致（选项式、少概念）。
3. **接内核 fabric**：图执行走 `RegisterAgent`/`Submit` 同一条内核调度路径
   （`internal/kernelscheduler` + `internal/taskfabric`），不另起炉灶——
   节点 = capability 任务，边的就绪检查 = `fabric.IsReady(depsCompleted)`。
4. **覆盖 M1 三种模式**：委托 / 流水线 / 编排都是图的特例（见 §5）。
5. **迁移路径平滑**：旧 `api/graph` 在 v0.3.0 保持 Deprecated 兼容；`sdk.Graph`
   随 v0.3.0 上线，旧包进入移除倒计时（v0.4.0 移除）。

---

## 3. 目标 API（草案）

```go
package sdk

// Graph 是动态编排的一等公民。节点执行、边条件、路由回调全部可选——
// 最小用法（AddNode + AddEdge）就是一个普通 DAG。
type Graph struct { /* 内部：节点表、边表、router、scheduler 选项 */ }

// NewGraph 创建空图。
func NewGraph(id string) *Graph

// AddNode 添加一个可执行节点。exec 可以是 *Agent（rt.NewAgent 结果）、
// 函数 func(ctx, state) error，或任何实现 GraphNode 接口的类型。
// state 是整图的共享状态（map[string]any），节点间传递数据。
func (g *Graph) AddNode(id string, exec any) *Graph

// AddEdge 添加条件边；cond 为 nil 表示无条件直连。
// cond 在源节点完成后求值，决定是否走这条边。
// 多入边节点（Join）默认 JoinAll：所有入边源节点都 settle（done/failed/skipped）
// 且至少一条入边存活（无条件恒活 / 条件满足）才就绪；Router 可覆盖该语义。
func (g *Graph) AddEdge(from, to string, cond func(state map[string]any) bool) *Graph

// SetRouter 设置动态路由（旧 NodeRouter 精髓）：节点完成后回调，
// 返回非空节点 ID 则优先执行该节点，返回 "" 则回落到静态边。
func (g *Graph) SetRouter(fn func(ctx context.Context, currentNodeID string, state map[string]any) string)

// RunGraph 执行图直到没有可执行节点（或上下文取消）。返回每节点结果。
// 执行经内核调度：每个节点作为 capability 任务走 fabric 量子调度。
func (r *Runtime) RunGraph(ctx context.Context, g *Graph) (*GraphResult, error)

// GraphResult 记录每节点结果与最终共享 State。
type GraphResult struct {
    NodeResults map[string]*Result   // 节点 ID → 执行结果
    State       map[string]any       // 最终共享状态
}
```

要点：
- **不暴露 Scheduler 接口**：默认用内核调度器（`internal/kernelscheduler`），
  需要换策略时经 `sdk` 选项（如 `WithGraphScheduler(kind)`）选择 FIFO/Priority/WeightedFair 等，
  符号不膨胀。
- **不暴露 State 类型**：用 `map[string]any` 取代旧 `*graph.State`，减少概念。
- **SubGraphNode 保留**：`AddNode(id, subGraph)` 传入另一个 `*sdk.Graph` 即递归组合，
  不需额外类型。
- **运行时可改图（v0.3.0 语义收敛）**：`AddNode/AddEdge/RemoveNode/RemoveEdge`
  设计为**可安全并发调用**（`RWMutex` 保护，无数据竞争），但 `RunGraph` 在
  **入口拍一次固定快照**，整个执行过程不再重新读图——结构变更不作用于
  进行中的运行，从下一次 `RunGraph` 生效。Evolution 运行时图补丁
  （GraphPatchExecutor 等价物）留到后续里程碑（届时改为每轮 re-snapshot）。

---

## 4. 与内核调度器的接线

```
sdk.Graph
   │
   ▼
sdk 编排器（graphOrchestrator，纯 sdk 侧，逐层推进）
   │  每轮：算就绪批次 → 为每个就绪节点提交一个单任务 → 等终态 → 合并 state → 求 router
   ▼
r.Submit(ctx, sdk.Task)          ← 完全复用现有单任务提交链路
   │
   ▼
internal/kernelscheduler（Schedule → Acquire → RunQuantum → Yield，量子调度）
   │
   ▼
*sdk.Agent / 函数节点执行器（经 RegisterAgent 注册的 sdkAgentExecutor）
```

**复用点（这是"闭环"的关键）**：编排器提交单节点用的就是 `Runtime.Submit` 已有的那条路
（`submitThroughScheduler` → fabric Create/Schedule → kernel drain → 结果回流），
**一行执行引擎代码都不用新写**。graph 只在 Submit 之上加一层「决定何时提交哪些节点」的
纯 sdk 协调逻辑。

**执行模型（关键澄清，与旧 graph 不同）**：fabric 的边只有「静态依赖」一种语义
（`Task.Dependencies` 全部 COMPLETED 即 `IsReady`），**没有** Condition / NodeRouter
的运行时 hook。因此 `sdk.Graph` 不把整图一次性 Create 进 fabric，而是由一个 **sdk 侧的
极简编排器逐层驱动**：

1. 编排器维护三样东西：`done`（已完成节点集）、`state`（`map[string]any` 共享状态）、
   `iter`（每节点已执行次数，用于循环上限）。
2. 每一轮计算「就绪批次」= 未完成节点中，所有入边源节点都已 settle
   （done/failed/skipped）、且至少一条入边未被判死（无条件边恒活；条件边在源
   完成后求值）的节点——即 **JoinAll 语义**（多入边必须全部源完成才就绪）。
3. 就绪批次里的每个节点各 Create 一个 **无依赖的** fabric 单任务（就绪已由编排器判定），
   一批同时提交——fabric 的并发 drain 天然让它们并行执行（复用 `WithMaxConcurrent`）。
4. 等这批任务全部到达终态，把各节点输出写回 `state`，加入 `done`。
5. 若设置了 `SetRouter`，用 `router(ctx, 刚完成的节点, state)` 的返回值决定是否强制把某个
   节点插入下一批（返回 `""` 则回落到静态边 BFS）——这就是旧 `NodeRouter` 的精髓。
6. 无就绪节点且 router 无指定时结束。循环（A→B→A）由 router 或自环条件边表达，
   `MaxIterations`（默认 100）+ 图级 `timeout` 兜底防死循环（§6）。

**职责边界**：fabric 只负责「执行一个节点任务」（Schedule→Acquire→RunQuantum→结果回流，
全部复用现有 `submitThroughScheduler` 链路）；**图拓扑推进 / 条件 / 路由 / 循环 / 共享 state
全部集中在 sdk 编排器一处**。不引入第二个执行引擎（节点执行仍是 fabric quantum），满足
§7 单引擎原则；也不把图逻辑拆散到「fabric Dependencies + sdk cond」两处，保持简洁。

---

## 5. 覆盖 M1 三种协作模式

| M1 模式 | 旧路线图描述 | 在 sdk.Graph 中的表达 |
|---------|-------------|----------------------|
| 委托模式 | Leader 委托 Specialist，聚合结果 | 图：leader → specialist1 → specialist2 → leader（Router 或条件边控制顺序） |
| 流水线模式 | A → B → C，支持并行无依赖 Stage | 图：AddEdge(A,B), AddEdge(A,C) 即并行；串行链即流水线 |
| 编排模式 | Coordinator 协调多个 Worker 并行，聚合重试 | 图：coordinator 出多条边到 workers，Join 节点聚合；失败重试由内核 RetryPolicy 接管 |

即：**一个 Graph API 统一覆盖三种模式**，不再需要 `DelegateToSpecialist` /
`CreatePipeline` / `Orchestrate` 三个独立 API——这就是极简。

---

## 6. 安全边界与限制

| 限制 | 策略 |
|------|------|
| **无限循环** | 每节点 `MaxIterations` 硬上限（默认 100），图级 `timeout` 兜底 |
| **图过大** | 节点数 ≤ 1024（硬上限），边数 ≤ 4096 |
| **并发改图** | `AddNode/AddEdge/RemoveNode/RemoveEdge` 用 `RWMutex` 保护（无数据竞争）；`RunGraph` 入口拍一次固定快照，结构变更从下一次运行生效 |
| **节点 panic** | 内核 `RunQuantum` 的 panic recovery 天然捕获：节点失败 → 编排器记录错误 → 出边不触发 → 图结束 |
| **State 数据竞争** | 节点运行期**不直接写共享 state**：各节点只返回自身 `*Result`，由编排器在「本批全部终态后」单线程合并回 `state`（§4 步骤 4）。故并行批次下 `map[string]any` 无并发写 |

---

## 7. 与旧 api/graph / internal/workflow/graph 的关系（迁移路径）

| 阶段 | 版本 | 动作 |
|------|------|------|
| 现状 | v0.3.0 | `api/graph` 标记 Deprecated；`internal/workflow/graph` 被 `api/graph` 及进化系统引用，保留 |
| 实现 | v0.3.0（本版交付） | sdk 新增 `Graph`（`sdk/graph.go` + `sdk/graph_run.go`）；节点执行走 `Runtime.Submit`（fabric），**不复用** `internal/workflow/graph` 的图结构或 Runner，两套彻底解耦 |
| 并行 | v0.3.0 | `api/graph` 继续可用（Deprecated 但兼容），示例迁移到 `sdk.Graph`；`internal/workflow/graph` 原地保留给进化系统 |
| 移除 | v0.4.0 | 删除 `api/graph`；`internal/workflow/graph` 仅在进化系统也迁移后才收敛（独立评估，不在本设计范围） |

注意：`internal/workflow/graph` 与 `internal/taskfabric` 并存期间，sdk.Graph 的执行
**必须**走 fabric（与 Submit 同一路径），不得保留两套执行引擎——这是
「H1/H2: 合并 SDK 和 kernel 两条路径」既定原则的延续。

---

## 8. 详细迁移计划（v0.3.0 交付，实施记录）

原则：**只加一个文件 `sdk/graph.go`（必要时 `sdk/graph_test.go`），执行引擎零新增，
全部复用现有 `Runtime.Submit`**。每步都能独立编译通过、独立验收，可分多次提交。

### 步骤 0：接线勘探（0.5 天，无产出代码）

**做什么**：确认 `Runtime.Submit` 的确切签名、`sdk.Task` / `sdk.Result` 字段、
`RegisterAgent` 如何把 `*Agent` 与 capability 绑定、`Result` 里节点输出放在哪个字段。
把这些写进本节「接线事实」小抄（见步骤末）。

**验收标准**：
- [ ] 在文档追加一段「接线事实」：列出 `Submit(ctx, Task) (*Result, error)` 真实签名、
      `Task` 构造所需最小字段、`Result` 输出字段名——全部带 `文件:行号`。
- [ ] 确认单任务 `Submit` 在无依赖时是同步等终态返回，还是需自旋等待（决定步骤 3 等待写法）。

### 步骤 1：数据结构 + 构建期 API（0.5 天）

**做什么**：在 `sdk/graph.go` 定义 `Graph` 结构（`nodes map[string]*graphNode`、
`edges []graphEdge`、`router`、`mu sync.RWMutex`、`maxIterations int`、`timeout time.Duration`），
实现 `NewGraph / AddNode / AddEdge / SetRouter`。`graphNode.exec any` 暂只存不解析。

**验收标准**：
- [ ] `go build ./sdk/ && go vet ./sdk/` 通过。
- [ ] 新增导出符号计数 ≤ 8（`Graph`、`NewGraph`、`AddNode`、`AddEdge`、`SetRouter`、
      `RunGraph`、`GraphResult`，以及可选 `WithGraphScheduler`）——用 `go doc ./sdk | grep -c` 自证。
- [ ] `AddNode`/`AddEdge` 对重复 id、未知端点、超过 §6 上限（1024 节点 / 4096 边）返回明确错误
      或 panic（构建期 fail-fast，二选一并注释说明）。
- [ ] 单测：构图后 `len(nodes)`/`len(edges)` 正确；非法构图被拒。

### 步骤 2：节点执行器适配（1 天）

**做什么**：实现 `resolveNode(n *graphNode) func(ctx, state) (*Result, error)`，
把三种 `exec` 归一：
- `*Agent` → 复用现有 `RegisterAgent` + `Submit`（节点 = 一个 capability 任务）；
- `func(ctx, state map[string]any) (any, error)` → 包一个匿名 agent/capability 提交；
- `*Graph`（子图）→ 递归调用 `RunGraph`，把子图 `GraphResult.State` 并回父 state。

**验收标准**：
- [ ] 三类节点各有一条最小单测：单 `*Agent` 节点图、单函数节点图、单子图节点图，
      `RunGraph` 后 `NodeResults[id]` 有值且 `err == nil`。
- [ ] `*Agent` 节点走的是 `Runtime.Submit`（用 event/日志或断言 scheduler 计数验证，
      证明"复用而非另起引擎"）。
- [ ] `go test -race ./sdk/ -run TestGraphNode` 通过。

### 步骤 3：编排器主循环（静态 DAG，2 天）——核心

**做什么**：实现 `graphOrchestrator`（§4 六步）。先只做**无条件边 + 无 router**的纯 DAG：
算就绪批次 → 并发提交本批各节点（`errgroup` 或 `sync.WaitGroup` + 每节点 `Submit`）→
等本批全部终态 → 合并各 `Result` 到 `state`、加入 `done` → 下一轮。`RunGraph` 组装
`GraphResult` 返回。

**验收标准**：
- [ ] 串行链 A→B→C：执行顺序严格 A、B、C，`State` 逐节点累积正确。
- [ ] 扇出扇入 A→{B,C}→D：B、C **并行**执行（用时间戳或并发计数断言重叠），D 等两者完成。
- [ ] 节点失败：B 返回 error → D（依赖 B）不执行，`GraphResult` 标记 B 失败、
      `RunGraph` 返回聚合错误，且**不 panic、不死锁**。
- [ ] `go test -race ./sdk/ -run TestGraphDAG` 全绿（含并行用例，race 必跑）。
- [ ] 空图 / 单节点 / 有环但无 router（应被 MaxIterations 或环检测拦下）都有确定行为。

### 步骤 4：条件边 + Router（1.5 天）——动态精髓

**做什么**：就绪判定加入 `edge.cond(state)` 求值（nil=无条件）；每批完成后，
若 `router != nil` 则对刚完成节点调用 `router(...)`，返回非空 id 强制加入下一批（绕过静态边）。
`iter[id]++`，超过 `maxIterations` 则该节点不再入队。

**验收标准**：
- [ ] 条件分支：A 后按 `state["x"]` 走 B 或 C（只有一条边 cond 为真），另一分支不执行。
- [ ] Router 循环：A→B，router 在 B 完成后返回 "A" 直到 `state` 满足条件，
      循环次数正确且被 `MaxIterations` 兜底（构造死循环用例，断言不超限、不 OOM）。
- [ ] Router 返回 "" 时严格回落到静态边 BFS（与步骤 3 行为一致）。
- [ ] `go test -race ./sdk/ -run TestGraphDynamic` 全绿。

### 步骤 5：安全边界 + 运行时改图（1 天）

**做什么**：落实 §6 全部限制：`WithGraphTimeout` / `MaxIterations` / 节点边上限；
构建期 API 加 `RWMutex`；`RunGraph` 入口 `RLock` 拍一次固定图快照（节点/边切片
浅拷贝），整个执行过程用这份快照——并发 `AddNode/AddEdge` 安全（无数据竞争），
但结构变更不作用于进行中的运行（为未来 Evolution 图补丁留落点，本期不接进化）。

**验收标准**：
- [ ] 图级 timeout 到期 `RunGraph` 返回 `context.DeadlineExceeded` 且已提交节点被取消。
- [ ] 超节点/边上限构建被拒（复用步骤 1 校验）。
- [ ] 并发测试：一个 goroutine 跑 `RunGraph`，另一 goroutine 并发 `AddNode`，
      `go test -race` 无数据竞争（验证快照语义）。
- [ ] 节点 panic 被 `RunQuantum` recovery 捕获，图按"失败节点"路径收尾（呼应 §6）。

### 步骤 6：三种协作模式示例 + 文档（1 天）

**做什么**：`docs/cookbook`（或 `examples/`）新增一个 `graph_demo`，用**同一套 Graph API**
演示委托 / 流水线 / 编排三种模式（§5）。把 `api/graph` 现有的 `examples/graph_demo`
迁到 `sdk.Graph` 写法。

**验收标准**：
- [ ] 示例 `go run` 可跑通，输出符合预期，无需引用任何 `internal/` 或 `api/graph`。
- [ ] 三种模式各一段，代码行数对比旧 `DelegateToSpecialist`/`CreatePipeline`/`Orchestrate`
      三 API 的等价写法，证明"一个 API 覆盖三模式"。
- [ ] README / godoc 注释完整，`go doc ./sdk Graph` 输出可读。

### 步骤 7：旧包收敛评估（0.5 天，仅评估不删）

**做什么**：跑一次全仓引用扫描，确认 `sdk.Graph` 上线后 `api/graph` 的唯一生产引用
（examples）已迁走；生成一份"v0.4.0 删除清单"，明确 `internal/workflow/graph`
仍被 `evolution/genome`、`evolution/diff`、`ares_bootstrap` 引用**因此本期不动**（见 §7、§9）。

**验收标准**：
- [ ] 产出引用矩阵：`api/graph`、`internal/workflow/graph` 各自的生产/测试引用点（带文件:行号）。
- [ ] 确认删除 `api/graph` 后 `go build ./... && go test ./...` 仍全绿（可在临时分支验证后还原）。
- [ ] 明确写下：v0.4.0 **不删** `internal/workflow/graph`（进化系统仍依赖），仅将 `api/graph`
      标注"下版移除"。
**总量**：约 8 人天，单文件为主，无引擎改动，每步可独立提交与回归。

---

## 9. 决策记录

- **2026-08-22**：v0.3.0 发布前复盘发现 sdk 无图编排等价物；用户确认旧 graph
  （尤其 NodeRouter + GraphPatchExecutor 动态能力）是核心资产，不应丢弃。
  决定：v0.3.0 内交付（原计划 v0.4.0 M1，用户决策提前），sdk 内以极简 API 回归图编排，
  旧包保持 Deprecated 兼容。
- **API 取舍**：不暴露 Scheduler 接口、不暴露 State 类型（用 map[string]any）、
  不暴露编译/插件/限流细节——全部走 sdk 选项或内核默认，保持符号 ≤ 10 个新增。
- **GraphPatchExecutor 等价物**：由「运行时可安全改图」+ 既有 `rt.Evolve` 补丁框架
  共同实现，不在 sdk 暴露补丁类型，进化系统内部驱动图结构变更。
- **2026-08-22（架构定案）**：复核代码后确认 fabric 的边**只有静态依赖语义**，
  无 Condition/NodeRouter 运行时 hook；旧 graph 也已不自带执行循环（跑的是
  `internal/workflow.Runner`，`MaxParallel=1` 串行）。若让 `sdk.Graph` 直接复用
  `CompileBound`+Runner，等于承认两套执行引擎，违背 H1/H2。**决定采用单引擎方案**：
  节点执行一律走 `Runtime.Submit`（fabric 量子调度），图拓扑/条件/路由/循环/共享 state
  全部收敛到一个 sdk 侧 `graphOrchestrator`（§4）。执行引擎**零新增代码**，只在 Submit
  之上加一层纯协调逻辑。
- **2026-08-22（简洁性约束）**：实现限定为**单文件 `sdk/graph.go`**，新增导出符号 ≤ 8；
  不暴露 Scheduler/State/编译器/补丁类型；不为委托/流水线/编排各造 API，统一用 Graph 表达。
- **2026-08-22（复用与不动边界）**：`internal/workflow/graph` 因仍被 `evolution/genome`、
  `evolution/diff`、`ares_bootstrap/provide_new_evolution.go` 生产引用，**v0.3.0 一律不动**；
  仅 `api/graph`（纯 Deprecated 转发，唯一生产引用是 examples）在示例迁移后标注下版移除。
  这样旧进化链路零风险，新图能力独立闭环。
- **2026-08-22（Join 语义定案）**：多入边节点默认 **JoinAll**——所有入边源节点都 settle
  （done/failed/skipped）且至少一条入边存活（无条件恒活 / 条件满足）才就绪；
  条件边在源节点完成后求值（出边条件），false 标记边死，入边全死的节点级联跳过（settleSkips）；
  Router 返回非空可覆盖静态边（v1 限制：仅当轮第一个完成节点触发 router 决策）。

---

## 10. 接线事实小抄（步骤 0 产出）

以下事实在实现前确认，作为编排器接线的 ground truth：

| 事实 | 值 | 文件:行号 |
|------|-----|-----------|
| `Submit` 签名 | `func (r *Runtime) Submit(ctx context.Context, t Task) (*Result, error)` | `sdk/task.go:68` |
| `Task` 最小字段 | `Capability string`, `Input string`（`ID`/`Timeout` 可选） | `sdk/task.go:13-25` |
| `Result` 输出字段 | `Output string`（LLM 返回内容） | `sdk/agent.go:113-119` |
| `RegisterAgent` 绑定 | `agentByCapability[capability] = a; sdkExecutors[capability] = &sdkAgentExecutor{agent: a}` | `sdk/task.go:38-52` |
| `Agent.name` | 未导出，同包 `graph.go` 直接访问 `v.name` 作为 capability | `sdk/agent.go:25` |
| `Submit` 等终态方式 | **自旋等待**：10ms ticker 轮询 `fabric.Task(state)`，非同步阻塞 | `sdk/scheduler.go:133-154` |
| `ensureScheduler` | 懒启动：首次 `Submit` 时 `schedOnce.Do` 创建 fabric + scheduler | `sdk/scheduler.go:80-93` |
| 函数节点不走 fabric | 纯计算节点 `n.fn` 直接 inline 执行，不调 `Submit`（无 LLM 调度必要） | `sdk/graph_run.go:189` |
| `*Agent` 节点走 fabric | `bindGraphAgent` → `r.Submit(ctx, Task{Capability: n.agentName, Input: input})` | `sdk/graph_run.go:194-195` |
| 子图节点递归 | `r.runGraphRounds(ctx, subSnap, child, ...)` 共享父 state | `sdk/graph_run.go:239` |

## 11. 步骤 7：旧包收敛评估

**引用矩阵（v0.3.0 实现后）**：

| 包 | 生产引用 | 测试引用 | 动作 |
|----|----------|----------|-------------|
| `api/graph` | `examples/graph_demo/main.go`（已迁到 `sdk.Graph`） | 无 | 标注 Deprecated，v0.4.0 删除 |
| `internal/workflow/graph` | `evolution/genome`, `evolution/diff`, `ares_bootstrap/provide_new_evolution.go` | `internal/workflow/graph/*_test.go` | **不动**（进化系统仍依赖） |

**结论**：
- `examples/graph_demo` 已从 `api/graph` 迁移到 `sdk.Graph`，不再引用 `api/graph`。
- `api/graph` 的唯一生产引用已清零；v0.4.0 可安全删除。
- `internal/workflow/graph` 被进化系统多处引用，**v0.4.0 不删**，等进化系统迁移后独立评估。

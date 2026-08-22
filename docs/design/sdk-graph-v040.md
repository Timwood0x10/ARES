# sdk 动态图编排设计（v0.4.0 M1 输入）

> **状态：设计提案（未实现）** · 归属：v0.4.0 M1 多 Agent 协作模式（P2 必做）
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
5. **迁移路径平滑**：旧 `api/graph` 与 `internal/workflow/graph` 在 v0.4.0 内保留，
   新 `sdk.Graph` 上线后旧包进入移除倒计时（v0.5.0 移除）。

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
- **运行时可改图**：`AddNode/AddEdge/RemoveNode/RemoveEdge` 设计为可安全并发调用，
  执行引擎每个量子边界重新读图结构（RWMutex 保护），为 v0.4.0 Evolution
  图补丁（GraphPatchExecutor 等价物）留好落点。

---

## 4. 与内核调度器的接线

```
sdk.Graph ──编译──▶ []sdk.Task（节点 = capability 任务）
                        │
                        ▼
              internal/taskfabric.Fabric
              （Dependencies 表达边依赖；IsReady = depsCompleted）
                        │
                        ▼
              internal/kernelscheduler
              （Schedule → Acquire → RunQuantum → Yield，量子调度）
                        │
                        ▼
        *sdk.Agent / 函数节点执行器（经 RegisterAgent 注册）
```

- 边的就绪语义 = `fabric.IsReady`（所有依赖 COMPLETED 且自身 READY）；
- 条件边在源节点完成后求值，不满足则跳过该边（节点可能因此不触发）；
- NodeRouter 优先级最高：返回非空 ID 则下一个量子直接执行该节点，否则走静态边 BFS；
- 循环（A → B → A）天然支持：Router 返回 A 即可，或加自环条件边（需防无限循环，
  用 `MaxIterations` 或 quantum 预算保护，默认限制见 §6）。

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
| **并发改图** | `AddNode/AddEdge/RemoveNode/RemoveEdge` 用 `RWMutex` 保护；执行引擎每个量子开始前读当前图快照 |
| **节点 panic** | 内核 `RunQuantum` 的 panic recovery 天然捕获：节点失败 → 出边不触发 → 图结束 |
| **State 数据竞争** | 量子调度单线程 acquire，同一时刻最多一个节点运行，`map[string]any` 读写天然串行化 |

---

## 7. 与旧 api/graph / internal/workflow/graph 的关系（迁移路径）

| 阶段 | 版本 | 动作 |
|------|------|------|
| 现状 | v0.3.0 | `api/graph` 标记 Deprecated；`internal/workflow/graph` 被 `api/graph` 引用，保留 |
| 实现 | v0.4.0 M1 | sdk 新增 `Graph` 相关符号；内部编译目标优先复用 `internal/workflow/graph` 的图结构，执行层迁到 fabric 量子调度 |
| 并行 | v0.4.0 | `api/graph` 继续可用（Deprecated 但兼容），示例迁移到 `sdk.Graph` |
| 移除 | v0.5.0 | 删除 `api/graph` 与 `api/workflow`；`internal/workflow/graph` 若仅被 api/graph 使用则一并收敛 |

注意：`internal/workflow/graph` 与 `internal/taskfabric` 并存期间，sdk.Graph 的执行
**必须**走 fabric（与 Submit 同一路径），不得保留两套执行引擎——这是
「H1/H2: 合并 SDK 和 kernel 两条路径」既定原则的延续。

---

## 8. 实施计划（v0.4.0 M1 内）

| 步骤 | 内容 | 验证 |
|------|------|------|
| 1 | `sdk/graph.go`：Graph/NewGraph/AddNode/AddEdge/SetRouter/RunGraph/GraphResult | 编译 + vet |
| 2 | 执行器：节点适配（*Agent / func / 子图）→ sdkAgentExecutor 复用 | 单节点图测试 |
| 3 | 边编译：AddEdge → fabric Dependencies；条件边求值器 | DAG 依赖测试 |
| 4 | Router 集成：量子间路由决策点（fabric 就绪检查后） | 动态跳转/循环测试 |
| 5 | 运行时改图：RWMutex + 图快照语义 | 并发改图测试 |
| 6 | 安全限制：MaxIterations/节点上限/超时 | 极限测试 |
| 7 | 迁移示例：docs/cookbook 新增 Graph 三个模式 demo（委托/流水线/编排） | 示例可运行 |
| 8 | 移除旧 api/graph（v0.5.0 边界，M1 末尾评估） | 删除后全量测试 |

---

## 9. 决策记录

- **2026-08-22**：v0.3.0 发布前复盘发现 sdk 无图编排等价物；用户确认旧 graph
  （尤其 NodeRouter + GraphPatchExecutor 动态能力）是核心资产，不应丢弃。
  决定：v0.3.0 保持现状（旧包 Deprecated 兼容），本设计作为 v0.4.0 M1 输入，
  sdk 内以极简 API 回归图编排。
- **API 取舍**：不暴露 Scheduler 接口、不暴露 State 类型（用 map[string]any）、
  不暴露编译/插件/限流细节——全部走 sdk 选项或内核默认，保持符号 ≤ 10 个新增。
- **GraphPatchExecutor 等价物**：由「运行时可安全改图」+ 既有 `rt.Evolve` 补丁框架
  共同实现，不在 sdk 暴露补丁类型，进化系统内部驱动图结构变更。

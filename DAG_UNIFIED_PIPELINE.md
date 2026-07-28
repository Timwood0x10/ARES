# DAG 统一生产管线设计

## 1. 结论

当前 `internal/workflow/engine` 与 `internal/workflow/graph` 不应继续作为两套并行运行时长期存在。

目标不是简单兼容两个模块，也不是把 `graph.Graph` 机械转换成 `engine.Step[]`，而是：

> 提炼双方最佳能力，淘汰冲突和薄弱语义，通过统一 Workflow IR、ExecutionScope 和单一 Runner 建立唯一生产管线，并让 Knowledge、Memory、Experience、Evolution、Observability、Checkpoint 与 Recovery 全部接入同一个运行生命周期。

最终形态：

```text
api/workflow Builder / YAML / legacy engine API
                              │
legacy graph API ─────────────┤
                              ▼
                     Workflow Spec / IR
              Node + Edge + Condition + Join
                              │
                       Compile + Validate
                              │
                 ┌────── ExecutionScope ──────┐
                 │ State / Knowledge / Memory │
                 │ Event / Trace / Checkpoint │
                 └────────────┬───────────────┘
                              ▼
                    Scheduler + Single Runner
                              │
                    Mutable Graph + PatchQueue
                              │
             Evolution / Experience / Observability
```

旧 API 在迁移期间可以保留，但只能作为 IR 的构建前端，不再拥有独立执行语义。

---

## 2. 当前问题

当前实际上存在至少三条执行路径：

```text
api/client/workflow  → engine.Executor
api/service/workflow → engine.DynamicExecutor
api/graph            → graph.Graph.Execute
```

它们不仅实现重复，而且在条件、跳过、路由、状态、调度和动态变异等关键语义上不一致。

### 2.1 条件与跳过语义冲突

| 运行时 | 条件位置 | 条件不满足 | 对下游的影响 |
|---|---|---|---|
| `graph.Graph` | Edge | 目标不入 ready queue，无结果记录 | 后代可能静默不可达 |
| `engine.Executor` | Step | 记录为 skipped，并加入 completed | 下游继续执行 |
| `engine.DynamicExecutor` | Step | 记录为 processed，但不加入 completed | 直接下游可能一直等待 |

证据：

- `internal/workflow/graph/executor.go:418-429`
- `internal/workflow/graph/executor.go:527-543`
- `internal/workflow/engine/executor.go:455-498`
- `internal/workflow/engine/dynamic_executor.go:454-531`

### 2.2 Router 语义冲突

| 运行时 | Router 行为 |
|---|---|
| `graph.Graph` | 可将目标直接加入 ready queue，绕过静态入度；静态后继仍继续处理 |
| `engine.Executor` | 可绕过 `DependsOn`；不会自动取消其他分支 |
| `engine.DynamicExecutor` | 只调整执行顺序，目标仍需满足依赖 |

一个 `Router` 名称实际表达了三种不同契约，无法靠统一函数签名解决。

### 2.3 状态模型冲突

`graph` 使用共享可变状态：

```go
type State struct {
    values map[string]any
}
```

Node 可以任意读写整份 State。该模型依赖 graph 的单线程执行假设，无法安全地直接进入 engine 的并发模型。

engine 则把状态拆为：

- `WorkflowExecution.Variables`
- `OutputStore`
- 按 Step 隔离的字符串输出
- `StepState` / `StepResult`

Graph 的任意结构化输出、`FuncNode` 写入和 `SubGraphNode` 全量 State 合并，无法无损压缩成一个字符串 Step 输出。

### 2.4 Start 语义不真实

`Graph.Start()` 只保存一个 ID 并在执行前验证存在，但 ready queue 实际从全图所有零入度节点生成。

证据：

- `internal/workflow/graph/graph.go:179-195`
- `internal/workflow/graph/executor.go:106-115`
- `internal/workflow/graph/executor.go:182-193`

因此当前 Start 不是执行可达域的真正入口。

### 2.5 Patch 已经发生双模型漂移

当前 Evolution 路径同时存在：

- 操作私有 no-op Graph 的 `GraphPatchExecutor`
- 操作真实 `MutableDAG` 的 live patcher

同一个 AddEdge patch 在不同执行器中甚至期望不同 payload 结构。这说明弱类型的 `Target + Value any` 不能继续作为跨系统协议。

---

## 3. 能力取舍

### 3.1 从 engine 保留

以下能力进入统一内核：

1. `MutableDAG` 的线程安全、版本号和运行期变异能力；
2. DynamicExecutor 的并行派发基础；
3. HITL；
4. Retry 与 Step Recovery；
5. Loop；
6. Checkpoint 与 Resume；
7. SubWorkflow；
8. AgentRegistry；
9. Plugin 生命周期；
10. ExecutionCollector；
11. 版本感知的 safe point 重排。

### 3.2 从 graph 吸收

以下能力进入统一定义层和调度层：

1. 显式 `Node + Edge` 图模型；
2. Edge 作为一等公民；
3. 条件边；
4. Agent、Tool、Func、SubGraph 等 Node binding；
5. Scheduler 策略接口；
6. 链式 Builder；
7. 显式分支结构的可读性；
8. State 面向用户的便利访问方式；
9. 图级 tracing 与 limiter 配置体验。

### 3.3 必须淘汰

以下实现或语义不进入新管线：

1. `Graph.Execute` 第二套执行循环；
2. 静态 `engine.Executor` 独立生产路径；
3. synthetic/no-op GraphPatchExecutor；
4. Router 隐式绕过依赖；
5. `Start()` 只校验、不限定可达域的假语义；
6. Node 重复 ID 静默覆盖；
7. 无锁共享 `State map[string]any`；
8. 条件不满足后节点和后代静默消失；
9. 两个 engine executor 对 skipped 的不同解释；
10. `Target + Value any` 弱类型 Patch；
11. Graph 与 MutableDAG 两套 checkpoint、event 和 status；
12. 外围系统直接修改 DAG 或自行创建 runtime/bus/store 的旁路。

---

## 4. 统一执行语义

融合前必须先冻结执行合同。

### 4.1 边类型

显式区分：

```text
DataDependency  数据依赖
ControlFlow     控制流
```

数据依赖决定输入是否可用；控制边决定节点是否被激活。不得继续用一个 `DependsOn` 同时隐含两种关系。

### 4.2 分支

显式提供：

```text
BranchOne   恰好选择一个目标，条件重叠时报错
BranchMany  可以激活多个目标
```

禁止通过条件函数“恰好互补”的约定隐式模拟 XOR。

### 4.3 汇合

每个多入边节点必须声明：

```text
JoinAll  等待所有被激活的前驱终止
JoinAny  任一被激活的前驱成功即可执行
Merge    每次到达都可产生一次触发
```

“结构 AND + 条件 OR”不能继续作为隐藏默认行为。

### 4.4 节点状态

统一状态至少包括：

```text
Pending
Ready
Running
Completed
Failed
NotSelected
Unreachable
Blocked
Interrupted
Cancelled
```

推荐规则：

- 分支未选择：`NotSelected`
- 所有控制路径均不可到达：`Unreachable`
- 必需上游失败：`Blocked`
- 用户或 context 终止：`Cancelled`
- HITL 等待：`Interrupted`

所有未执行节点都必须有状态和原因，禁止静默消失。

### 4.5 Router

原 Router 拆为明确能力：

```text
SelectBranch     选择控制流分支
ActivateNode     显式激活节点
PrioritizeNode   只调整 ready queue 优先级
BypassDependency 特权操作，默认禁止
```

普通用户 API 不开放隐式 dependency bypass。

### 4.6 并发与 State

统一 Runner 支持并行，但 ExecutionState 不能继续是裸 `map[string]any`。

建议节点通过事务化视图读写：

```go
type StateView interface {
    Get(key string) (any, bool)
    Output(nodeID string) (NodeOutput, bool)
    Set(key string, value any) error
}
```

每个节点执行完成后，由 Runner 原子提交写集。并发写冲突必须有明确策略：拒绝、版本检查或显式 merge。

---

## 5. Workflow IR

IR 不能只是重命名 `engine.Step`，至少应包含：

```go
type WorkflowSpec struct {
    ID       string
    Entries  []NodeID
    Nodes    []NodeSpec
    Edges    []EdgeSpec
    Schedule ScheduleSpec
    Policies WorkflowPolicies
}
```

### 5.1 NodeSpec

表达：

- Node ID；
- Agent / Tool / Function / SubWorkflow 类型；
- 输入映射与输出 schema；
- Timeout / Retry / Recovery / HITL；
- 资源和并发约束；
- Join policy；
- metadata。

### 5.2 EdgeSpec

表达：

- from / to；
- DataDependency 或 ControlFlow；
- Condition；
- branch group；
- priority；
- output mapping。

### 5.3 Binding 层

任意 Node、闭包和 Agent 实例不能直接稳定序列化，因此定义与可执行对象分离：

```go
type Executable interface {
    Execute(context.Context, *ExecutionContext) (NodeOutput, error)
}

type BoundWorkflow struct {
    Spec     *WorkflowSpec
    Bindings map[NodeID]Executable
}
```

AgentRegistry、ToolRegistry、FuncNode 和 SubGraph 都成为 Binding provider，而不是四套执行分支。

### 5.4 Typed Mutation

Patch 必须强类型化：

```go
type AddEdgeMutation struct {
    From      NodeID
    To        NodeID
    Kind      EdgeKind
    Condition PredicateRef
}

type RemoveNodeMutation struct {
    NodeID NodeID
    Mode   RemoveMode
}
```

删除策略必须明确：

```text
RejectIfReferenced
Detach
Cascade
Rewire
```

Mutation 只能进入 PatchQueue，并在 Runner safe point 原子应用。

---

## 6. 唯一运行生命周期

统一生产管线固定为：

```text
1. Parse / Build Spec
2. Compile IR
3. Validate + Explain
4. Create ExecutionScope
5. Retrieve Knowledge / Memory
6. Schedule Ready Batch
7. BeforeNode hooks
8. Execute Node
9. Commit State + Result
10. AfterNode hooks
11. Evolution Evaluate
12. Apply Typed Patches at SafePoint
13. Atomic Checkpoint
14. Terminal Event
15. Distill Experience
```

### 6.1 ExecutionScope

每次执行只创建一个 scope，统一持有：

- execution ID；
- immutable IR version；
- transactional State；
- Node states/results；
- Knowledge context；
- Memory context；
- Event sink；
- Trace context；
- Checkpoint transaction；
- PatchQueue；
- ExecutionCollector。

禁止外围模块重新创建独立 KnowledgeRuntime、PluginBus、EventBus、CheckpointStore 或临时 Graph。

---

## 7. 外围系统闭环

| 系统 | 统一接入点 | 输入 | 输出 |
|---|---|---|---|
| Knowledge | ExecutionScope prepare | workflow goal、输入、预算 | 工作知识图 / prompt context |
| Memory | prepare / BeforeNode / AfterNode | 当前任务、节点状态 | 记忆命中、交互记录 |
| Experience | terminal | 完整执行轨迹、结果、失败原因 | 可复用经验 |
| Evolution | AfterNode / terminal | metrics、route、tool、memory hits | typed mutations |
| Observability | 全生命周期 | IR version、node、patch、checkpoint | trace、metrics、events |
| Checkpoint | Node commit / safe point | state、node states、IR version、patch queue | 原子恢复点 |
| Recovery | Node failure | failure context、graph snapshot | retry / replace / fail |
| HITL | BeforeNode | interrupt config、ExecutionScope | approval decision / resumable state |

### 7.1 Knowledge

Knowledge 检索必须成为 ExecutionScope prepare 的标准步骤，而不是由某个 agent 或 API 调用方偶然启用。

检索结果进入统一的只读 KnowledgeContext，节点按输入映射消费，避免直接污染全局 State。

### 7.2 Memory 与 Experience

- BeforeNode：按节点目标和当前 context 检索短期/长期记忆；
- AfterNode：记录节点输入、输出、工具和路由结果；
- Terminal：基于完整 trace 蒸馏经验；
- 失败和被阻塞路径同样进入经验质量评估。

### 7.3 Evolution

Evolution 不得直接修改 Graph 或 MutableDAG。

正确路径：

```text
Execution trace
      ↓
Evolution evaluator
      ↓
Typed Mutation proposal
      ↓
Validation / policy gate
      ↓
PatchQueue
      ↓
Runner safe point
      ↓
Atomic apply + checkpoint + event
```

### 7.4 Checkpoint

Checkpoint 必须原子保存：

- Workflow IR ID 与 version；
- ExecutionState version；
- 所有 NodeStatus；
- 已提交 NodeOutput；
- pending / applied mutation IDs；
- HITL interrupt 状态；
- scheduler 必需状态；
- event sequence。

不能只保存“执行过的节点列表 + State”。

---

## 8. 易用性目标

统一后，普通用户只需要一个公开入口：

```go
wf, err := workflow.New("review").
    Agent("draft", "writer").
    Agent("review", "reviewer").
    After("review", "draft").
    BranchOne("review",
        workflow.When("publish", approved),
        workflow.Otherwise("revise"),
    ).
    Build()
```

`Build()` 必须完成：

- 重复 ID 检查；
- 悬空依赖检查；
- 环检测；
- entry 与不可达检查；
- branch overlap / missing fallback 检查；
- 多入边 join policy 检查；
- State key 写冲突检查；
- unsupported capability 检查；
- typed mutation schema 检查。

`Explain()` 应输出：

- 哪些节点是 entry；
- 每个节点的触发条件；
- join 方式；
- 最大并行度；
- 哪些节点可能不可达；
- checkpoint / HITL / recovery 行为；
- 每个旧 API 构造被编译成何种规范语义。

---

## 9. 实施阶段

### P0：语义合同与金丝雀测试

冻结：

1. BranchOne / BranchMany；
2. JoinAll / JoinAny / Merge；
3. NotSelected / Unreachable / Blocked；
4. Router 权限；
5. State 并发与提交；
6. mutation 生效点；
7. checkpoint 原子边界。

建立 linear、fan-out、diamond、全条件 false、条件重叠、Router bypass、并发 State、动态 patch、HITL resume 测试。

### P1：建立 IR、Compiler、Validator

完成：

```text
legacy engine Workflow ──┐
                         ├── Workflow IR ── Validate / Explain
legacy graph Graph ──────┘
```

此阶段不切换生产执行器。

### P2：提炼 Single Runner

以 DynamicExecutor 的成熟能力为基础，提炼消费 IR 的统一 Runner：

- ready batch scheduling；
- transactional State；
- node lifecycle；
- retry/recovery/HITL；
- PatchQueue safe point；
- atomic checkpoint；
- canonical events/status。

不要继续把 `engine.Step` 膨胀成隐式 IR。

### P3：切换生产入口

建议顺序：

1. `api/service/workflow`
2. `api/client/workflow`
3. `Graph.Execute`
4. Evolution patch
5. Knowledge / Memory / Experience hooks
6. Checkpoint / Resume

### P4：删除旧运行路径

在 conformance tests 全部通过后删除：

- `Graph.Execute` 的旧执行循环；
- 静态 `engine.Executor` 生产入口；
- synthetic/no-op GraphPatchExecutor；
- 双 patch schema；
- 两套 status/event/checkpoint adapter；
- 任何绕过 Single Runner 的直接图修改路径。

删除模块或兼容路径时，按项目约定保留明确的 `// TODO(tech-debt)` 迁移说明，直到引用完全清零。

---

## 10. 验收标准

### 10.1 单运行时

- 所有公共执行入口最终调用同一个 Runner；
- 仓库中不存在第二套 ready queue / indegree 执行循环；
- 不存在 synthetic/no-op workflow graph；
- 所有 patch 经过同一个 PatchQueue。

### 10.2 语义一致

- 同一 IR 从 Builder、YAML、legacy engine、legacy graph 构建时结果一致；
- skipped、unreachable、blocked、cancelled 不再混淆；
- Router 不可隐式绕过 dependency；
- Branch 与 Join 均有明确策略和测试。

### 10.3 外围闭环

每次生产执行都能关联：

```text
execution ID
IR version
knowledge hits
memory hits
node results
route decisions
evolution proposals
applied patches
checkpoint version
experience record
trace/event sequence
```

任何模块不得通过独立 runtime、独立 bus 或私有图绕开该关联链。

### 10.4 易用性

- 80% 普通工作流只需要 `api/workflow`；
- 结构错误在 Build/Compile 阶段发现；
- 每个未执行节点都有状态和原因；
- 一份 Explain 输出即可理解拓扑、分支、汇合、并发、恢复和动态变异行为。

---

## 11. 最终方向

统一方案不是“保留 engine，再给 graph 做一层 adapter”，也不是“让两个模块共享几个接口”。

最终方向是：

> 以 engine 的 MutableDAG、并发、HITL、Recovery 和 Checkpoint 作为运行能力骨架；以 graph 的显式 Node/Edge、条件边、Scheduler 和 Builder 作为建模与易用性来源；用新的 Workflow IR、ExecutionScope 和 Single Runner 固化统一语义；所有外围系统只能通过唯一运行生命周期完成闭环。

这条路线才能同时实现：

- 优点共享；
- 缺点淘汰；
- 单一生产管线；
- 外围系统真实闭环；
- 用户入口和运行语义长期稳定。

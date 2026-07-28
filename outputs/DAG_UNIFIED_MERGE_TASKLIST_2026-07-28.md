# DAG 合并全新执行 Tasklist

> 日期：2026-07-28
> 目标：把 `internal/workflow/engine` 与 `internal/workflow/graph` 合并为唯一的 Workflow IR + Single Runner 生产管线，并让 Checkpoint、Observability、Knowledge、Memory、Experience、Evolution 只通过这一生命周期接入。
> 基线：按 2026-07-28 当前工作区源码重新整理；不沿用旧报告的任务编号和完成百分比。

---

## 0. 最终完成定义

只有同时满足以下条件，才算“DAG 合并完成”：

1. Builder、YAML、legacy engine API、legacy graph API 全部编译成同一种可序列化 `WorkflowSpec`；
2. 可执行函数、Agent、Tool、Condition、Router、Loop 条件统一进入一种 Binding 模型；
3. 所有公共执行入口最终调用同一个 Runner；
4. 仓库里不再存在第二套生产可达的 ready queue / indegree 执行循环；
5. Branch、Join、Loop、HITL、Retry、Recovery、Checkpoint、Resume 的语义只有一份；
6. 所有运行期图修改都经过 Typed Mutation → PatchQueue → Runner SafePoint；
7. Knowledge、Memory、Experience、Evolution、Trace、Metrics、Collector 共享同一个 ExecutionScope 和 execution ID；
8. 同一 workflow 从任意旧入口进入时，通过同一 conformance suite，结果和状态一致；
9. legacy engine/graph 生产执行器引用清零后，才允许删除旧运行路径。

**现在还没有达到以上状态。当前禁止：默认宣称统一完成、删除 legacy engine/graph、把旧执行器改动算作新 Runner 能力。**

---

## 1. 当前基线：哪些不用重做，哪些仍未闭环

### 1.1 已有基础，后续只做回归保护

- [x] 已有 `WorkflowSpec / NodeSpec / EdgeSpec / ConditionExpr` 基础 IR；
- [x] 已有 `ExecutionScope`、事务化 State、统一 NodeStatus；
- [x] 已有 Runner 基础并发调度、MaxParallel、Retry、Recovery、HITL、SubWorkflow；
- [x] fan-out/fan-in 提前退出、shared Runner `maxParallel` 竞态、Error/Attempts、SubWorkflow 父 State 等旧问题已有修复；
- [x] 已有 Compiler、Validator、Scheduler、PluginBus、CheckpointStore 的基础接口；
- [x] 当前代码已尝试修复成功节点 checkpoint commit 边界、Resume 继续调度、Loop initial state/父 SpecID、Merge/JoinAll；
- [x] Service 同步 Runner 路径已开始使用 `CompileFromEngineWithBindings`，并传入 input/variables/PluginBus。

这些只表示“已有代码”，不表示完整验收通过。下列任务必须补强断言后才能正式标记完成。

### 1.2 当前代码仍明确断线

- [ ] Runner 默认 condition evaluator 捕获的是构造时局部 map；`WithBindings`/`WithCompiledWorkflow` 替换 `r.bindings` 后，evaluator 不一定读取新 bindings；
- [ ] Service 同步路径没有真正装配 `ConditionFuncs`，并存在条件 evaluator 固定返回 `true` 的代码；
- [ ] `RouterFuncs` 被 Compiler 捕获，但 Runner 不消费；
- [ ] `untilCondition` 被 Runner option 保存，但执行循环没有调用；
- [ ] Service stream 路径仍使用基础 `CompileFromEngine`，丢 bindings；当前事件也是执行结束后回放状态，不是真正的执行流；
- [ ] Resume 没有恢复完整 node output/timestamps/scheduler branch 状态，也没有 IR version 校验；失败节点恢复策略不清晰；
- [ ] Checkpoint 不包含 scheduler 状态、IR version、pending/applied patch、HITL、event sequence；失败节点路径也没有形成对称原子 checkpoint；
- [ ] Loop 仍按 iteration 重建多个 ExecutionScope，最终只合并 status，无法保证唯一 execution ID、完整 output/error/attempts/timestamps；
- [ ] Scheduler 的“所有被激活前驱”没有显式状态模型；当前 JoinAll 检查所有结构前驱，未选择分支可能永久阻塞；Merge 并发 arrival 可能被 readySet 折叠；
- [ ] `api/service/workflow` 仍由 `UseRunner` 分流，bool 零值仍走 legacy DynamicExecutor；
- [ ] `api/client/workflow` 仍直接持有和调用 `engine.Executor`；
- [ ] `api/graph` 仍导出 legacy `Graph/NewGraph`，`Graph.Execute` 和 `ExecuteFromCheckpoint` 仍可达；
- [ ] legacy `SubGraphNode.Execute` 仍直接调用 `n.graph.Execute`；
- [ ] unified Runner 中没有 PatchQueue、Typed Mutation、ExecutionCollector、Knowledge/Memory/Experience/Evolution 接线；
- [ ] 没有 `Explain()`，Validator 也未覆盖全部统一合同。

---

## 2. 唯一关键路径

```text
T0 语义合同与强测试
  ↓
T1-01 Scheduler 状态机 ──┐
                          ├── T1 Loop / Checkpoint / Resume
T2-01 BoundWorkflow ──────┘
  ↓
T2 Compiler + Validator/Explain
  ↓
T3 Service 同步/流式入口迁移并默认切换
  ↓
T4 Client/YAML + Graph/SubGraph 入口迁移
  ↓
T5 Typed Mutation + PatchQueue + SafePoint
  ↓
T6 Observability + Knowledge/Memory/Experience/Evolution 闭环
  ↓
T7 全入口一致性、灰度与故障恢复验收
  ↓
T8 删除 legacy 生产运行时
```

T1-01 与 T2-01 可以并行；Loop 的 Until、Checkpoint 与 Resume 必须建立在统一 Binding 和 Scheduler 状态模型之上。不得跳过 T0–T2 直接默认切换；不得跳过 T7 直接删除 legacy。

---

# T0：冻结统一语义合同，先让测试能证明对错

## DAG-T0-01 建立 Runner 核心语义合同测试

**目的：** 把此前临时 overlay 场景和当前新修复全部转成仓库内强断言测试。

**必须覆盖：**

- [ ] linear、fan-out、diamond fan-in；
- [ ] `BranchOne`：唯一分支、条件重叠、无匹配、fallback；
- [ ] `BranchMany`：0/1/N 条边激活；
- [ ] `JoinAll`：只等待“已激活前驱”，未选择分支不能阻塞；
- [ ] `JoinAny`：第一个成功激活即可执行，后续 arrival 不重复；
- [ ] `Merge`：每个 arrival 独立触发，包括并发 arrival；
- [ ] `NotSelected / Unreachable / Blocked / Cancelled / Interrupted` 无静默节点；
- [ ] explicit Entries 限定执行可达域；
- [ ] `MaxParallel`、context cancel、协作式 timeout；
- [ ] 并发 State 写入冲突策略；
- [ ] Runner 同实例并发执行无 race。

**验收：**

```bash
go test -race -count=20 ./internal/workflow/...
```

所有合同必须是结果断言，不能只打印日志。

**依赖：** 无。

## DAG-T0-02 建立 Loop / Checkpoint / Resume 故障恢复合同

- [ ] 首轮执行完整 workflow，后续只执行 `LoopNodes`；
- [ ] initial input/variables 每轮可见但不被错误覆盖；
- [ ] 整次 Loop 只有一个 execution ID 和父 SpecID；
- [ ] 最终结果保留 setup、body 各轮的 output/error/attempts/timestamps；
- [ ] Until 条件在正确轮次停止；
- [ ] 成功节点 checkpoint 是 commit 后状态；
- [ ] 失败、HITL interrupt、cancel 也有定义明确的 checkpoint；
- [ ] 进程重启后只执行 pending/ready 节点，不重复已完成副作用；
- [ ] Spec ID/version 不匹配必须拒绝；
- [ ] Resume 后终态不能在仍有 pending 节点时返回 completed。

**验收：** 使用内存 CheckpointStore 和序列化/反序列化模拟“新 Runner 进程”恢复，而不是复用原对象。

**依赖：** DAG-T0-01。

## DAG-T0-03 建立多前端 Conformance Suite 骨架

同一规范案例分别由以下来源构建：

- [ ] 原生 Builder；
- [ ] YAML；
- [ ] legacy engine Workflow；
- [ ] legacy graph Graph。

统一比较：

- normalized IR；
- 拓扑与 Entries；
- Branch/Join；
- 最终 State；
- 每个 NodeStatus；
- route decision；
- checkpoint/resume 结果。

**验收：** 一套 table-driven suite 能被四种前端复用。

**依赖：** DAG-T0-01。

---

# T1：把 Single Runner 内核做成可信运行时

## DAG-T1-01 修正 Condition / Branch / Join / Merge 调度状态机

- [ ] Scheduler 显式记录 edge activation，而不是只记录 predecessor completed；
- [ ] JoinAll 等待所有“已激活且必须等待”的前驱；
- [ ] 未选择 control-flow edge 不参与 JoinAll；
- [ ] BranchOne 重叠时按冻结合同报错，不允许“第一条碰巧获胜”掩盖错误；
- [ ] Merge 使用 arrival token/sequence，不能通过删除 completed 标记模拟；
- [ ] 并发到达同一 Merge 时不折叠、不丢触发；
- [ ] finaliseUnprocessed 根据 activation graph 生成准确原因。

**验收：** DAG-T0-01 全部通过，且 `go test -race` 无竞态。

**依赖：** DAG-T0-01。

## DAG-T1-02 重构 Loop 为单一父 ExecutionScope

- [ ] 不再用新的顶层 ExecutionScope 替换当前 scope；
- [ ] 使用 iteration frame 或 node-attempt/iteration key 表达每轮状态；
- [ ] 全程保持同一个 execution ID、父 SpecID 和事件序列；
- [ ] setup 状态只执行一次且永久保留；
- [ ] body 每轮结果可审计，不用同 ID 的最后一个 status 覆盖历史；
- [ ] `UntilCondition` 真正被调用；
- [ ] Until closure 只存在于 Binding，IR 中使用可序列化引用。

**验收：** DAG-T0-02 的 Loop 合同全部通过。

**依赖：** DAG-T0-02、DAG-T2-01。

## DAG-T1-03 定义原子 CheckpointSnapshot

创建强类型快照，至少包含：

- [ ] execution ID；
- [ ] WorkflowSpec ID + immutable version/hash；
- [ ] committed State version；
- [ ] 全量 NodeStates/outputs/attempts/timestamps；
- [ ] scheduler ready/pending/activation/branch/arrival 状态；
- [ ] loop iteration frame；
- [ ] HITL interrupt 状态；
- [ ] pending/applied mutation IDs；
- [ ] event sequence；
- [ ] checkpoint version/schema version。

保存顺序固定为：

```text
node result → State commit → Scheduler transition → Patch safe point → Checkpoint save → event acknowledgment
```

失败、cancel、interrupt 路径也必须定义对应原子边界。

**验收：** checkpoint 不能再用 `map[string]any` 作为内部协议；序列化 round-trip 强测试通过。

**依赖：** DAG-T1-01、DAG-T1-02。

## DAG-T1-04 重写 Resume 为“恢复同一执行”

- [ ] 校验 checkpoint schema、Spec ID、Spec version/hash；
- [ ] 恢复 State、node outputs、attempts、timestamps、execution timing；
- [ ] 恢复 Scheduler，而不是用 `OnNodeCompleted` 猜测重放；
- [ ] 明确 failed/running/interrupted 节点在 crash 后的恢复策略；
- [ ] 恢复 Loop、HITL、Mutation、event sequence；
- [ ] 只执行未提交节点；
- [ ] 保持原 execution ID；
- [ ] 终态由全量节点状态计算，禁止只看 `scope.err`。

**验收：** DAG-T0-02 的 crash/restart 场景全部通过；重复 Resume 不产生重复副作用。

**依赖：** DAG-T1-03。

## DAG-T1-05 固化 timeout / retry / recovery / HITL 合同

- [ ] 文档和接口明确 timeout 是 Go context 协作式取消；
- [ ] 所有 production Agent/Tool/Func binding 必须传播 ctx；
- [ ] timeout、retry、replacement recovery 的 attempts/error/status 一致；
- [ ] HITL approval/reject/timeout/fallback 都能 checkpoint 和 resume；
- [ ] Plugin hook 失败策略明确，不得静默改变节点终态。

**验收：** 受控 fake executor + 真实 Agent adapter 两层测试通过。

**依赖：** DAG-T1-03。

---

# T2：统一 IR 与 Binding，消灭“编译了但执行语义丢失”

## DAG-T2-01 定义唯一 `BoundWorkflow`

建议最终形态：

```go
type BoundWorkflow struct {
    Spec     *WorkflowSpec
    Nodes    map[NodeID]Executable
    Predicates map[PredicateRef]Predicate
    Routers  map[RouterRef]Router
    Loops    map[LoopPredicateRef]LoopPredicate
}
```

- [ ] `WorkflowSpec` 只保留可序列化引用；
- [ ] Agent、Tool、Func、SubWorkflow 都实现统一 `Executable`；
- [ ] Condition、Router、Until 不再分散在多个 map 和 RunnerOption；
- [ ] Runner 只接收 `BoundWorkflow` 或由统一 Binder 解析后的对象；
- [ ] binding 缺失、类型不匹配必须 compile/bind 阶段 fail-fast；
- [ ] 删除 graph condition 默认 `true` 等保守但错误的 fallback。

**验收：** 序列化 Spec 后重新绑定可以得到同样执行结果。

**依赖：** DAG-T1-01。

## DAG-T2-02 修复 engine Compiler 的完整语义保持

- [ ] Step Condition 不再只挂到第一条入边；
- [ ] 明确 legacy Step Condition 编译成 node guard 还是 control-flow predicate；
- [ ] Router 编译成显式 `SelectBranch / ActivateNode / PrioritizeNode`，默认禁止 bypass dependency；
- [ ] Loop Until 编译成 predicate ref + binding；
- [ ] Retry/Recovery/HITL/SubWorkflow/Timeout/metadata 全量映射；
- [ ] 不支持的 legacy 语义必须报错，不能静默降级。

**验收：** engine 前端通过 DAG-T0-03。

**依赖：** DAG-T2-01。

## DAG-T2-03 修复 graph Compiler 与 Node Binding

- [ ] Graph 提供只读 snapshot/export，让 Compiler 获取真实 Node 类型和实例；
- [ ] AgentNode、ToolNode、FuncNode、SubGraphNode 全部生成对应 binding；
- [ ] 每条条件边获得稳定 predicate ref 与 closure binding；
- [ ] `Start()` 编译成真实 Entries/可达域；
- [ ] legacy Scheduler 配置映射到统一 ScheduleSpec，无法等价时 fail-fast；
- [ ] SubGraph 编译为 SubWorkflow/BoundWorkflow，不再递归调用 legacy `Graph.Execute`。

**验收：** graph 前端通过 DAG-T0-03。

**依赖：** DAG-T2-01。

## DAG-T2-04 补齐 Validator 与 Explain

Validator 必须新增：

- [ ] condition/predicate/router/binding 引用存在性；
- [ ] BranchOne fallback、重叠风险和空分支；
- [ ] 所有多入边节点显式 Join policy；
- [ ] Entries 与不可达节点策略；
- [ ] State key 并发写冲突；
- [ ] Loop body 边界和退出条件；
- [ ] unsupported capability；
- [ ] typed mutation schema 和 policy。

`Explain()` 至少输出：

- [ ] entries/可达域；
- [ ] 每个节点的激活条件；
- [ ] branch/join/merge；
- [ ] 最大并行度；
- [ ] retry/recovery/HITL/checkpoint；
- [ ] binding 来源；
- [ ] 可能 NotSelected/Unreachable/Blocked 的原因；
- [ ] legacy 语义如何被编译。

**验收：** 无法执行的结构在 Runner 启动前失败；Explain 有 golden tests。

**依赖：** DAG-T2-02、DAG-T2-03。

---

# T3：先迁移 Service，建立第一条真实生产闭环

## DAG-T3-01 合并 sync/stream 的编译与 Runner 装配

- [ ] sync 和 stream 共用一个 `compileAndBind()`；
- [ ] 都使用完整 `BoundWorkflow`，不得一条捕获 bindings、一条丢 bindings；
- [ ] 删除固定返回 `true` 的 condition evaluator；
- [ ] 统一注入 input、variables、PluginBus、CheckpointStore、Collector、EventSink；
- [ ] Service Config 增加实际可用的 CheckpointStore/运行依赖；
- [ ] Agent adapter 使用 NodeSpec input mapping，而不是所有节点永远读全局 `input`。

**验收：** 同一 workflow 的 sync/stream 最终状态、输出、错误一致。

**依赖：** T1、T2 全部完成。

## DAG-T3-02 实现 Runner 原生事件流

- [ ] Runner 在真实生命周期发 started/ready/running/completed/failed/interrupt/patch/checkpoint/terminal；
- [ ] `ExecuteStream` 实时转发事件，不在执行结束后伪造逐节点事件；
- [ ] 事件包含 execution ID、Spec version、node ID、sequence、timestamp；
- [ ] 慢消费者、cancel、channel close 不阻塞执行或泄漏 goroutine。

**验收：** 事件顺序、终态和 checkpoint sequence 强断言；race/leak 测试通过。

**依赖：** DAG-T3-01。

## DAG-T3-03 Service 灰度并默认切 Runner

- [ ] 先保留显式 canary/legacy fallback；
- [ ] 用相同请求做 Runner 与 DynamicExecutor shadow 对比；
- [ ] 差异按 IR/状态/输出/事件分类，不允许静默 fallback；
- [ ] conformance + 线上 canary 达标后，Runner 成为真正默认；
- [ ] 避免 `UseRunner bool` 零值语义与注释冲突，改为明确 mode/枚举；
- [ ] 再移除 Service 对 DynamicExecutor 的生产调用。

**验收：** `api/service/workflow` 的生产代码不再调用 `ExecuteDynamic`。

**依赖：** DAG-T3-02。

---

# T4：迁移其余公共入口，达成“所有入口同一个 Runner”

## DAG-T4-01 迁移 Client/YAML

- [ ] YAML loader 输出 WorkflowSpec 或 legacy Workflow 后立即 compile+bind；
- [ ] `WorkflowClient` 不再持有 `*engine.Executor`；
- [ ] Execute/ExecuteFromFile 调用统一 Runner；
- [ ] 提供结果兼容 adapter，保持外部 API 迁移期兼容；
- [ ] YAML 条件、Loop、Retry、Recovery、HITL 有端到端测试。

**验收：** `api/client` 生产代码无 `engine.NewExecutor`/`Executor.Execute`。

**依赖：** DAG-T3-03。

## DAG-T4-02 迁移 Graph API

- [ ] `api/graph.NewGraph` 返回兼容 Builder/Wrapper，而不是直接 type alias legacy Graph；
- [ ] `Execute` 内部固定 compile+bind+Runner；
- [ ] `ExecuteFromCheckpoint` 调统一 Resume；
- [ ] legacy scheduler/router/condition 通过 adapter 显式映射；
- [ ] 不可等价的旧行为给迁移错误或 deprecation，不允许走第二运行循环。

**验收：** 公共 Graph API 可保留，但其执行栈只进入统一 Runner。

**依赖：** DAG-T4-01。

## DAG-T4-03 迁移 SubGraph/SubWorkflow

- [ ] legacy `SubGraphNode.Execute` 不再调用 `n.graph.Execute`；
- [ ] 子图编译为 BoundWorkflow；
- [ ] 父子 State 输入/输出映射显式；
- [ ] 子 workflow 继承 trace/execution lineage、PluginBus、Checkpoint、Collector；
- [ ] 子 workflow 失败、HITL、Resume 行为可恢复。

**验收：** 全仓生产路径搜索不到 `.graph.Execute` 的嵌套调用。

**依赖：** DAG-T4-02。

---

# T5：统一动态变异，只允许 PatchQueue

## DAG-T5-01 定义 Typed Mutation 协议

至少包含：

- [ ] Add/Remove/Replace Node；
- [ ] Add/Remove Edge；
- [ ] Change Schedule；
- [ ] Change Retry/Recovery；
- [ ] Branch/Predicate 更新；
- [ ] 明确 RemoveMode：Reject/Detach/Cascade/Rewire；
- [ ] mutation ID、base IR version、来源、理由、policy metadata。

**验收：** 不再用 `Target + Value any` 作为统一协议。

**依赖：** DAG-T2-04。

## DAG-T5-02 将 PatchQueue 放入 ExecutionScope

- [ ] 外围模块只能 submit proposal，不能直接改 Graph/MutableDAG；
- [ ] Runner 在 node commit 后的 safe point 拉取 patch；
- [ ] 校验版本、拓扑、binding、policy；
- [ ] 原子 apply，失败 rollback；
- [ ] patch 前后 IR version、结果和事件写入 checkpoint；
- [ ] Resume 不重复应用已 applied mutation。

**验收：** 并发 patch、非法 patch、crash-after-apply-before-ack 全部可确定恢复。

**依赖：** DAG-T1-03、DAG-T5-01。

## DAG-T5-03 迁移 Evolution patch 入口

- [ ] Evolution 输出 Typed Mutation proposal；
- [ ] 删除/停用 synthetic/no-op GraphPatchExecutor；
- [ ] Recovery patch、Graph patch、MutableDAG patch 统一经 PatchQueue；
- [ ] 任何直接图修改调用在生产代码中清零。

**验收：** 全仓所有生产 patch 都可关联 execution ID、base/new IR version、proposal/applied/rejected 状态。

**依赖：** DAG-T5-02。

---

# T6：把外围系统接入唯一 ExecutionScope

## DAG-T6-01 扩展 ExecutionScope 运行依赖

统一持有或引用：

- [ ] EventSink / trace context；
- [ ] ExecutionCollector；
- [ ] Checkpoint transaction；
- [ ] PatchQueue；
- [ ] KnowledgeContext；
- [ ] MemoryContext；
- [ ] execution lineage / Spec version。

禁止节点或外围模块自行创建私有 runtime/bus/store/graph。

**依赖：** DAG-T5-02。

## DAG-T6-02 Observability 与 Collector 闭环

- [ ] Before/After Node、route、tool、memory hit、interrupt、error、patch、checkpoint、terminal 全记录；
- [ ] trace、metric、event 使用同一 execution ID 和 sequence；
- [ ] collector 数据可供 checkpoint、experience、evolution 消费；
- [ ] 失败、blocked、not_selected 路径同样可观测。

**验收：** 单次执行可查询完整 timeline，不依赖拼接多个私有 ID。

**依赖：** DAG-T6-01。

## DAG-T6-03 Knowledge / Memory 标准 prepare 与 node hooks

- [ ] workflow prepare 阶段统一检索 Knowledge/Memory；
- [ ] 检索结果进入只读 context，不直接污染全局 State；
- [ ] BeforeNode 按节点目标检索，AfterNode 记录真实消费；
- [ ] hit IDs、score、是否使用写入 Collector；
- [ ] 不允许 API 调用方偶然开关形成旁路。

**验收：** 每次生产执行能追踪 knowledge hits、memory hits 到具体节点和输出。

**依赖：** DAG-T6-02。

## DAG-T6-04 Experience / Evolution terminal 闭环

- [ ] Terminal 基于完整 trace 蒸馏 Experience；
- [ ] 失败/blocked/cancelled 同样进入质量评估；
- [ ] Evolution 只读取 Collector/metrics，输出 mutation proposal；
- [ ] proposal 经 policy gate 和 PatchQueue，不直接改运行图；
- [ ] experience/evolution 记录关联 execution ID、Spec version、checkpoint version。

**验收：** 完成“执行 → 轨迹 → 经验/进化 → proposal → safe point patch → checkpoint”的可回放链。

**依赖：** DAG-T6-03、DAG-T5-03。

---

# T7：生产验收与切换门槛

## DAG-T7-01 全入口一致性验收

- [ ] Builder/YAML/engine/graph 四前端通过 DAG-T0-03；
- [ ] sync/stream/client/graph/subgraph 通过同一结果合同；
- [ ] 所有未执行节点都有状态和原因；
- [ ] 相同 Spec version 的执行语义与入口无关。

## DAG-T7-02 稳定性与恢复验收

运行并通过：

```bash
go test ./internal/workflow/...
go test -race -count=20 ./internal/workflow/...
go test ./api/service/workflow/... ./api/client/... ./api/graph/...
go test ./...
go vet ./...
golangci-lint run ./...
git diff --check
```

另需：

- [ ] crash/restart matrix；
- [ ] HITL 长暂停恢复；
- [ ] patch crash consistency；
- [ ] 高并发 MaxParallel/State 冲突；
- [ ] event backpressure/goroutine leak；
- [ ] 基准性能不低于约定阈值。

## DAG-T7-03 生产可达路径审计

生产代码必须满足：

- [ ] `engine.Executor.Execute` 调用为 0；
- [ ] `DynamicExecutor.ExecuteDynamic*` 调用为 0；
- [ ] `graph.Graph.Execute*` 调用为 0；
- [ ] 第二套 ready queue/indegree 执行循环不可达；
- [ ] 直接 Graph/MutableDAG patch 调用为 0；
- [ ] 私有 KnowledgeRuntime/EventBus/CheckpointStore 临时构造旁路为 0。

测试和 migration fixture 可暂存，但必须与生产 build 隔离。

**依赖：** T0–T6 全部完成。

---

# T8：删除 legacy 运行时，只保留兼容前端

## DAG-T8-01 删除旧执行循环

在 DAG-T7 全部通过后才执行：

- [ ] 删除/隔离 `Graph.Execute` 旧循环；
- [ ] 删除静态 `engine.Executor` 生产路径；
- [ ] 删除 `DynamicExecutor` 生产路径；
- [ ] 删除旧 Resume/checkpoint/status/event adapter；
- [ ] 删除 synthetic/no-op GraphPatchExecutor；
- [ ] 删除双 patch schema；
- [ ] 保留必要的 legacy Builder/YAML parser，但输出只能是统一 IR/BoundWorkflow。

按项目约定，砍模块及兼容路径时在相关迁移点保留明确的 `// TODO(tech-debt)`，直到引用完全清零。

## DAG-T8-02 最终易用性收口

- [ ] 普通用户只需要一个 `api/workflow` Builder；
- [ ] `Build/Compile` 即完成 Validate；
- [ ] 一份 Explain 可理解拓扑、分支、汇合、并发、恢复、变异；
- [ ] 兼容 API 都标注迁移方式和最终去除边界；
- [ ] 示例和文档不再展示 legacy Execute。

**最终验收：** 满足第 0 节全部九条完成定义。

---

## 3. 推荐实际执行批次

### 批次 A：先修当前最危险的“看似接上，实际没生效”

1. DAG-T0-01 / T0-02 正式测试；
2. 并行完成 DAG-T1-01 Scheduler activation/Merge 与 DAG-T2-01 BoundWorkflow；
3. DAG-T1-02 Loop 单 Scope + Until binding；
4. DAG-T1-03 / T1-04 Checkpoint + Resume；
5. 修掉 condition bindings 捕获错误、固定 `return true`、Router 未消费；
6. 在 DAG-T3-01 中统一 sync/stream 装配，消除 stream 丢 bindings。

**批次 A 完成后：** Runner 内核才有资格继续 opt-in 测试，但仍不能删除 legacy。

### 批次 B：统一编译和第一生产入口

1. DAG-T2-02 / T2-03 / T2-04；
2. DAG-T3-01 / T3-02；
3. DAG-T3-03 Service 默认切换。

**批次 B 完成后：** Service 成为第一条完整统一生产路径。

### 批次 C：清掉其他运行入口

1. DAG-T4-01 Client/YAML；
2. DAG-T4-02 Graph；
3. DAG-T4-03 SubGraph。

**批次 C 完成后：** 才首次满足“所有公共入口同一个 Runner”。

### 批次 D：动态变异和外围闭环

1. T5 Typed Mutation/PatchQueue；
2. T6 Observability/Collector；
3. T6 Knowledge/Memory；
4. T6 Experience/Evolution。

### 批次 E：验收与删除

1. T7 全入口、稳定性和可达路径审计；
2. 灰度无阻断后执行 T8；
3. 最后一次全仓 test/race/vet/lint/build。

---

## 4. 每个任务统一完成规则

任务只能在以下证据齐全时打勾：

1. **实现存在**：不是只有字段、接口、注释或 TODO；
2. **生产可达**：至少一个目标生产入口真实调用；
3. **强断言测试**：失败时测试必红，不是只打印状态；
4. **故障路径覆盖**：error/cancel/crash/resume 有定义；
5. **race 通过**：涉及并发必须跑 `-race`；
6. **无语义 fallback**：不能用固定 true、best-effort、silent drop 假装兼容；
7. **旧路径减少**：迁移任务必须同时减少 legacy 生产调用；
8. **证据记录**：保留 file:line、测试名、关键命令结果。

---

## 5. 当前最先要做的 6 个具体任务

如果现在开始实施，严格按以下顺序：

1. **把 Loop/Resume/Checkpoint/Merge/JoinAll 当前修复写成仓库正式强断言测试并跑红绿验证；**
2. **并行完成 Scheduler edge activation 模型与唯一 BoundWorkflow；**
3. **把 Loop 改为单一父 ExecutionScope，并真正调用 Until binding；**
4. **定义强类型 CheckpointSnapshot，按快照恢复 Scheduler/Loop/HITL/Outputs；**
5. **修掉 condition binding 捕获、Router 未消费、固定 true 和 stream 丢 bindings；**
6. **sync/stream 共用装配后，再让 Service Runner 成为默认并移除其 DynamicExecutor 调用。**

完成这 6 项，只代表“Runner + Service 统一完成”；之后仍必须做 Client/Graph/SubGraph、PatchQueue、外围闭环和 legacy 删除。

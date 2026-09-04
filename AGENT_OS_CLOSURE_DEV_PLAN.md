# Agent OS 端到端闭环 — 开发计划

核实时间：2026-09-03。以下每条断点均已在代码中比对过行号，不是推测。

> **进度状态（2026-09-03 续接）**
>
> - ✅ 已落地：Step 1-6 全部（含 6.1 策略 ID 透传进补丁并 fail-fast；6.2 `deploymentStagingRuntime` 改持 `asm` 引用、`Evaluate` 内 `asm.Current()` 现取 baseline）。
>
> - ✅ 已落地：Step 7.1——`UpdateLiveDAG` 现在就地调用 `WorkflowGenome.SetDAG` 把基因组重指到 live DAG，并删掉旧的 "NOT updated here" 注释；新增回归测试 `TestUpdateLiveDAG_RepointsWorkflowGenome`；修掉 `GenomeReg == nil` 时的空指针。
>
> - ✅ 已落地：Step 7.2——7.2.1 字面 `generateDiffPatches` 同图断言（`ares_evolution/generate_diff_patches_workflow_test.go`，含正向 `TestGenerateDiffPatches_SameGraphNodeRefsResolveInLiveDAG` 与反证 `TestGenerateDiffPatches_CrossGraphMismatchIsDetected`，证明基因组读 live DAG、patch 节点 ID ⊆ live DAG ∪ 本次引入节点）；7.2.2「可观测变更」集成测试 `ares_bootstrap/deploy_live_dag_integration_test.go` 的 `TestDeployLiveDAG_ObservableTopologyChangeAndRollback`（Deploy 后 `runtime.GetAgentDAG(AgentDAGLiveKey)` 拓扑变化、`Rollback` 就地复原、同一指针）；7.2.3 F04 隔离由 `closure_contract_test.go` 覆盖并通过（`TestClosure_Ready_AllExecutorsBoundToLiveTargets`）。
>
> - ✅ 已落地：Step 7.3——非 serve 入口显式日志（N-4）：`bootstrap.go` 在注册合成图后、`cfg.Evolution.Enabled` 时输出 "evolution verdicts available but no live agent topology to act on"（`live_dag_registered=false` + 合成图 key），明确 serve 入口是唯一 live DAG 供给方。
>
> - ✅ 已落地：Step 4——真实执行 A/B（N-1，2026-09-03）：
>
>   - 调度器捕获：`kernelscheduler.Scheduler` 新增 `WithShadowExecutionHook`（`shadow.go`），`executeWithCandidates` 在量子成功收尾后把 `ToModelTask` 视图交给钩子（失败量子不采样；钩子契约「只入队不阻塞」）。
>
>   - 隔离执行：`ares_evolution/shadow_executor.go` 的 `ShadowExecutor` 同时实现 `ShadowExecutionHook`（结构化满足，零 import 回环）与 `ShadowExecutionFeeder`。`Feed` 对候选与活跃双臂在同一批缓冲任务副本上执行，双臂各写一条 `strategy_id==自身` + `shadow:true` 的 `strategy_shadow` fitness 证据；跑不起来的臂不造假分、不成对。证据走独立 source，rollback 窗口与 deployment staging 的活跃分不受影子记录污染。
>
>   - 副作用 deny-list：serve 侧 `cmd/ares/shadow_execution.go` 的 `shadowDenyBinder` 在接口层拦截一切工具调用（`ListTools` 返回空、`CallTool` 返回 `errShadowToolDenied`，生产 binder 全程零触达，测试用接口级 spy 断言计数 == 0）；影子 cognition 不接生产 EventStore。
>
>   - 判决回流：`ShadowSampler.SetExecutionFeeder`（`lifecycle.ShadowSampler()` 暴露）——`Prime` 先跑真实执行 A/B 并把配对结果 `RecordResult` 进 G2 判决，无产出时回退 replay 窗口（语义不变）。
>
>   - 配置：`evolution.shadow_execution: {enabled: false, sample_size: N}` 默认关闭（`ares_config` + `configs/ares.yaml`）；`wireShadowExecution` 在 evolution/影子执行/采样器任一缺失时显式 no-op 并留日志。
>
>   - 测试：`kernelscheduler/shadow_hook_test.go`（采样契约）、`ares_evolution/shadow_executor_test.go`（配对证据/采样条数/克隆隔离/防造假 guard）、`ares_evolution/shadow_sampler_construction_test.go`（构造路径，N-3 薄弱点）、`cmd/ares/shadow_execution_test.go`（deny-list spy 断言 + 端到端影子证据）。
>
> - 验证：`go build ./...`、`go vet`、`gofmt`、`go test -race ./internal/kernelscheduler/... ./internal/ares_evolution/... ./internal/ares_bootstrap/... ./internal/evolution/deployment/...`、以及 `make gate` 全部通过。
>
> - ✅ 已落地：**Step Y 度量半边（Y.2/Y.3 的 OBSERVE 阶段）+ P1-3 recover 修复（2026-09-04）**：
>
>   - 共享原语：新增 `internal/feedback`（纯数据包，零依赖标准库以外）承载 `Outcome`/`CollaborationOutcome`/`ToolCallOutcome`。生产方（`agentipc`、tool binder 装饰器）与消费方（`ares_evolution`）各自在消费点声明自己的 observer 接口，结构化满足 —— 内核 IPC 总线与工具层**零 import 进化层**。
>
>   - Y.2 协作度量：`agentipc.Bus` 新增 `WithCollaborationObserver`（`collaboration_observer.go`）。观测点在 `primitives.go` 的 **`Request`** **与** **`Send`** **两处出口**（`Delegate`/`Handoff` 走 `Request` 自动继承）。`feedback.CollaborationKind` 区分 `request`（回答）/`send`（投递受理）—— 两者度量的不是同一件事，混成一个成功率将不可审计。
>
>   - Y.3 工具度量：`sub.ObserveToolCalls` 装饰 `ToolBinder`（`tool_observer.go`）。选装饰器而非在执行体内加钩子，是因为生产有**两条执行体**（`sub` executor 与 `agentfabric.ChatCognition`），都从注入的 binder 取工具——包 binder 一处覆盖两条，且后续新增第三条执行路径无法绕过。
>
>   - 判决接入：`ChannelFeedbackRecorder`（`ares_evolution/channel_feedback.go`）把两通道写成 `collaboration` / `tool_call` **独立 source** 的 `KindFitness` 证据，按 `asm.Current()` 现取归因；`FitnessWeights` 新增 `Collaboration`/`ToolCall` 两维，`RuntimeFitnessAggregator.Window` 对两通道**按策略 scope**（不同于 workflow/scheduler/recovery 的 runtime-global）。
>
>   - 配置：`evolution.channel_feedback: {collab_enabled, collab_weight, tool_enabled, tool_weight}` 全默认关闭/0（`ares_config` + `configs/ares.yaml`）。`enabled` 管「是否记录」、`weight` 管「是否计入判决」，两者分离以便先审计证据再放权重。
>
>   - **P1-3 修复**（deep review 已核实项）：`Request` 的裸 goroutine 抽为 `Bus.invokeHandler` 并加 recover 边界，panic 转 `ErrHandlerPanic` 走与普通 handler error **相同的 sentinel-nil-reply 唤醒协议**（只 log 不唤醒会让调用方白等满 timeout）；panic 值不进 error（可能含内部路径/请求数据），只进注入的 logger（`Bus.WithLogger`，两个生产构造点已接）。`Send` 的同步 panic 刻意不 recover —— 它跑在调用方 goroutine 上，吞掉才是隐藏错误。
>
>   - 测试：`agentipc/collaboration_observer_test.go`（四种 outcome 表驱动、Send 生产路径、panic 不写假成功、panic 不烧 timeout、日志上下文键）、`ares_evolution/channel_feedback_test.go`（归因/未归因丢弃/未开通道零记录/source 隔离）、`ares_evolution/fitness_aggregator_channels_test.go`（weight=0 惰性、开权重后判决分化、跨策略不泄漏）、`cmd/ares/channel_feedback_wiring_test.go`（编译期接口断言 + 端到端 + 生产 Send 路径）。
>
>   - 验证：`go build`/`go vet`/`gofmt`/`golangci-lint`(0 issues)/`go test -race`/`make gate`/`git diff --check` 全过。
>
> - ⛔ **Step Y 作动半边未落地（下一步的真实工作量所在，见修订后的 Step Y）**：度量已通，但**进化拿到分数后没有对应旋钮可拧**——`mutation.Strategy` 的可变面只有 `PromptTemplate` + `Params`（LLM 参数），既无工具白名单/排序字段，agent 也没有「选择协作对象」的可调动作。原计划的 `ToolSelectionGenome` / `CollabGenome` 因此无法按原文交付。
>
> - ⭐ **Step Y 已立项（用户方向确认 2026-09-03，范围于 2026-09-04 修订）**：打通「单 agent 任务 + 跨 agent 协作 + 工具特征」三通道真实闭环（详见文末 Step Y）。

## 0. 未闭环边界（硬边界 — 0.3.1 vs 0.4.x 的划分依据，不可含糊）

> 本节是**发布措辞的唯一依据**。对外只能说清楚"哪些闭环了、哪些没有"；凡本节点名项，均不得在 0.3.1 发布文档/CHANGELOG 中以"闭环""完整"表述。

### N-1（P1-1，最关键）候选无自身证据 —— real-execution A/B ✅ 已落地（2026-09-03，见进度状态 Step 4）

- **原状**：G2 shadow 判定靠 `ReplayScorer` 读**策略自身历史**，不是"候选真跑一遍"；verdict 不是候选特异性的。

- **落地后**：`evolution.shadow_execution.enabled=true` 时，候选与活跃双臂在隔离 runner（工具调用接口级 deny、不接生产 EventStore）中对最近缓冲的真实任务副本各执行一次，配对结果直接进 G2 判决，双臂各留一条 `strategy_shadow` 来源、`shadow:true` 标记的证据。默认仍关闭；关闭时 replay 语义不变。

- **0.3.1 发布措辞**：可写"进化判决语义已闭环；开启 `shadow_execution` 后判决具备候选特异性（候选在隔离上下文真实执行）"；隔离执行的采样窗口与任务覆盖仍受 `sample_size` 与流量限制，不得写成"全量 A/B 验证"。

### N-2（缩水一）"真实 agent" = peer 拓扑，非单 agent 内 DAG

（Step Y.1 的改造对象：将供给**单 agent 内部工作流 DAG**，见 Step Y）

- **现状**：`buildLiveAgentDAG`（`cmd/ares/serve_live_dag.go:30`）建的是**一 peer 一节点**的顶层图（AgentType=主能力）。

- **边界**：`UpdateLiveDAG` 注入的是这个 peer 拓扑。若目标为"**单 agent 内部的工作流步骤图**"，那是另一个 gap，`UpdateLiveDAG` 喂进去的不覆盖它。

- **0.3.1 发布措辞**：写"进化作用于 peer 级 agent 拓扑"，不写"作用于单 agent 内部工作流"。

- **Step Y.1 状态**：已立项（2026-09-03），完成后 N-2 的"单 agent 内 DAG"部分即关闭。

### N-3（缩水二）测试覆盖率未达标的

- **现状**：总覆盖率 ≈**59.2%**，低于 GA 目标 **65%**。

- **薄弱区**：`shadow_sampler.go:93` 构造路径、`postgres/repositories`（0%，需真 PG）。

- **性质**：非发布硬阻断，但属于"质量就绪"欠账，应列入 0.4.x 补覆盖。

### N-4（收尾待办，非新缺口）非 serve 入口的显式日志 ✅ 已落地（2026-09-03，Step 7.3）

- `Bootstrap` 单独使用（非 serve）时保持合成图 + 关闭 deployment 的行为已在；显式日志已补（`bootstrap.go`，`cfg.Evolution.Enabled` 时输出 verdict-available-but-no-live-topology）。

- **性质**：行为已满足，仅是观测缺口，不改变闭环结论。

### N-11（新增，Step Y 交付前）跨 agent 协作与工具特征不进入进化判定

- **现状**：`agentipc.Bus`（132行）无协作 success/timeout 度量，协作反馈完全未进进化；`agents/sub/executor.go` 的工具成败仅进经验蒸馏旁路（`emitSubTaskResult`），未作 evolved genome 特征。

- **影响**：即使 N-1/Step4 的候选实跑做实、Step 7 打通 live DAG，「单 agent 任务 + 协作」仍缺协作与工具两维反馈——进化不知道"问哪个 agent 该不该"、"该调哪个工具"。

- **Step Y.2/Y.3 状态**：已立项（见 Step Y），完成后 N-11 即关闭。

- **0.3.1 发布措辞**：写"进化作用于单 agent 任务与 peer 拓扑"，**禁止**写"进化作用于跨 agent 协作与工具选择"（除非 Y.2/Y.3 已落地）。

***

## 现状结论

已经闭合的：`generateDiffPatches`（`internal/ares_evolution/genome_wiring_system.go:975`）确实完成了 Snapshot → Mutate → Snapshot → Diff 的完整链路，不是空壳；`DeploymentPipeline` 已经在 `internal/ares_bootstrap/bootstrap.go:537-553` 接进 `Coordinator.SetDeployer`。

没闭合的是**判决环节**：变异能产出补丁，补丁能进部署管线，但管线用来判断「这个补丁好不好」的那个分数是假的，而且判决口径和配置语义不一致。所以现在的系统是「能变异、能晋升，但晋升与否和补丁质量无关」。

***

## Step 1 — 修复晋升判据的语义错位（P0，必须先做）

**问题**：`internal/evolution/deployment/deployment.go:215`

```go
if shadowScore < dp.config.PromotionThreshold {
```

`PromotionThreshold` 的文档定义是「the minimum fitness **improvement** required，Default: 0.05 (5% improvement)」——是**增量**。但这里拿 `shadowScore`（绝对分）去比。配合 `deployment_wiring.go` 的 `coldStartScore: 0.5`，结果是任何补丁都以 0.5 ≥ 0.05 无条件通过晋升。这是当前闭环里最致命的一处：门槛存在但恒真。

**做什么**：

1. `DeploymentRecord` 增加 `BaselineScore float64` 字段。
2. **改** **`StagingRuntime.Evaluate`** **签名为一次返回双值**，而非新增一个独立的 `Baseline(ctx)`：

   ```go
   // internal/evolution/deployment/deployment.go
   type StagingRuntime interface {
       Apply(ctx, p patch.RuntimePatch) (*patch.RuntimePatch, error)
       // Evaluate returns (shadowScore, baselineScore, err). shadowScore 是
       // 当前补丁作用后的分；baselineScore 是同一时间窗内未打补丁的当前
       // 活跃分。两者必须在同一次调用 / 同一时间锚点内采样——若拆成两次
       // 独立调用，并发写 evidence 时两窗可能不一致，delta 失真。
       Evaluate(ctx context.Context) (shadow, baseline float64, err error)
       Rollback(ctx, rollback *patch.RuntimePatch) error
   }
   ```

   结论：**不要新增** **`Baseline(ctx)`** **单独方法**——那会让 baseline 与 shadow 落在两个不同时间窗。把"取 baseline"并入 `Evaluate`，由 `deploymentStagingRuntime` 内部保证同一次 `Window` 采样口径一致。
3. `Deploy` 判据改成增量：

   ```go
   shadowScore, baselineScore, err := dp.staging.Evaluate(evalCtx)
   record.BaselineScore = baselineScore
   delta := shadowScore - baselineScore
   if delta < dp.config.PromotionThreshold { reject }
   ```
4. reject 的 `Reason` 同步改成打印 `delta` 和 baseline，便于审计追溯。

**验收标准**：

- 新增测试：baseline=0.60 / shadow=0.62（delta=0.02 < 0.05）→ `DeploymentRejected`；baseline=0.60 / shadow=0.70（delta=0.10）→ `DeploymentPromoted`。

- 新增测试锁定回归方向：shadow 绝对分很高（0.9）但 baseline 更高（0.95）→ 必须 reject。修复前这个 case 会 promote。

- **窗口一致性测试**：同一次 `Evaluate` 内 shadow 与 baseline 必须采样自同一时间锚点（用记录 `agg` 窗口查询次数 / 时间戳断言为一次查询、同一 since/until）。

- `go test ./internal/evolution/deployment/...` 全绿。

***

## Step 2 — 让 staging 评估变成 per-patch，而不是全局常量

**问题**：`internal/ares_bootstrap/deployment_wiring.go:64-73`

```go
res := r.agg.Window(ctx, "")
```

空 strategy 过滤 = 全局窗口。**每个补丁拿到的 shadowScore 完全相同**，与补丁内容无关。同时 `Apply`（:47）明确注释 "Preflight only ... do NOT touch any registry state"，`Rollback`（:75）只是 `applyCount--`。也就是说 staging 从没真正装载过补丁，评估的是打补丁之前的世界。Step 1 修好判据后，如果这里不修，baseline 和 shadow 会取到同一个全局均值，delta 恒为 0，所有补丁恒被拒——从「恒真」翻到「恒假」，同样不可用。这两步必须一起上。

**做什么**：

1. 证据侧已经具备条件：`internal/ares_evolution/observer.go:204` 有 `evidenceKeyStrategyID = "strategy_id"`，`writeEvidence`（:270）会写入。把补丁的 strategy/target 标识透传进 staging。
2. `deploymentStagingRuntime` 增加 `currentPatchStrategy string` / `activeStrategyID string` 字段，`Apply` 时记录 `p.Target`/strategy id。**`Evaluate`** **内部取双分**（对应 Step 1 改后的签名）：

   - shadow ← `r.agg.Window(ctx, r.currentPatchStrategy)`

   - baseline ← `r.agg.Window(ctx, r.activeStrategyID)`

   - 关键：**在同一次调用内、用同一 since/until 时间锚点**取这两窗，避免并发写 evidence 时两窗错位（Step 1 的窗口一致性约束）。可先落一个锚点再以该锚点构造两个 strategy 过滤查询。
3. `Rollback` 里清空 `currentPatchStrategy`，避免跨补丁串味。

**验收标准**：

- 单测：向证据库写入两个不同 strategy\_id 的证据（A=0.9，B=0.3），依次 Deploy 两个对应补丁，断言拿到的 shadowScore 分别为 0.9 和 0.3，而不是同一个均值。

- 单测：证据库为空时 shadow 与 baseline 都回落 `coldStartScore`，不 panic、不返回 0 导致误拒。

- **窗口一致性测试**：`Evaluate` 返回的 shadow 与 baseline 采样自同一时间锚点（断言两次 `agg` 查询的 since/until 相同）。

- 并发安全：`deploymentStagingRuntime` 现在有跨调用可变状态，`Deploy` 虽然全程持 `dp.mu`，仍需 `go test -race` 覆盖。

***

## Step 3 — 补上 live 回滚路径，激活 RollbackThreshold

**问题**：两处死配置，grep 确认全仓无读取方：

- `RollbackThreshold`（`deployment.go:41`）只在结构体定义和默认值里出现，没有任何执行路径。

- `ShadowSampleSize`（:33）同样零读取。

根因在 `deployment.go:236`：

```go
_ = liveRollback // retained for future live rollback
```

晋升后的回滚句柄被丢弃。而 `internal/evolution/patch/patch.go` 的 `Registry` 只有 `Register/Replace/Apply/ApplySet`，**没有 Revert**，所以回滚能力在底层就缺一块。

**做什么**：

1. `patch.Registry` 增加 `Snapshot(target)` + `Restore(target, snap)`：`Apply` 前抓取旧 executor/component，失败或回滚时还原。`ExecutorComponent.Snapshot` 目前返回 `ErrNoSnapshot`（patch.go:155），对这类组件退化为「保存旧 executor 引用并在 Restore 时 Replace 回去」。
2. `DeploymentPipeline` 保存 `liveRollback` 到 `DeploymentRecord`，并新增 `MonitorAndRollback(ctx, record)`：晋升后按 `EvaluationTimeout` 再采一次窗口分，若 `baseline - current > RollbackThreshold` 则调用 `live.Rollback` 并把状态改写为 `DeploymentRolledBack`。
3. `ShadowSampleSize` 二选一：接进 Step 4 的真实影子执行作为采样条数；若 Step 4 暂不做，则**从配置里删掉**，不留假旋钮。倾向删除——留着比删掉更有害。

**关键约束（修正）——live 目前没有真逆操作，Snapshot/Restore 是唯一回滚支点**：

- `deploymentLiveRuntime.Apply`（`deployment_wiring.go:94-99`）现在回传 **`&p`（同一指针）**，不是逆操作补丁。`deployment.go:236 "retained for future live rollback"` 丢弃的 `liveRollback` 因此**不是可执行的回滚句柄**。

- 所以 `MonitorAndRollback` 要真回滚，必须依赖 `patch.Registry` 的 `Restore` 还原 `Apply` 前的状态——**没有 Snapshot/Restore，"回滚到旧实例"就无从谈起**。结论：Step 3 的第 1 条（Snapshot/Restore）与第 2 条（回滚路径）**必须同批落地**，二者绑定，不能只做其一。

- `LiveRuntime` 接口也需同步加 `Rollback(ctx, snap)`（接收 `Snapshot` 产物）或让 `DeploymentPipeline` 直接操作 `patch.Registry.Restore`——取决于 live runtime 的封装边界，二选一并在实现里固定，测试按同一选择断言。

**验收标准**：

- 单测：晋升后注入一个回归到 0.4 的窗口（baseline 0.8，超出 0.10 阈值）→ 记录状态变为 `DeploymentRolledBack`，且 registry 里的 executor 已还原为旧实例（用指针相等断言）。

- 单测：回归幅度 0.05（未超阈值）→ 保持 `DeploymentPromoted`，不触发还原。

- `grep -rn "RollbackThreshold" --include="*.go" .` 必须出现在非 `_test.go` 的执行路径中（当前只有定义）。

***

## Step 4 — 影子评估接真实任务执行（工作量最大，可独立排期）

**问题**：`internal/ares_evolution/shadow_evaluator.go:439` `Evaluate` 是纯函数打分：

```go
activeScore := scorer(ctx, active)
shadowScore := scorer(ctx, shadow)
```

`scorer` 是注入的函数，不跑任务。全仓 grep `CopyTaskForShadow` / `ShadowExecutor` **零命中**——影子执行器不存在。所以「影子对比」目前是对策略对象打分，不是对策略产生的实际结果打分。

**做什么**：

1. 接线锚点已定位：`internal/kernelscheduler/scheduler.go:654` `executeWithCandidates`、:920 `buildQuantumStep`。新增 `ShadowExecutor`：复制 task 描述，用候选策略构建 quantum step，在隔离上下文中执行，产出的证据带 shadow 标记写入证据库（复用 `observer.writeEvidence`）。
2. 影子执行必须**只读副作用**：禁止写入生产 memory / 触发对外工具调用。这一条是硬约束，需要在 executor 层加显式开关，不能靠调用方自觉。
   **隔离边界（落地到可 review 粒度）**：`ShadowExecutor` 的执行上下文注入一个**副作用 deny-list ctx**，明确两类封禁——

   - 写生产 memory：任何经由 `agentfabric`/memory manager 的写路径一律短路，只允许读；

   - 触发外部工具：工具调用在影子 ctx 上被拦截（返回"shadow-mode: disabled"，不落盘、不发请求）。
     用**接口级 spy**（而非仅日志）在单测中断言整个影子执行期间对生产 memory store 的**写入调用次数 == 0**。
3. 配置：`configs/ares.yaml:105` 的 `evolution` 块下新增 `shadow_execution: {enabled: false, sample_size: N}`，**默认关闭**。全仓当前无 `shadow_execution` 键，属于新增。`sample_size` 若能落地即为 Step 3 的 `ShadowSampleSize` 提供真实取值前提。
4. `internal/ares_evolution/lifecycle.go:720` 的 `l.sampler.Prime(ctx, candidate, active)` 是现成的注入点，影子结果从这里回流。

**验收标准**：

- 端到端测试（挂在 `cmd/ares/e2e_grand_loop_real_test.go` 旁）：开启 shadow\_execution，跑一轮变异 → 断言证据库中出现带 shadow 标记的新证据，且 active/shadow 两侧 evidence count 均 > 0。

- 隔离性测试：影子执行期间对生产 memory store 的写入次数必须为 0（用 spy store 断言，且工具调用也被拦截）。

- 默认关闭验证：不改配置时行为与当前完全一致，`make gate` 无差异。

***

## Step 5 — 消除 G3 gate 的静默恒过

**问题**：`internal/ares_evolution/gate_eval.go:97`

```go
if g.registry == nil || g.runner == nil || len(g.suite.TestCases) == 0 {
    return true, 0, "eval suite not configured, skipping"
}
```

未配置即返回 `true`（通过）。`:135` 还有第二个同样的 pass-through（"no evaluators produced results, skipping"）。grep `g3.skipped` / `GateG3Skipped` **零命中**——跳过没有任何计数器或指标。生产环境里如果 registry 忘接，G3 会永久静默放行，且无从察觉。

**做什么**：

1. 增加 `skippedCount` 计数与结构化 warn 日志，跳过时输出具体缺失项（registry / runner / 空 suite 分别区分）。
2. 增加 `StrictMode bool`：开启时未配置直接返回 `false`（拒绝），而不是放行。生产配置置为 true。
3. 暴露跳过次数到可观测面，与现有 evolution 指标同路径。

**验收标准**：

- 单测：三种缺失场景各自产生一次 skip 计数且日志含对应原因。

- 单测：`StrictMode=true` + registry 为 nil → 返回 `false`，且 reason 明确说明是严格模式拒绝。

- 现有 `TestG2ConfigContract` 与 `make gate` 保持绿；`StrictMode` 默认 false 以免破坏现有 gate。

***

## 执行顺序与依赖

Step 1 和 Step 2 **必须同批上线**：单独做 Step 1 会把「恒真放行」翻成「恒假拒绝」，单独做 Step 2 则判据仍然错。这是一次原子修复。

Step 3 依赖 Step 1 的 `BaselineScore` 字段。Step 5 完全独立，可以随时插入。Step 4 工作量和风险都远高于前四步（涉及调度器和副作用隔离），建议单独排期，前四步先交付。

## 每步统一的验证动作

```
go build ./... && go vet ./... && gofmt -l .
go test -race ./internal/evolution/... ./internal/ares_evolution/... ./internal/ares_bootstrap/...
make gate
```

`make gate` 的实际内容是 `scripts/g1_reachability_gate.sh` + `TestG2ConfigContract` + `TestEventContract` + `-race -tags closure` 跑 `ares_evolution` 与 `ares_bootstrap` 两个包，所以 Step 1/2/3 的改动都会被它覆盖。

## Step 6 — 打通补丁的策略归因，解开 deployment 的自锁（P0）

Step 1/2 落地后暴露出一个原计划没写到的前置缺口：**没有任何补丁生产方会填 strategy 归因**。

- `RuntimePatch.Source` 装的是提议者类别（`diff.memory` / `candidate` / `ga`），`Target` 装的是组件名；证据库里的 `strategy_id` 来自 mutation 策略生命周期。这三个是互不相交的命名空间。

- 所以 per-patch 打分要用的那个 key 不存在。早期实现拿 `Source`/`Target` 兜底，结果是查询恒不命中、恒回落 cold-start，而返回值看起来仍像一次真实测量——这比不打分更危险。

- 现在的处理：`RuntimePatch` 新增 `StrategyID` 字段，`Apply` **只认** `StrategyID`，不做任何兜底；`Evaluate` 在任一侧 ID 为空时两侧同时返回 cold-start，delta 恒为 0，被正阈值拒绝；`deploymentAdapter.Deploy` 在入口就以显式错误拒绝无归因补丁，避免拒绝原因显示成 `delta 0.000`（读起来像"测量出来打平"）。

**当前后果**：`evolution.deployment.enabled=true` 时每个补丁都会被拒。这是有意的失败闭合——判决语义已正确，但判决所需的输入还没接上。deployment 默认 `Enabled: false`，所以默认行为不变，`make gate` 无差异。

### 6.1 生产侧：把策略 ID 透传进补丁

`generateDiffPatches(ctx, genomeReg, diffReg, nChildren)` 增加 `strategyID string` 形参，对返回的每个 patch 统一打标 `StrategyID`。**`strategyID == ""`** **直接返回 error**（fail fast），不允许不可归因的补丁流入 Coordinator。

两个调用点各自提供 ID，且必须与证据写入方用**同一个 key**：

| 调用点                                               | 传入的 ID                        | 谁往同一 key 写证据                                         |
| ------------------------------------------------- | ----------------------------- | ---------------------------------------------------- |
| `genome_wiring_system.go:915`（`RunIdleEvolution`） | `system.Population` 当代最优个体 ID | `recordOutcomesLocked`                               |
| `genome_wiring_run.go:416`（`submitToCoordinator`） | 该轮候选个体 `child.ID`             | `genome_wiring_run.go:367` 已写 `StrategyID: child.ID` |

第二个调用点是关键：`recordOutcomesLocked` 已经在用 `child.ID` 写 fitness 证据，所以补丁打上同一个 `child.ID` 后，`agg.Window(ctx, child.ID)` 立刻命中，shadow 侧不再恒回落 cold-start。这是打通归因**唯一必要**的一处对齐，不需要新造标识体系。

### 6.2 baseline 侧：`activeStrategyID` 必须每次 Evaluate 现取

现存缺陷（`bootstrap.go:547-553`）：`staging.activeStrategyID` 在 wiring 时从 `ASM.Current()` **取一次就冻结**。ASM 在运行期会晋升/回滚策略，于是 baseline 会指向一个早已不活跃的策略——delta 的分母漂了，而且不报错。

改法：`deploymentStagingRuntime` 持有 `asm *evolution.ActiveStrategyManager` 引用而非字符串快照，`Evaluate` 内调 `asm.Current()` 现取。`Current()` 已加读锁并返回 `Clone()`，并发安全。保留字符串字段仅供测试注入。

### 6.3 验收

- 候选补丁的 `StrategyID` == 证据写入的 `child.ID`，`Evaluate` 的 shadow 侧读到真实证据（非 0.5 cold-start）。

- ASM 晋升后再 `Evaluate`，baseline 跟着切到新的活跃策略。

- `generateDiffPatches` 传空 ID 返回 error。

- 端到端：`deployment.enabled=true` 时补丁**能被判决**（可晋升也可拒绝），不再一律被拒。这是本步的成功标志。

***

## Step 7 — 让进化真正作用于真实 agent（P0，闭环最后一环）

原计划把这条列为"不在计划内"，**该表述已过期**。核实后的实际状态：

**已经通了的部分**：`cmd/ares/serve_agents.go:66-81` 已调用 `buildLiveAgentDAG(cfg)` 把配置里的 agent 群体物化成真实拓扑（一 peer 一节点，`Capabilities[0]` 作 AgentType，沿用 legacy `Dependencies` 作边），注册到 `ares_runtime.AgentDAGLiveKey`，并 `UpdateLiveDAG(liveDAG)` 注入进化执行器——内部用 `graphExec.SetGraph` / `recoveryExec.SetDAG` **就地**替换占位 DAG（`Register` 无法覆盖已注册 key，这是 `provide_new_evolution_live_dag_test.go` 记录的已修缺陷）。memory 补丁本来就写 live `comp.Memory`。`serve_live_dag_test.go` 的 `TestUpdateLiveDAG_WiredFromServeShape` 已断言 recovery 补丁落在 live DAG 的 steps 上。所以"补丁只作用于占位物"对 serve 路径已不成立。

**真正剩下的洞（比占位更糟）**：`provide_new_evolution.go:341-343` 的注释自己写明 —— *"The genome registry's WorkflowGenome is NOT updated here because it needs a full re-registration"*。于是形成**跨图错配**：

- `WorkflowGenome`（`provide_new_evolution.go:119` 构造）仍包着 bootstrap 那个 3 步合成 DAG（`input→process→output`），**变异是在合成拓扑上算的**；

- 但补丁被应用到真实 agent DAG 上。

- 结果：patch 里的节点 ID 在目标图里根本不存在。这不是"作用于占位物"，是**在 A 图上推理、往 B 图上写**——要么静默 no-op，要么按不存在的节点报错，两种都会被误读成"补丁质量差"，而根因是拓扑源错了。

### 7.1 让 WorkflowGenome 跟上 live DAG

沿用执行器已有的就地更新模式：给 `WorkflowGenome` 加 `SetDAG(*engine.MutableDAG)`，由 `UpdateLiveDAG` 一并调用；若 genome registry 需要覆盖注册，则给它加 `Replace`。**不要**用"重新 Register"绕——已注册 key 无法覆盖，那是注定失败的 no-op，正是 7 前半段那个已修缺陷的同一个坑，别重犯。同时删掉那条"NOT updated here"的注释，它已不再描述实现。

### 7.2 补上"改动确实落到真身上"的断言

现有测试只断言注入链**被调用**，没断言**效果**。新增验收：

1. **同图性**：`UpdateLiveDAG` 后，`generateDiffPatches` 产出的 workflow patch 的节点 ID ⊆ live DAG 节点 ID 集合。这条直接钉死跨图错配。
2. **可观测变更**：一个 workflow 补丁走完 `Deploy`（`deployment.enabled=true`）后，`runtime.GetAgentDAG(AgentDAGLiveKey)` 的拓扑发生预期变化；`Rollback` 后恢复原拓扑。
3. **隔离不回退**：`closure_contract_test.go` 的 F04 断言（Bootstrap 时合成图只在 `evolution` key、不得占用 live key）保持通过——本步是给 live key 补供给，不是放宽隔离。

### 7.3 非 serve 入口的显式表态

Bootstrap 单独使用时没有 agent population，也就没有 live DAG。此时不要伪造一个：保持合成图 + 保持 deployment 关闭，并在日志里说明"判决可用，但无 live 拓扑可作用"。**沉默地在合成图上晋升，正是这份计划要消灭的那类问题。**

***

## 交付边界（更新后）

Step 1-5 交付「判决语义正确」；Step 6 交付「判决输入可归因」；Step 7 交付「判决作用于真实 agent 拓扑」；Step 4 落地后追加交付「候选特异性判决（真实执行 A/B，默认关闭）」；**Step Y 追加交付「单 agent 任务 + 跨 agent 协作 + 工具特征 三通道真实闭环」**。全部落地（含 Step Y），`进化已作用于真实 agent（单任务 + 协作）` 才可以写进发布文档。仍需如实声明的边界：N-3（覆盖率 59.2% < 65%，0.4.x 补覆盖）与 N-11（Step Y 落地前，跨 agent 协作与工具特征不进入进化判定）。

***

## Step Y — Y 方案：打通「单 agent 任务 + 跨 agent 协作」的真实闭环（用户确认方向：2026-09-03）

> **第一性原理（用户）**：agent 不可能主动感知外部世界，它只能通过三条通道被动感知——**单 agent 任务、工具调用、跨 agent 协作回执**。进化的输入必须是这三条通道真实观察到的反馈，不能是外部臆测。
> **目标**：让进化环 ①干活→②度量→③判定→④改→⑤部署→回到① 覆盖**单 agent 任务 DAG + 跨 agent 协作**，而不只是 peer 拓扑。判定标准仍是一条：**下一代的 agent，因上一代三条通道的反馈而变得更好。**

### Y.0 现状核实（已对照代码，2026-09-03）

| 通道         | 是否有反馈       | 反馈进进化吗                | 代码位置                                         |
| ---------- | ----------- | --------------------- | -------------------------------------------- |
| 单 agent 任务 | ✅           | ⚠️ 只进 peer 拓扑         | `agentloop.Engine.Run` / `executor.Execute`  |
| 工具调用       | ✅           | ❌ 仅进经验蒸馏旁路，**未作进化特征** | `executor.go:924 executeToolCall`→`CallTool` |
| 跨 agent 协作 | ❌ **无回执度量** | ❌ 完全没进进化              | `agentipc/bus.go`（132行，无 success/timeout 度量） |

**结论**：`ShadowExecutor`（N-1/Step4）已把"候选实跑"做实，但它喂入的是 scheduler 捕获的**任务**；**跨 agent 协作和工具特征这两个通道仍是进化盲区**。这恰是 Y 要补的，也是"真实 agent = 单任务 + 协作"里唯一没闭环的部分。

### Y.1 单 agent 内部任务 DAG 供给（闭 N-2 的一半）

**问题**：`buildLiveAgentDAG`（`serve_live_dag.go:30`）建的是 peer 拓扑；单 agent 内部的任务流转 DAG（`agentloop` 里 agent 实际跑的那套）**没有供给** **`UpdateLiveDAG`**。进化改了 peer 图，却改不到"单个 agent 怎么干活"。

**做什么**：

1. 定义一个 `SingleAgentDAGProvider`：在 serve 侧把**具体某个 agent 的活动工作流**（其构建好的 `engine.MutableDAG`，包含真实 step/依赖）暴露出来。
2. `UpdateLiveDAG` 增加按 agent 维度注册的能力：`UpdateLiveAgentDAG(agentID, dag)`，与 peer 级 `UpdateLiveDAG` 并存（peer 管协作拓扑，单 agent 管内部步骤）。
3. `agentloop` 执行时，把该 agent 的步骤 DAG 注册进来，让 workflow patch 能改"单 agent 内部怎么编排步骤"。

**验收**：单 agent 的步骤 DAG 注入后，`generateDiffPatches` 产出的 patch 节点 ID ⊆ 该单 agent DAG ∪ 新引入节点；一个单-agent workflow patch Deploy 后，`runtime.GetAgentDAG(单agentKey)` 拓扑变化且 Rollback 复原。

### Y.2 跨 agent 协作反馈进进化（闭最大的新洞）

**问题**：`agentipc.Bus` 无任何协作成功/超时/成败度量。协作作为 agent 感知世界的关键通道，反馈从未进过进化。

**做什么**：

1. `agentipc.Bus` 增加协作观察者：在 `Request`/`Reply`/`timeout`/`ErrAgentNotRegistered` 的出口记录一条 `collaboration` 类事件——`(initiator, target, topic, outcome, latency)`。
2. 写进 `writeEvidence` 同构：`source="collaboration"`，payload 含 `strategy_id`（发起方/接收方策略）、`outcome`（success/timeout/not\_found）、`latency`。
3. 新增一个 `CollabGenome`（或扩展现有 genome）把"问题该问哪个 agent / 协作该不该发起"作为可进化策略，用协作成功率做 fitness。
4. shadow 执行时同样拦截协作调用（deny-list 扩到协作回执），与工具 deny 同级。

**验收**：两条 agent 间跑 N 次协作，进化能读到 `collaboration` 事件并按 `outcome` 统计成功率；候选策略的影子协作被拦截（计数 0 真实回执）。fitness 反映协作质量。

### Y.3 工具调用反馈作进化特征（闭工具通道）

**问题**：工具成败只进经验蒸馏旁路（`emitSubTaskResult`），未作 evolved genome 特征。

**做什么**：在 `executor.go:924 executeToolCall` 的 `CallTool` 结果处，把 `(tool, success, latency)` 写一条 `source="tool_call"` evidence；新增 `ToolSelectionGenome` 进化"该任务该不该调某工具 / 工具调用顺序"，fitness = 工具成功率加权。

**验收**：进化能看到每个工具的真实成败；一个"少调无用工具"的候选能因工具成功率提升而被 promote。

### Y.4 三条通道合并成"一条线"（收口）

**做什么**：把三条通道的 evidence 用同一个 `strategy_id` 聚合，喂给同一个判定（复用 N-1 的 ShadowExecutor 配对框架）。`strategy_shadow` 证据扩展为包含任务/工具/协作三项子维度，G2 一次判定读全维度。

**验收**：一个候选策略的 G2 判定同时基于它自己的任务、工具、协作三项真实表现——"进化让 agent 变好"从单一维度变三维可验证。

### Y.5 一致性前提（不破坏已落地实现）

- 新增通道全部走**独立 evidence source**（`collaboration` / `tool_call`），不污染 `strategy` 与 `strategy_shadow`（守住 N-1 的隔离契约）。

- 全部默认关闭或显式开关（`evolution.collab_feedback` / `evolution.tool_features`），默认行为与当前 `make gate` 无差异。

- `ShadowExecutor` 的 deny-list 扩到协作；不改其现有任务执行语义。

### Y 验收（端到端）

开启全部开关，跑一轮完整进代：单 agent 任务 + 工具 + 协作三通道各产生真实反馈 → 进化为候选评估 → 候选因某项真实提升被 promote → 部署后同 agent 下一轮表现提升。这才配写「进化已作用于真实 agent（单任务 + 协作）」。

***

## Step Y 附带 code review（对已落地实现的针对性复核）

> 针对 Step 4/N-1 已落地的 ShadowExecutor 实现做了复核，结论：质量高、意图正确，无 P0 bug。

| 检查点                                                                        | 结果                                                                                    |
| -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `shadow_executor.go` `shadowEvidenceSource="strategy_shadow"` 独立 source    | ✅ 正确——不污染 `strategy`，守住 rollback/staging 不被影子记录污染（注释已说明）                              |
| `shadowTaskBuffer=32` 最近任务缓冲                                               | ✅ 合理，证据来自近期流量                                                                         |
| `shadowExecTimeout=15s` 外层超时                                               | ✅ 防止 Prime 内联阻塞进化心跳                                                                   |
| deny binder（`shadowDenyBinder`）接口级 `ListTools`空+`CallTool`拒绝、生产 binder 零接触 | ✅ 隔离硬边界，spy 断言                                                                        |
| `ShadowSampler.SetExecutionFeeder` 无产出回退 replay                            | ✅ 语义不变，安全降级                                                                           |
| 配置默认关闭                                                                     | ✅ 不改变现有行为，`make gate` 无差异                                                             |
| 唯一保留                                                                       | ⚠️ 候选实跑只在 `evolution.shadow_execution.enabled=true` 才触发；默认关闭时仍是 replay（N-1 已如实标注，非缺陷） |

**新增问题**（Y 范围内）：`agentipc.Bus` 无协作度量（Y.2）与 `executor` 工具特征未进进化（Y.3）——两者是 Y 的交付对象，非已落地代码的缺陷。

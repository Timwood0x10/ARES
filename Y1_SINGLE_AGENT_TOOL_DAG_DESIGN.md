# Y.1 单 agent 内部"工具调用 DAG"设计对比

> 状态：**架构决策草案（未实施）**。用于对比两个方案，供用户定案后再落地。
> 关联：`AGENT_OS_CLOSURE_DEV_PLAN.md` 的 Y.1 / N-2 / N-12。
> 日期：2026-09-04 · 综述人：Timwood0x10（AI review 协作）

***

## 0. 一句话结论

把"单 agent 内部如何干活"从进化不可达（ReAct 消息循环）变成进化可达，核心是把**工具调用**表达成一张可变 DAG。本文对比两个实现方案：

- **方案 A**：DAG **节点装载** ReAct 循环（节点 = 一个认知 agent，节点属性 = 工具白名单/提示词）。

- **方案 B**：把**每一轮实际发生的工具调用轨迹投影成 DAG**（运行时 ReAct 保持自主，旁路沉淀轨迹图，进化 patch 该图回灌为偏好先验）。

其余组合（纯扁平化重写执行体为预定图）经核实**成本与风险高一个量级且削弱 LLM 自主性**，已排除，仅在附录给出排除理由。

***

## 1. 背景与现状（已核实代码事实）

### 1.1 生产执行体是 ReAct 消息循环，不是 DAG

- `internal/agentfabric/chat_cognition.go`：peer 模式生产主路径。

  - 状态 `chatStepState{Round, MaxRounds, Messages[], Prompt, Params}`（`chat_cognition.go:77-85`）——线性消息数组 + 轮次计数器，**无节点/边**。

  - `chatStep`（`:303-382`）每轮：Chat API 一次 → 得到 0..N 个 `ToolCalls` → 逐个执行 → 观察 append 回 `Messages`。

  - `ExecuteStep`（`:184-266`）是**调度量子**语义：一轮不结束就 yield（`StepOutcome{Checkpoint: st}`），调度器在 fabric 的 check point slot 存/取 `chatStepState` 续跑。

- `internal/agents/sub/executor.go`：同样 ReAct 工具循环（`:860-874`），经 `ExecuteStep` 委托（`agent.go:348`）。两条执行体差异仅在 A1.4 端口方式，语义一致。

- `internal/agentloop/engine.go`：同为 ReAct。非 peer 生产主路径。

> **关键**：`chatStepState` 是 **resume 的 PCB（schema 版本校验，§6 持久化规范）**。任何要改动它形状的重构，都会触碰 quantum/checkpoint/yield/调度量子契约。

### 1.2 进化 DAG 基建已完整可复用

- `MutableDAG`（`workflow/engine/mutable_dag.go`）：增/删/替换节点、增删边、`SchedulerType` 字段、线程安全、版本号、GraphEventHub。

- `DAGPatchExecutor`（`workflow/engine/dag_patcher.go`）：`PatchInsertNode/RemoveNode/ReplaceNode/AddEdge/RemoveEdge/SetSchedulerType`，全部直接改 `MutableDAG`。

- `generateDiffPatches`（`ares_evolution/genome_wiring_system.go:962-1058`）：parent snapshot → mutate → child snapshot → differ → `RuntimePatch`。

- `UpdateLiveDAG`（`ares_bootstrap/provide_new_evolution.go:350-432`）：接住 live DAG，`WorkflowGenome.SetDAG` + 重建 graph executor / recovery executor 指向 live DAG。

- `Step` 节点（`workflow/engine/types.go:52-68`）：`ID/AgentType/Input/DependsOn/Timeout/RetryPolicy/RecoveryPolicy/Metadata`。

- 工具白名单旋钮已落地（Y.3-ACT）：`Params["tools"]`（`agents/strategy.go` 的 `ToolWhitelistFromParams`）已接入 `chat_cognition.go:311` 与 `sub/executor.go` 两条执行体，空值=全量。

### 1.3 当前缺口（Y.1 要闭的）

`UpdateLiveDAG` 喂给进化的是 **peer 拓扑（一 peer 一节点）**（`serve_live_dag.go:14-75`），不是单 agent 内部步骤。进化能改 peer 图、能改活跃策略的工具白名单，但**改不到"单个 agent 干活时每一步工具调用的选择/顺序"**——N-2 的"单 agent 内 DAG"部分未闭环。

***

## 2. 方案 A：DAG 节点装载认知（节点 = 一个认知 agent）

### 2.1 是什么

用 DAG 的**单节点**装载一个 agent 的完整 ReAct 执行。节点的**属性**（`Metadata`，或直接并入活跃策略的 `Params["tools"]`/prompt）决定该 agent 内部工具使用行为。进化改节点属性 = 改单 agent 行为。

```
              ┌─────────────────────────────────────────────┐
   peer DAG    │  Step{AgentType: "coder", Metadata: {        │
   node ─────▶ │     tools: "web_search,calculator", ...      │
              │  }   └─ 装载 chatCognition（ReAct 自主循环） ──┤
              └─────────────────────────────────────────────┘
```

### 2.2 影响面（最小）

| 维度         | 影响                                                      |
| ---------- | ------------------------------------------------------- |
| checkpoint | **零影响**——节点只是容器，`chatStepState` 不碰，量子/yield 不变          |
| yield/调度   | 一个节点=一次任务=一次量子，与现有调度完全正交                                |
| 执行体        | **零改动**——ReAct 循环原样跑                                    |
| 进化作动       | 改节点 `Metadata`/策略 `Params["tools"]` → 下一轮该 agent 工具白名单变 |
| 归因         | 直接复用已闭环的会话 `collaboration`/`tool_call` EvidenceKey 归因   |

### 2.3 交付内容

1. `buildLiveAgentDAG` 为每个 peer 节点写入其策略工具白名单（从活跃策略 `Params["tools"]` 读）到 `Step.Metadata`。
2. 进化 mutation 增加对"节点工具白名单"维度的变异（可在 `Params["tools"]` 上，**已存在** `Mutator.mutateTool`）——故**基本无需新算子**。
3. 执行体读取策略白名单过滤——**已落地**（Y.3-ACT）。
4. 装配：把节点属性回流为活跃策略，或直接让执行体从节点 Metadata 读白名单。

> 实质：**Y.1 的 60% 已经通过 Y.3-ACT + 现有 MutableDAG 达成**。A 主要是"把已有的东西接起来 + 把作动语义写清楚"。

### 2.4 优点

- 范围小、风险低、不触碰 checkpoint/yield。

- 完全顺着 ReAct/LLM 自主本质，不削弱灵活性。

- 复用已有全部基建（Y.3-ACT 白名单、MutableDAG、generateDiffPatches）。

- 作动语义清晰："进化决定 agent 该用哪些工具"。

### 2.5 缺点

- "图"的粒度只到 **peer 级/单节点**，尚未表达**单 agent 内部的多步工具轨迹**——不能满足"每一步工具调用都是 DAG 节点"的诉求。

- 进化改的是"白名单集合"，不是"调用顺序/依赖图"。

***

## 3. 方案 B：ReAct 工具调用轨迹投影成 DAG（运行时自主 + 旁路轨迹图）

### 3.1 是什么

保持运行时 ReAct **完全自主**（LLM 每轮决定调哪些工具，不改执行模型）。同时，把**实际发生过的工具调用轨迹**，投影成一张 tool-call DAG：

- 每个出现的工具调用 = 一个 DAG 节点（`nodeID = toolName#seq`）。

- 一轮内的并行工具调用 = 同一层的多个节点（可作为兄弟节点）；

- 跨轮 `依赖前面的观察` = 边（前一轮工具节点 → 后一轮依赖它的节点，按消息里的观察顺序/血缘推断）。

- 整张图**旁路沉淀**（如写 `strategy_shadow` 或新 source / in-memory 投影），**不替代 ReAct 执行模型**。

```
ReAct 实际轨迹                        tool-call DAG（旁路投影）
─────────────────                     ─────────────────────────
Round1: web_search(▸)                [web_search#1] ──────▶ [calc#2]
Round2: code_exec(读取其结果)              ▲                      │
        + file_read                    search#1            code_exec#3
                                             └── file_read#4 (并行)
```

### 3.2 影响面

| 维度         | 影响                                                                                    |
| ---------- | ------------------------------------------------------------------------------------- |
| checkpoint | **旁路投影**，不碰 `chatStepState` 形状（但仍要在 checkpoint 里记录"已展开到哪"，否则 resume 后轨迹续不齐——见 3.3 风险） |
| yield/调度   | 投影发生在 `chatStep` 内部、执行工具后、yield 前，不改变量子边界                                             |
| 执行体        | **小改动**：在 `chatStep` 执行工具调用的循环里加"往轨迹	DAG 记一笔"的 hook（类似 `ToolCallObserver` 的位置）        |
| 进化作动       | patch 轨迹 DAG（增删/替换某类工具节点、调白名单）→ 把作动反向**回灌为下一轮的工具先验/偏好**（不强制）                          |
| 归因         | 复用 tool\_call 证据；轨迹 DAG 节点失败率可作为新的 fitness 维度（可选）                                     |

### 3.3 关键难点（如实列出）

1. **resume 续图**：多量子任务上，`chatStepState` 是 resume 的 PCB。轨迹 DAG 若不在 checkpoint 里带"已展开索引"，resume 后新 quantum 无法续接轨迹。需要在 `chatStepState`（或旁路附加字段）加一个**兼容字段**（如 `ToolTraceSeq`），schema 从 1 → 2。此改动触碰 checkpoint 读的旧版路径（`decodeChatStepState` 需容忍缺字段）。
2. **边/血缘推断**：ReAct 消息里工具观察是 `tool` 角色的 message，通过 `ToolCallID` 关联；但"后一轮工具依赖前一轮哪个观察"是**启发式推断**，没有显式依赖声明。图结构因此是"近似轨迹"，非精确语义——mutation 在这些近似边上作动的含义需写清楚。
3. **作动回灌的语义**：patch 出"删掉某工具节点"后，运行时**不能直接把工具从 LLM 禁用**（那又变回白名单，削弱自主）；更合理是作为**先验/提示**注入（如提示词里标注"此任务准备用 X 工具的倾向降低"）或调整 `Params["tools"]` 白名单的上界。这个回灌通道是 B 的真正新增工作量。
4. **轨迹图的有效性**：若每轮轨迹差异巨大（LLM 高度非确定），沉淀出的图会碎片化，作动噪音大。需按 pattern（相同工具序列的聚合统计）收敛，而非逐条轨迹。

### 3.4 优点

- 精确满足"每一步工具调用 = DAG 节点"，且**不重写执行模型、不削弱 ReAct 自主**。

- 复用 `MutableDAG`/`DAGPatchExecutor`/`generateDiffPatches` 作动轨迹图。

- 图具有真实语义（真实走过的调用），比预定笼子可解释。

- 可渐进：轨迹图先只作"观测/审计"，成熟后再开"回灌作动"。

### 3.5 缺点

- 比 A 复杂一个档次：需处理 resume 续图、边/血缘推断、作动回灌三块新逻辑。

- checkpoint schema 需升版（1→2），触碰持久化规范 §6.1。

- 作动回灌的"先验"落地方式尚无现成通道，需新增（提示词注入或新的先验字段）。

***

## 4. 两方案直接对比

| 维度               | 方案 A（节点装载）    | 方案 B（轨迹 tool DAG）           |
| ---------------- | ------------- | --------------------------- |
| 满足"工具调用是 DAG 节点" | ❌ 只到 peer/节点级 | ✅ 精确                        |
| 执行模型 / ReAct 自主  | 完全不动          | 完全不动                        |
| checkpoint       | 零影响           | 需 schema 1→2（续图索引）          |
| yield / 调度量子     | 零影响           | 小影响（量子边界不变，内加 hook）         |
| 复用基建             | 几乎全部复用        | 复用大部分 + 新增轨迹投影/回灌           |
| 主要新增工作量          | 很小（接线 + 语义文档） | 大（续图推断 + 血缘推断 + 作动回灌 + 图聚合） |
| 作动语义清晰度          | 高（工具白名单）      | 中（近似轨迹 + 启发式边）              |
| 发布 0.3.1 风险      | 低             | 中偏高                         |
| 进化精度             | 粗（白名单集合）      | 细（到工具调用级）                   |

***

## 5. 我的建议

- **如果你要的是"现在就把 Y.1 闭环、稳进 0.3.1"**：方案 A。它已由 Y.3-ACT 完成大半，今天是可交付的，风险极低。

- **如果你真正欣赏的是"每一步工具调用成为 DAG 节点"（您明确表达的）**：方案 B。它**不牺牲 ReAct 自主**（这是它比"扁平化重写"高明之处），但需要按 §3.3 处理续图/血缘/回灌三块，工程量与风险明显高于 A。

**两者不冲突，是递进**：A 先立好"单 agent 用哪些工具"的可进化旋钮；B 在 A 之上把"用过的工具轨迹"沉淀成图、作动到调用级。建议**分两阶段**：本轮以 A 收口 Y.1 并锁定发布措辞；B 作为下一迭代立项，先在计划文档记下设计，经您再次认可后实施。

***

## 6. 附录：已排除的"扁平化重写执行体为预定图"及其理由

因您曾倾向让"运行时执行体真按 DAG 分步推进"，此处明确排除理由：

- 若把 ReAct **每一轮**硬编码成 DAG 节点且要求**运行时按图推进**，则：

  1. 强制给 LLM 画预定步骤 ── LLM 不按图走即语义错位，**削弱自主性**；
  2. `chatStepState` 需存全图进度 + 每节点状态 → schema 重写、yield 语义重定义 → 触碰 quantum/checkpoint/调度契约，风险与成本最高；
  3. 与您欣赏的"工具调用是 DAG 节点"相比，它表达的是"预定步骤"，不是"真实工具轨迹"，反而不够自然。

- 结论：**以 B（旁路轨迹投影）替代它**，既保留"工具调用成节点"，又不动 ReAct。

***

## 7. 待定项（决定后再实施）

1. 选 A（收口本轮）还是 B（轨迹 DAG 立项），还是 A→B 分阶段。
2. 若 B：轨迹 DAG 的边/血缘推断粒度、作动回灌的落地通道（提示词先验 vs 白名单上界）、轨迹聚合的 pattern 阈值、checkpoint schema 升级方案（1→2 向后兼容）。

## 8. 用户 review 修正（2026-09-04，全部经代码核实）

> 上表初稿对方案 A 的"易交付、低风险"判断**被证伪**。以下修正逐条对应初稿的错误。

### 8.1 方案 A 的作动通道实际是断的（初稿 §2.3 判断错误）

初稿说 A"基本无需新算子"。核实否定：

- 进化快照单位是 `engine.DAG`，节点类型 `DAGNode{StepID, InDegree, OutDegree}`（`workflow/engine/types.go:170`）——**不含 Metadata**。

- `WorkflowDiffer.Diff` 只比 Nodes 增删 + Edges 增删（`evolution/diff/workflow_differ.go`），metadata-only 改动产 0 个 patch。

- `DAGPatchExecutor` 仅支持 Insert/Remove/Replace/AddEdge/RemoveEdge（`dag_patcher.go:108`），无 metadata 算子。

- `mutateReplaceNode` 新建 Step 只拷 `ID/Name/AgentType/Input/DependsOn`（`workflow_genome.go`），会**抹掉 Metadata**。

∴ A 真实工作量 = 新增 `PatchSetNodeMetadata` + 扩 `DAGNode` 快照 + 修 genome 变异保留 metadata，**不是接线**。

### 8.2 生产路径上 `mutateTool` 是死代码（初稿"已存在"误导）

`buildMutator` 只传 `WithPromptPool`/`WithSeed`（`genome_wiring_system.go:187-195`），`SystemConfig` 无 ToolPool 字段；`WithToolPool` 唯一非测试调用在 `api/evolution`（公共 SDK，非 serve 路径）。∴ `hasTool := len(toolPool)>0` 恒 false → 工具变异永不选中；即便选中，len≤1 直接返回 clone（no-op）。

### 8.3 唯一活着的工具变异路径会清空 LLM 工具列表（现存 bug，非 Y.1 引入）

`guidedMutateTool`（`EnableExperienceGuidedMutation` 默认 true，`bootstrap_steps.go:313` 已接 GuidanceProvider）的词表来自 `extractToolNames`，`knownTools = {search,read,write,...}`（`experience_hints.go:160`），与实际注册工具名（`web_search/calculator/json_tools/...`）**零交集**。后果链：`Params["tools"]="search"` → 过滤后 `llmTools` 空 → 但 `chatAvailable` 用未过滤 schemas 判断（`chat_cognition.go:305`）照常进 Chat，模型拿零工具。**已修复**（见 §8.6 guard）。

### 8.4 ActiveStrategy 是全局单例，A 的"该 agent"语义不成立

`PGStrategyStore.GetActive` 是 `WHERE is_active=TRUE ... LIMIT 1`（无 agent 维度），改 `Params["tools"]` 影响**所有 agent**。初稿 §2.2"下一轮该 agent 工具白名单变"错误。per-agent 必须走节点 Metadata，而那条路被 §8.1 堵住。

### 8.5 装配缺口比初稿 §7.3 暗示的大

链路已通一半：`Step.Metadata → ProjectStep 合入 PlanStep.Payload（projection.go:63-65）→ CheckpointEnvelope.Payload`。但**无任何执行体从 task.Payload 读 tools**——`ParamKeyTools` 全仓只在 strategy.go 出现。缺的是 `renderPromptAndParams` 里 merge `task.Payload["tools"]` 进 params，并定义节点 vs 全局策略优先级。

### 8.6 修复与重新排序（已按此执行）

按用户建议优先级落地：

1. **✅ 已修**：空白名单 guard——过滤后 `len(filtered)==0` 时回退全量而非留空（`chat_cognition.go` + `sub/executor.go` 两处），加 warn 日志 + 回归测试 `TestToolWhitelistZeroIntersectionFallsBackToFullSet`。
2. **待**：§7.3 明确 `Payload["tools"]→params` 装配 + 优先级规则；**补** **`agentloop/engine.go`** **第三条执行体**（初稿"两条"漏了 SDK 路径）。
3. **待**：词表对齐（`knownTools` ↔ 实际注册工具名），否则 guided mutation 持续写出无效白名单。
4. **待**：工具级 fitness 维度（初稿标"可选"→ 实为 A/B 成立前提，否则 GA 无法收敛到"哪个工具集更好"）。
5. **待**：安全护栏——`EvolutionGuardrails` 只管 score/lineage，需加工具集 allowlist 上界校验。

### 8.7 方案 B 的 checkpoint 门槛被初稿高估

初稿 §3.3.1 说 B 需 checkpoint schema 1→2。**否定**：`EventToolCallStarted/Completed` 已带 `tool_name+tool_call_id`，且在每轮 yield 前落到 EventStore（`chat_cognition.go:358-375`）——轨迹可从事件日志重建，**零 checkpoint 改动**，只需给两事件补 `round` 字段（2 行）。

（若未来真要升版，还有两坑：`decodeChatStepState` 用严格 `!=`（`chat_cognition.go:289`），bump 会导致在飞 v1 checkpoint resume 失败，须先改 `>` 语义；且 `stepSchemaVersion` 有**两处独立定义**——`sub/executor.go:40` 与 `chat_cognition.go:47`。）

### 8.8 修正后的结论

- 初稿 §5"A 今天可交付、风险极低"**不成立**：A 需 metadata 算子 + 快照扩展 + genome 保留 + 词表对齐 + guard。

- 优先序改为：**先修现存 bug（guard+词表）→ 明确 Payload 装配 + 补 agentloop → 落工具级 fitness → 再定 A/B**。

- A（若选）必须承认需 metadata 维度 patch/diff/快照扩展；B（若选）走**事件日志投影**，**不动 checkpoint schema**。

- 无论 A/B，**先落工具级 fitness 维度**，否则作动无法被 GA 选择。

***

## 9. 第二轮 review 修正（2026-09-04，全部经代码核实）

> §8 纠正了"乐观但乐观在有检测"。本轮 review 更深一层，指出"**已落地代码里作动与选择所依赖的前置条件仍未验证**"。

### 9.1 所有待办的前置条件缺失：fitness 传导未经验证（最优先）

§8.6 第 4 条把"工具级 fitness 维度"列为 A/B 成立前提——**必要但不充分**。即便加了工具维度，若"基因差异 → score 差异"这条链路断裂，进化仍会在选择环节看到同一分。

**核实的机制修正**：`StrategyHash`（`scoring/hash.go:40`）用 `fmt.Fprintf("%s=%v", 每个 Params 键值)` ——它**已包含** **`Params["tools"]`**，所以工具变异的子代 hash ≠ 父代 hash，**不会误命中父代 ScoreCache 条目**（最初担心的 Clone 传 ScoreCache 那条，经核实 `ScoreCache` 以 hash 为 key 且 hash 含 tools，不成立）。

**但本质论点成立且最关键**：ScoreCache 的**预填充路径**（`genome_wiring_run.go:141-157` 的 `batchScorer`，N 个 agent 合并成 LLM 批量调用）——批量 scorer 是否真的把"不同 `Params["tools"]` 的工具调用成败差异"反映进分数，**无人验证**。如果 batch scorer 只用 prompt + 数值参数打分、忽略工具特征，那么工具变异的 fitness 差异**仍到不了选择环节**（尽管 ScoreCache key 本身无问题）。

∴ **必须把一条端到端断言排在 A/B 所有工作之前**：

> "两个仅 `Params["tools"]` 不同的策略，在工具级 fitness 维度开启后，必须得到**不同的聚合 fitness**。"

这条断言现在**没有任何测试**。它也是 §8.8"先落工具级 fitness"的前提——不先验证传导，fitness 维度做了也可能白做。

### 9.2 §8.4 的对偶缺口：归因侧同样全局

§8.4 只写了作动侧（`GetActive` 全局单例），**归因侧同样全局**：`ChannelFeedbackRecorder.write` 用 `r.activeID()` 归因（`channel_feedback.go:324`），所有 `tool_call` 证据都记到**同一个** `strategyID`。

∴ 即便 per-agent 作动通过节点 Metadata 打通了，GA 在**证据侧**仍分不出"是哪个 agent 的哪个工具集贡献的"——两个 agent 用不同工具集，`tool_call` 证据却合在同一策略下。**A 和 B 都受制于此**（B 的轨迹投影同样需要 agent 维度的证据才能学到"哪条轨迹好"）。

对策：`activeID()` 需升级为 `(strategyID, agentID)` 双 key，或在 evidence payload 增加 `agent_id` lively 并让 aggregator 按 (策略, agent) scope 读取。文档 §7 之前未提此维度。

### 9.3 §8.7 对 B 的工作量估算偏低

§8.7 说方案 B"补 round 字段 2 行"即可重建轨迹——**低估了两件事**：

1. **成败信号不在事件里**：`EventToolCallCompleted` 的 payload（`chat_cognition.go:381`、`sub/executor.go:936`）只有 `agent_id/tool_name/tool_call_id`，**无 success/error/output**。执行失败时 `err` 只进 `result = fmt.Sprintf("error: %s", ...)` 塞回 messages，**没进事件**。而 §3.2 指望"轨迹 DAG 节点失败率作 fitness 维度"——**这个信号从事件日志取不到**，要先给 `EventToolCallCompleted` 补 `success/error` 字段。
2. **eventStore 接线是隐含前提**：`emitEvent` 在 `eventStore == nil` 时静默 no-op。"从事件日志投影"隐含一个**未声明前提**：eventStore 必须已接线。未接线则轨迹投影恒空。

∴ B 实际需：补 `success/error` 事件字段（不只 round）+ 确认/强制 eventStore 接线，才能支撑"节点失败率作 fitness"。

### 9.4 三条执行体的事件契约不同构（文档此前按同构处理）

文档 §8.6 第 2 条只说"补 agentloop 第三条执行体"——实际问题不是少接一条，是**三条执行体的事件 payload 形状本就不一致**：

- `agentloop/engine.go:437`（sdk 路径）：带 `tool/args/result/success`——**最全**。

- `sub/executor.go:936`、`chat_cognition.go:381`（peer 两条生产路径）：只有 `agent_id/tool_name/tool_call_id`——**无成败**，是最简。

- `internal/workflow/graph/node.go:118-211`（graph 执行器）：另一种 payload。

而 `ares_archive/extract.go` 的 `extractVerdict/extractFileChanges`（`:107/:226`）读了 `payload["output"]`（`:472`），该字段**只有 sdk/callback\_bridge 路径提供** → 这些提取器对 **peer 事件静默返回空**。

∴ B 的投影层**必须先统一三条执行体的事件契约**（至少 peer 两条与 sdk 对齐到含 results/success），否则按 agentloop 的形状投影会漏 peer 数据。这比"补一条"工程量大。

### 9.5 方案缺少验收信号

A 和 B 都没定义"**怎么知道它真的 работает**"。既然 fitness 传导这种核心链路能**悄悄坏掉一整轮而所有测试照绿**（§9.1），方案文档应自带一条端到端断言。

最小验收信号（无论 A/B）：

> "两个**仅** **`Params["tools"]`** **不同**的策略，开启工具通道后，必须产生**不同的 fitness**（归因到各自策略），且该差异可由 `RuntimeFitnessAggregator` 读到。"

这条同时覆盖 §9.1（传导）、§9.2（归因双 key）、§9.3（成败入事件）——它是 Y.1 是否有意义的最终判定。

### 9.6 次要：两个 store 对"活跃策略"的判定不一致

- `pg_strategy_store.go:104`：`ORDER BY created_at DESC`。

- `strategy_repository.go:62`：`ORDER BY version DESC`。

多活跃行时二者给出不同答案。正常路径下 `SetActive` 先 deactivate all，不会触发；但作为一致性欠账应记录，避免未来依赖时踩坑。

***

## 10. 本轮落地状态（2026-09-04，已编译 + 测试通过）

### 10.1 已完成

| 项                  | 内容                                                                                                                                                                                                                                                                 | 位置                                                                                                                    |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| 空白名单 guard         | 白名单与注册工具零交集时回退全量，避免把空工具表交给 LLM                                                                                                                                                                                                                                     | `agentfabric/chat_cognition.go`、`agents/sub/executor.go` + 回归测试 `TestToolWhitelistZeroIntersectionFallsBackToFullSet` |
| Clone 不继承 hash 缓存  | `Clone()` 是所有变异路径入口，继承 hash 会让子代命中父代 ScoreCache 条目、拿到父代分数 → 选择压力归零                                                                                                                                                                                                 | `mutation/types.go`                                                                                                   |
| EvidenceKey 整型不再丢失 | `numericParam` 覆盖 int/uint/float 全族；此前裸 `.(float64)` 断言会丢掉 `top_k`/`max_steps`/`memory_limit`，仅这些维度不同的策略会塌到同一 EvidenceKey                                                                                                                                          | `mutation/types.go` + 测试                                                                                              |
| **词表对齐（§8.6-3）**   | `extractToolNames` 的词表从裸动词 `{search,read,write,exec,...}` 换成 `registeredToolAliases`：keyword → **真实注册名**（`web_search`/`file_tools`/`calculator`/`json_tools`/…），去重且顺序确定。此前零交集导致每次 guided tool 变异都写出匹配不到任何工具的白名单                                                    | `ares_evolution/experience_hints.go` + 测试（含"别名目标必须是注册名"不变式测试）                                                         |
| **§9.5 端到端验收断言**   | 新增 `tool_dimension_transmission_test.go`，按四链断言"仅 `Params["tools"]` 不同 → fitness 不同"：①`StrategyHash` 分得开（否则命中兄弟 ScoreCache）②`ComputeEvidenceKey` 分得开（否则证据合并）③`Weights.ToolCall>0` 通道已武装 ④`Window` 真读到差异；配套 `TestToolDimension_UnarmedChannelIsInert` 说明默认 0 权重是刻意闸门 | `ares_evolution/tool_dimension_transmission_test.go`                                                                  |

§9.1 的核心结论由此被测试固定：**传导链现在是通的，但第 ③ 链（`Weights.ToolCall`）默认 0**——工具维度出厂即惰性，需运营显式给权重才进入选择。这是发布措辞必须写明的一条。

### 10.2 仍未闭环（按优先级）

1. `Payload["tools"] → params` 装配 + 节点 vs 全局策略优先级（§8.5），并补 `agentloop/engine.go` 第三条执行体。
2. 归因双 key `(strategyID, agentID)`（§9.2）——不做则 per-agent 作动即便打通，证据侧仍分不出是哪个 agent 的工具集。
3. `EvolutionGuardrails` 增加工具集 allowlist 上界（§8.6-5）。
4. 若选 B：先统一三条执行体事件契约、补 `EventToolCallCompleted` 的 `success/error`（§9.3、§9.4）。


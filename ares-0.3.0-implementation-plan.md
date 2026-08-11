# ARES 0.3.0 实施方案（根目录版 · 复用现有管道路线 v2）

> 依据：
> - `improve.md`（项目讨论纪要）
> - 《深入理解 AI Agent》[第 8 章 Agent 的持续进化](https://bojieli.github.io/ai-agent-book/book/chapter8/) / [第 10 章 多 Agent 协作](https://bojieli.github.io/ai-agent-book/book/chapter10/)
> - 项目源码实测（2026-08-10）
> 关联文档：`docs/zh/architecture/ares-0.3.0.md`（架构定义，已冻结）
> 状态：✅ 实施完成 — P0-1 ~ P0-5 全部完成（2026-08-10）
> 变更记录：v2 依据评审结论改为**复用现有管道**——`internal/evidence`（证据原语）+ `evolution/coordinator` + `evolution/deployment`（发布管道），不再并行新建第二套 Evidence / 发布机制。

---

## 〇、两章到底说了什么（提炼，非摘要）

### 第 8 章：Agent 的持续进化

| 原文核心观点 | 直接后果 |
|-------------|---------|
| **保存经历 ≠ 从经历中学习**；学习 = 评价→对照→归纳→验证 | 光写日志/存对话不产生进化，必须主动完成评价闭环 |
| **在线/离线分离**：在线循环只负责完成任务+记录证据；离线循环才聚合轨迹、诊断、生成候选、发布 | 运行时不自我修改，一切变更走候选管道 |
| **三层验证**：底层结果验证器（测试/DB 状态/工具返回）、中层过程验证器（规则/权限/动作序列）、上层质量验证器（Rubric） | 越靠下越用代码/环境真值，只有难以形式化的才交给 LLM |
| **Rubric 维度化评价**，**验证结果不压缩成标量** | 输出结构化诊断：维度 + 证据位置 + 置信度 |
| **候选生成有边界**：失败证据 + 必须保留的成功行为 + 此前被拒绝的修改记录（Self-Harness） | 不能把全部源码+日志塞给修改 Agent |
| **可证伪的变更契约**：失败证据、推断根因、归属组件、候选修改、预期修复、可能受损的既有行为、验证两者的用例 | Candidate 必须携带"影响预测" |
| **发布即软件流程**：稳定版 → 候选分支 → 最小补丁 → 静态检查/单测/安全扫描/失败轨迹重放/旧任务回归 → 灰度 → 新版本；`release_manifest.json` 记录失败簇/来源轨迹/根因/diff/预期修复/潜在回退/检查结果/候选版本/回滚版本 | 只有 verified → released 或 rejected 两个出口 |
| **进化控制范围外**：生成补丁的 Agent 不能修改稳定代码、验证器、审计日志、发布门槛 | 可信根必须隔离 |

### 第 10 章：多 Agent 协作

| 原文核心观点 | 直接后果 |
|-------------|---------|
| 两个设计维度：**共享上下文 vs 不共享上下文**（=线程 vs 进程） | 少量角色用共享上下文最简单；多角色并行/强隔离才用独立上下文 |
| 不共享时三大通信手段：**工具调用参数 / 共享文件系统 / 消息总线** | 0.3.0 选工具调用参数（Handoff 即参数化传递），不上消息总线 |
| **多阶段角色转换**（Sequential）：同一 runtime、同一执行，不同 system prompt/工具集 | "新 Agent" 只是角色切换，不是新实例 |
| **管理者模式控制上下文膨胀**：Manager 只装任务描述、计划、各 Agent 调用记录与返回结果、进度，不装全文 | leader 的 context 必须保持精简 |
| **失败模式一：并发冲突** → 乐观锁/worktree/隔离 | 共享产物区需要并发控制 |
| **失败模式二：错误级联放大**（传话游戏）→ **交叉验证**：独立视角重审；**确定性工具是断链器**（单测/编译器/DB 查询不受幻觉影响） | Reviewer 的价值=独立判断者；Validator 节点必须是 DAG 一等公民 |
| **失败模式三：过早终止**（偷懒式假完成/过早放弃/假成功）→ 解法殊途同归：**验证** | 执行必须接验证，不能"感觉不错" |
| **循环失控**：token 成本失控/理解债/认知投降 → 显式预算与终止条件、扎根真实观测的验证器 | 预算感知、步骤上限、验证器锚定真值 |

**两章交汇点**：第 10 章的多 Agent 协作是"执行层"，第 8 章的验证闭环是"学习层"——前者产生 Evidence，后者消费 Evidence 进化。这正是 ARES 0.3.0 的骨架。

---

## 一、项目现状实测（2026-08-10 更新）

### 1.1 四个新概念文件：存在但"三无"（无编译、无引用、无测试）

| 文件 | 内容 | 实测问题 |
|------|------|---------|
| `internal/agents/profile.go` | AgentProfile + ProfileRegistry + DefaultProfiles（planner/researcher/coder/reviewer）+ ApplyToContext | ❌ 缺 `context` import（L144/150/154）；`ErrProfileNotFound` 的 `Error()` 返回空字符串（ProfileID 为空且 Message 为空） |
| `internal/agents/handoff.go` | Handoff + ArtifactRef + builder（WithContext/WithArtifact/WithMetadata） | ✅ 逻辑完整可编译 |
| `internal/eval/evidence.go` | Evidence/EvidenceItem/DimensionScore/Verdict | ❌ L140 `e.Flag undefined`（Evidence 无 Flag 字段） |
| `internal/evolution/candidate.go` | Candidate 状态机 + CandidateVerifier（三道关）+ CandidateStore | ❌ `contains` 未定义（应为 `strings.Contains`）；`ProfileStore`/`AgentProfile` 未定义（L264/290） |

实测：`go build ./internal/eval/... ./internal/agents/... ./internal/evolution/...` 三个包全失败。

**评审新增发现的 bug（v1 未列，P0-1 顺手修）：**

| 位置 | 问题 |
|------|------|
| `candidate.go:120` `c.ID[:8]` | `CandidateStore.Submit` 生成 `"cand-1"`（6 字符）→ `String()` 切片越界 panic |
| `candidate.go:296` `profile.Metadata["skills"]` | `DefaultProfiles` 的 `Metadata` 是 nil map → 赋值 panic |
| `candidate.go:136-154` 状态机断链 | `CandidateVerifier.Verify()` 返回 `VerifyResult` 但**不调用 `c.Verify()`**，候选永远停在 `candidate` 状态，`promoteToStable` 永远拒绝。需在管线中显式衔接 |
| `profile.go:167` `ErrProfileNotFound` | `ProfileError{ProfileID:""}` 时 `Error()` 返回空字符串，错误信息丢失 |

### 1.2 可复用设施（v2 补全：v1 漏掉了证据原语与发布管道三块）

| 现有代码 | 能力 | 在 0.3.0 中的角色 |
|---------|------|------------------|
| `internal/evidence/`（`Evidence` + `Store` + `Collector` + `MemoryStore` + `postgres_store`） | **全系统统一证据原语**：`{ID, Source, Kind, Payload json.RawMessage, Metadata, Timestamp, TTL}`；`Store{Append, Query}`；`Collector.Emit(ctx, kind, payload)`；`Filter{Source, Kind, Since, Until, Limit}` | **P0-4 的持久化层**：`eval.Evidence`（领域诊断类型）→ bridge → `evidence.NewEvidence(source, KindDimensionEval, payload)` → `Collector.Emit`。不新建第二套证据存储 |
| `internal/agents/leader/`（Process：parse→plan→dispatch→aggregate） | 天然的 Manager 模式（第 10 章实验 10-3 结构） | 角色切换 + Handoff 的宿主 |
| `internal/agents/sub/`（TaskExecutor/MessageHandler/ToolBinder） | 子 Agent 执行器 | 接收 Handoff 的下游执行者 |
| `internal/workflow/`（NodeSpec/EdgeSpec/NodeRouter/Runner） | 动态 DAG | 编排载体：Node=Agent/Validator，Edge=依赖 |
| `internal/ares_eval/`（dimension_judge 标量、llm_judge、concurrent_runner） | 评估 | DimensionJudge→Evidence 升级对象（bridge 所在） |
| `internal/ares_arena/`（scenario/scorer/regression/survival） | 场景评分、回归、生存测试 | Candidate 验证的回归案例来源 |
| `internal/ares_events/`（Event/EventType/EventStore） | 事件流（Trace 载体） | Evidence 的输入，审计日志 |
| `internal/ares_experience/`（DistillationService/FeedbackService/RankingService）+ `internal/ares_evolution/experience/`（normalizer/aggregator/store） | **Experience Distillation 已有底座**：TaskResult → Experience 提取/排名/反馈 | P0-5 的离线归位环节：执行产物 → Experience，失败簇从 Experience + Evidence 双源取（对应 improve.md 四个 P0 Feature 之一） |
| **`internal/evolution/coordinator/`**（`PatchSource` × 7、`PatchProposal`、`Decision{Apply/Reject/Delay/Drop}`、`Submit/Evaluate/DecisionHistory/PatchHistory/ApplyEmergency`） | 中心化补丁决策器："不知道补丁来自哪，只决定 Apply/Reject/Delay" | **P0-5 的发布决策口**：Candidate 验证通过后转 `PatchProposal` 提交，decide 规则复用（priority ≥ AutoApplyThreshold → Apply） |
| **`internal/evolution/patch/`**（`RuntimePatch{ID,Type,Target,Value,Reason,Source,Rollback}`、`Registry.Register(target, Executor)`、`Executor.Apply` 返回回滚补丁） | 通用最小补丁单元 + 可回滚执行器注册表 | **P0-5 的变更载体**：Candidate → `RuntimePatch`（新增 `PatchChangeInstruction` 类型 + `ProfileExecutor` 注册），Rollback 天然支持回滚 |
| **`internal/evolution/deployment/`**（`DeploymentPipeline`：staging apply → evaluate → live apply / 自动回滚，`DeploymentConfig{Enabled, ShadowSampleSize, PromotionThreshold, RollbackThreshold}`） | canary 灰度发布 + 自动回滚 | **P0-5 的发布执行**：对应第 8 章"灰度→新版本→回滚" |
| `internal/evolution/`（genome/mutation/llm_adapter、coordinator、diff） | GA 进化 + 差异化器 | GA 降级为可选；`diff/` 可复用为 Candidate diff 生成 |
| `internal/ares_evolution/promotion/`（`DefaultPromoter`：strategy 生命周期 promote/demote） | 策略级发布 | 0.3.0 不接入（GA 域），但概念与 Candidate 发布一致，可作为参考 |
| `internal/agentloop/engine.go` | 工具循环引擎 | Execution Core 底座 |

---

## 二、实施步骤（P0-1 → P0-5，复用路线）

### P0-1 修编译（依赖方向先定死）

1. **`internal/agents/profile.go`**：补 `import "context"`；`ErrProfileNotFound` 的 `Error()` 补默认消息（ProfileID 为空时返回 `"profile not found"`）。
2. **`internal/eval/evidence.go`**：给 `Evidence` 增加 `Flag string` 字段（与 `DimensionScore.Flag` 语义一致，整体失败原因摘要）。`Evidence` 是全新类型，无既有调用方，改动零影响。
3. **`internal/evolution/candidate.go`**（依赖方向是关键决策）：
   - **决策（v2 不变）**：`evolution` import `agents`（已确认 `agents` 不依赖 `evolution`，无环），复用 `agents.AgentProfile`；`ProfileStore` 在 `evolution` 包新建（稳定指令读取属进化域）。
   - import 补 `strings`、`agents`；L202 `contains(text,p)` → `strings.Contains(text,p)`；L264/290 类型改 `*agents.AgentProfile`。
   - 新建 `internal/evolution/profile_store.go`：`ProfileStore{Get(role), Update(p), GetStable(role), SetStable(role,p)}`。**v2 定位调整**：`GetStable/SetStable` 只读稳定区与写候选区，真正"发布到稳定"动作由 P0-5 的 coordinator/deployment 完成。
4. **顺手修三个评审 bug**：`c.ID[:8]` 越界（`String()` 加长度保护）；`applyDiff` 中 `Metadata` nil map 初始化；状态机断链在 P0-5 显式衔接。

**验收**：三包 `go build` 通过。

---

### P0-2 补单测（孤儿代码变可验证代码）

沿用项目 testify + table-driven 规范（参照 `internal/evolution/benchmark_test.go`）：

| 文件 | 测试点 |
|------|--------|
| `internal/agents/profile_test.go` | Register/Get/List/Has；ApplyToContext 切换后 GetFromContext 正确；未注册返回 ErrProfileNotFound（且 Error() 非空） |
| `internal/agents/handoff_test.go` | builder 链；HasArtifact/ArtifactOfType；Size 统计 |
| `internal/eval/evidence_test.go` | AddDimension 阈值（2/3）；HasFailure/FailureFlags；Verdict String；Flag 字段 |
| `internal/evolution/candidate_test.go` | 状态机流转；Verifier 三道关；`String()` 对短 ID 不 panic；`applyDiff` 对 nil Metadata 不 panic；promote 前置校验（非 verified 拒绝/target 不存在拒绝） |

**验收**：四包 `go test` 全绿。

---

### P0-3 接线 leader（第 10 章"多阶段角色转换"落地）✅ 已完成

**目标**：Process 从固定四步升级为"角色切换 + 显式 Handoff"，实现第 10 章实验 10-3 的管理者模式 + 上下文隔离。

改动点（全部在 `internal/agents/`）：

1. `leaderAgent` 增加 `profileRegistry *agents.ProfileRegistry`（`New` 注入，默认 `agents.DefaultProfiles()`；`WithProfileRegistry` option 可覆盖）。
2. **命名澄清（v2 新增）**：`internal/agents/leader/profile.go` 的 `profileParser` 解析的是**用户画像**（需求/偏好），与新的 `agents.AgentProfile`（**Agent 角色**）是两个概念——`Process` Step 1 保留用户画像解析，Step 3 dispatch 前按任务类型从 `profileRegistry` 选**角色**，`registry.ApplyToContext(ctx, role)` 切换 system prompt + 工具集。
3. Step 3 dispatch 前构造 `agents.Handoff{From, To, Task, Context, Artifacts}`（新文件 `internal/agents/leader/handoff.go`：`buildHandoff`/`selectRole`/`taskAgentTypes`/`summarizeTasks`）：
   - `Context` 只带结构化数据（`task_count`、`agent_types`）——对应第 10 章"工具调用参数传递结构化数据"；
   - `Artifacts` 用 `ArtifactRef{Path, Type, Summary}` 引用而非内联全文——对应第 10 章"共享文件系统/结构化交付物"（0.3.0 用内存/临时路径，不上真实共享卷）。
   - **nil registry 防护**：`profileRegistry == nil` 或任务为空时返回原 ctx + nil Handoff，绝不 panic。
   - **回退不注入（评审修复）**：`selectRole` 返回 `(role, matched)`；当没有任务类型匹配已注册 profile 时（`matched=false`），`buildHandoff` 返回原 ctx + nil Handoff，**不应用 planner 回退 profile**——回退不是真实角色切换，避免 planner 指令泄漏进所有未接入角色切换的既有子 Agent prompt（生产任务类型 `agent_top`/`destination` 等均不匹配默认四角色）。
4. 下游 `internal/agents/sub/` 从 `agents.GetFromContext(ctx)` 取角色（`activeRoleInstructions`），将角色 Instructions 前置到渲染后的 prompt，**不继承上游完整对话历史**——仅当 ctx 中确有 profile 时前置（无 profile/空 Instructions 时输出不变）。
5. `internal/ares_events/` 新增 `EventHandoff` 事件类型（`handoff.transferred`）+ payload key（`handoff_from`/`handoff_to`/`handoff_artifact_count`/`handoff_context_keys`），供 Evidence 阶段追溯。

**第 10 章约束内化**：
- ✅ 管理者模式：leader 只装任务描述/计划/调用记录/进度，不装子 Agent 全文（现有 `dispatcher`/`aggregator` 已符合，Handoff 强化之）。
- ✅ 失败模式二防御：DAG 中 Reviewer/Validator 作为独立视角节点（P0-4 承载）。
- ✅ 失败模式三防御：执行必须接验证节点，validator 产出 Evidence 而非"感觉不错"。
- ✅ 循环失控防御：沿用 `MaxSteps`/步骤上限，预算感知。

**验收结果（已完成）**：`internal/agents/leader/handoff_test.go`（242 行）新增 7 个测试：researcher→coder→reviewer 三阶段角色切换（各阶段 Profile 不同）、Handoff 只含结构化上下文（不含原始消息体）、EventHandoff 事件落库（from/to/artifact 数）、空任务/未匹配类型**不注入**（nil Handoff + ctx 不变 + 无 profile）、nil registry 不 panic、selectRole 边界（含 matched 标志）。评审后补 `internal/agents/sub/executor_role_test.go`（47 行）：`activeRoleInstructions` 无 profile/有 profile/空 Instructions 三场景，覆盖"仅真实角色切换才前置指令"的回归。`make check` 全绿（vet/staticcheck/golangci-lint PASSED，leader 63.5%、sub 43.2%、ares_events 48.7%）。

---

### P0-4 三层验证器 + Evidence bridge（复用 `internal/evidence`，v2 关键变更）✅ 已完成

**目标**：标量评分升级为结构化诊断，**持久化走全系统通用证据原语 `internal/evidence`**，不新建第二套证据存储。

改动点（`internal/ares_eval/` + `internal/eval/` + `internal/evidence/`）：

1. **`internal/evidence` 增加新 Kind**：`KindDimensionEval EvidenceKind = "dimension_eval"`。`eval.Evidence`（领域诊断类型，含维度/证据链/置信度）作为 Payload 序列化写入通用 `Evidence`——两类型职责分离：`eval.Evidence` = 内存中的结构化诊断，`internal/evidence.Evidence` = 可查询可审计的持久化原语。
2. 新增 `internal/ares_eval/evidence_bridge.go`（150 行）：`DimensionJudgeResponse`（标量 4 维）→ `eval.Evidence`：
   - Verdict：reason 含"无法判断/uncertain"→Uncertain（中英文关键词）；四维均过阈值→Pass；任一失败→Fail；
   - Dimensions：每维映射 `DimensionScore`，挂 `EvidenceItem{Type:"llm_judge", Status, Detail}`，score 经 `clampScore` 防御钳制到 [0,max]；
   - Source="dimension_judge"；Confidence = 四维合计/10。
   - **持久化**：`Emit` 显式检查 nil store（返回 `ErrNilEvidenceStore`，规避 `Collector` 的静默 no-op）→ `evidence.NewCollector(store, "dimension_judge").EmitWithMeta(ctx, KindDimensionEval, ev, "task_id", taskID, "role", role)`。
3. 新增底层+中层验证器（第 8 章三层结构，**越靠下越用真值**）：
   - `internal/eval/result_verifier.go`（113 行）：底层——`ResultVerifier.Verify(taskID, role, []ResultCheck)`，确定性判定"是否真的办成"：全部 passed→Pass、任一 failed→Fail、missing/skipped→Uncertain、空→Uncertain；**维度 Pass 显式设置**（非 2/3 阈值），因为底层必须全过才算成功；
   - `internal/eval/process_verifier.go`（89 行）：中层——`ProcessVerifier.Verify(taskID, role, []ProcessCheck)`，查规则/权限/动作序列："是否以允许的方式办成"，任一违规即 Fail（规则是硬约束，同样显式设置维度 Pass）；
   - 上层维持 `dimension_judge`（LLM Rubric，"是否办得合适"）经 bridge 落库。
4. 现有调用方（`concurrent_runner`/`llm_judge`/`comparison`）继续用标量，bridge 提供兼容——两类并存，`eval.Evidence` 成为进化管线的输入格式，`internal/evidence` 成为其存储。

**验收结果（已完成）**：
- `internal/ares_eval/evidence_bridge_test.go`（172 行）：pass/fail/uncertain 判定（含中英文关键词表驱动）、nil response、clampScore 边界、**Emit 后 `Query(Filter{Kind: KindDimensionEval})` 读回且 Payload 反序列化为 `eval.Evidence` 成功**、nil store/nil response 报错、llmJudgeItem/isUncertainReason 单测。
- `internal/eval/verifier_test.go`（137 行）：ResultVerifier 全过/失败/缺失/跳过/空/空状态 6 场景；ProcessVerifier 全合规/违规/空/EvidenceItem 4 场景。
- `make check` 全绿（vet/staticcheck/golangci-lint PASSED，ares_eval 77.2%、eval 97.8%）。
- 过程中修复两个设计一致性问题：① verifier 维度 Pass 改为显式设置（2/3 阈值会让"1 失败/2 项"误判通过，与"任一失败即失败"语义矛盾）；② bridge 显式检查 nil store（`Collector` 对 nil store 是静默 no-op，原 `collector==nil` 检查永不触发）。

---

### P0-5 Candidate 管线闭环（复用 coordinator + deployment，v2 关键变更）✅ 已完成

**目标**：失败 Evidence → Candidate → 三道关 Verify → **已有 coordinator 决策 + deployment 灰度发布**，稳定与候选物理隔离，发布即审计。

改动点（`internal/evolution/`）：

1. **生成侧（含 Experience 归位）**：新增 `internal/evolution/diagnoser.go`（98 行）：输入 `eval.Evidence`（VerdictFail + FailureFlags，从 `internal/evidence` Store `Query(Filter{Kind: KindDimensionEval, Source: "result_verifier"})` 取失败簇，按 metadata role 过滤）+ **失败 Experience**（`internal/ares_experience.DistillationService` 从 TaskResult 提取，`FeedbackService.RecordFailure` 标记的失败经验），输出 `Candidate{Kind: CandidateInstruction, TargetRole, Diff, Reason, EvidenceIDs}`。触发条件对齐架构验收：**同一 TargetRole 同类失败 Evidence ≥2 条**（`MinFailureClusterSize` 常量）才生成候选。首版**由开发者/人工确认生成**（`GenerateRequest{Diff, Reason}` 由人工提供），不做自动 LLM 生成。Experience 在此的角色（对应 improve.md 四个 P0 Feature 之三）：为 Diagnoser 提供"该角色反复失败的模式 + 可复用解法"，与 Evidence（本次失败的具体诊断）互为补充。
2. **验证侧**：`CandidateVerifier` 三道关补齐（对照第 8 章发布门禁）：
   - 关 1 `staticCheck`：已有（危险模式拒绝）✅；
   - 关 2 `replayFailureCases`：**注入 `evidence.Store`（`WithEvidenceStore`）真实验证失败证据存在且为 `KindDimensionEval`**——每条 `EvidenceIDs` 必须能在 store 中查到且 kind 正确（查询全部记录以区分"缺失"与"kind 错误"）；未注入 store 时降级为"EvidenceIDs 非空"断言（兼容无 store 调用方）。
   - 关 3 `checkRegression`：`NewCandidateVerifier(regressionCheck func(*Candidate) error)` / `WithRegressionCheck` 可注入回归检查器（`ares_arena` 保留案例集回放的挂载点），nil 时跳过该关。
     - **门3生产接线完成**：新增 `internal/evolution/candidate_regression.go`（`CandidateRegressionChecker`，用 `ares_arena.RegressionTester` + 注入 `Scorer` + 保留案例集，对 `stable.Instructions` vs `candidate.Diff` 做保留案例显著性对比；`Confident && NewAvg<OldAvg` 判定回归并拒绝）。`ares_arena` 新增 `NewRegressionTesterWithScorer`（回归统计不需 arena Service，供门3复用）。
   - 构造器：`NewCandidateVerifier`（兼容旧签名）+ `NewCandidateVerifierWithOptions(WithEvidenceStore, WithRegressionCheck)`（函数式选项）。
   - **修复状态机断链**：`Verify()` 现在推进状态机——全关通过调 `c.Verify()` 置 `StatusVerified`，任一关失败调 `c.Reject(reason)` 置 `StatusRejected` + `RejectionReason`；nil candidate 防护。
3. **发布侧（v2 复用现有管道）**：新增 `internal/evolution/candidate_pipeline.go`（208 行）+ `profile_executor.go`（134 行），Candidate 验证通过后**不再自建 promoteToStable 直达稳定区**（评审后**删除死代码** `CandidateStore.promoteToStable` 与 `applyDiff`，消除双入口；发布统一走 `CandidatePipeline.Release`），改为：
   - **变更载体**：`patch.RuntimePatch{ID, Type: PatchChangeInstruction(新增), Target: "profile:<role>", Value: 新 Instructions, Reason, Source: "candidate", Rollback: 旧 Instructions}`；`ProfileExecutor`（实现 `patch.Executor`）注册到 `patch.Registry`——`Apply` 改候选区并返回回滚补丁，`CanApply` 校验 target/type/value。
   - **决策口**：`coordinator.Submit(PatchProposal{Patch, Source: SourceCandidate(新增 PatchSource 常量 "candidate"), Reason, Priority: 8})` → `coordinator.Evaluate(ctx)`。非 GA source 走 priority 规则：`Priority >= AutoApplyThreshold(8) → DecisionApply`，否则 Delay/Drop/Reject。
   - **发布执行**：`DecisionApply` 后经 `deployment.DeploymentPipeline`（`deploymentAdapter` 适配 `coordinator.PatchDeployer` 签名差异）canary 发布，或直接 `registry.Apply` → 成功后 `ProfileStore.SetStable(role, newProfile)`；回滚由 `RuntimePatch.Rollback` + deployment 自动回滚承担。
   - **审计（对照 `release_manifest.json`）**：`coordinator.DecisionHistory()`（SourceCandidate 决策）+ `coordinator.PatchHistory()`（应用结果）+ `deployment.History()`（灰度记录）+ `ProfileStore` 稳定区，共同构成 manifest；拒绝的候选保留 `RejectionReason` 供下一轮查阅。
4. **候选空间有边界**（Self-Harness）：Diagnoser 输入 = 失败证据（`internal/evidence` 按 role 过滤）+ 此前被拒绝的候选（`CandidateStore.ListByStatus(StatusRejected)` 可查）。

**第 8 章安全边界（不可逾越）**：
- ❌ Evaluator/Verifier/Safety Gate/Audit Log/Release Gate 不在 Candidate 可修改范围（coordinator 的 DecisionPolicy / deployment 的 Threshold 均不可被候选写入）；
- ❌ 自动创造新 Agent；
- ✅ Candidate 与 Stable 物理隔离（`ProfileStore` 分区），失败可回滚（`RuntimePatch.Rollback` + deployment）；
- ✅ 只有 verified → released 或 rejected 两个出口（`coordinator` 只发 Apply/Reject/Delay/Drop）。

**验收结果（已完成）**：`internal/evolution/candidate_pipeline_test.go`（282 行）新增 14 个测试场景：
- **端到端闭环**：2 条失败 Evidence → Diagnoser 生成 Candidate（EvidenceIDs=2）→ Verify 通过（Status=Verified）→ Release → coordinator Apply → `GetStable("coder").Instructions` 读到新指令 → Status=Promoted；
- **审计**：DecisionHistory 记录 `SourceCandidate` + `DecisionApply`，PatchHistory 记录应用且无错误；
- **门禁**：危险 diff 被 staticCheck 拒绝（Status=Rejected + RejectionReason）；注入回归检查器触发 reject；
- **边界**：未验证候选 Release 拒绝（ErrCandidateNotVerified）、未知 ID（ErrCandidateNotFound）、未装配 pipeline 报错、RuntimePatch 携带回滚（Rollback.Value=旧指令）、Diagnoser 失败簇 <2 / 其他 role 不生成、nil store / 空 role / 空 diff 校验、deploymentAdapter nil/disabled 边界。
- `make check` 全绿（vet/staticcheck/golangci-lint PASSED，evolution 83.8%）。
- 注：`make check` 首次运行遇 `TestRuntimeResurrection_ConcurrentKillAndResurrect`（`ares_integration`，既有并发时序测试）偶发失败，与本次改动无关（未触碰 `ares_runtime`/`ares_integration`），单独连跑 3 次均通过。

**评审后修复（代码已改，`make check` 复验全绿）**：
- ① 死代码/双入口：删除 `CandidateStore.promoteToStable` + `applyDiff`（被 `CandidatePipeline.Release` 取代），消除"候选直达稳定区"与"coordinator/deployment 发布"两条并行路径；同步删除 `candidate_test.go` 对应 6+4 个测试。
- ② 并发安全：`CandidateStore` 增加 `sync.RWMutex`（`Submit`/`Get`/`ListByStatus`/`ListByRole` 全部加锁，返回切片拷贝），新增 `TestCandidateStore_ConcurrentAccess` 并发测试（32 协程 × 50 提交，校验 ID 唯一），`go test -race` 通过。
- ③ 生产接线：
  - 门2 `replayFailureCases` 接入 `evidence.Store`（`WithEvidenceStore`），真实验证失败证据存在与 kind 正确，新增 `TestCandidateVerifier_ReplayGate_WithStore`（存在/缺失/错误 kind/查询失败 4 场景）；
  - `LLMJudgeEvaluator` 新增 `WithEvidenceStore`/`WithRole` option，`evaluateWithDimensions` 通过 `DimensionJudgeBridge` 在生产路径落 `KindDimensionEval` Evidence（best-effort，失败仅记日志不阻断评分），新增 `dimension_bridge_wiring_test.go`（落库可查/无 store 兼容路径/emit 失败不阻断/payload 可反序列化 4 场景）——生产真正产出结构化失败证据，`Diagnoser` 不再只依赖测试种子数据；
  - 门3 `checkRegression` 明确为 `ares_arena` 挂载点（保留可注入接口），补 `WithRegressionCheck` option，不改其设计。
- 门3已从占位升级为生产可用：`CandidateRegressionChecker` 通过 `ares_arena.NewRegressionTesterWithScorer` 复用回归统计（`RegressionTester` 实际运行只用 `Scorer`，无需 arena `Service`），以注入的 scorer + 保留案例集对比 `stable.Instructions` vs `candidate.Diff`；`Confident && NewAvg<OldAvg` 判定回归并拒绝。`ares_arena` 测试覆盖 scorer-only 构造与运行（新增 `TestNewRegressionTesterWithScorer`）。
  - **门3真实 LLM scorer 接入**：新增 `internal/ares_evolution/service/llm_arena_scorer.go`（`LLMArenaScorer`，实现 `ares_arena.Scorer`），用 `LLMClient`（可注入 `llm.Client` 或 mock）分两步调 LLM：① 以 `{instructions}` 为 agent 行为、`{case}` 为任务让 LLM 产出输出；② 让 LLM 对输出按 0-1 评分并解析（clamp 到 [0,1]，含 JSON/文本/裸数字解析）。新增 `llm_arena_scorer_test.go`（9 个单元场景 + 1 个回归比较集成测试，验证 `LLMArenaScorer` 驱动 `RegressionTester` 正确判定旧好/新差回归）。`make check`/`go vet`/`staticcheck`/`golangci-lint`/`go test -race` 全绿。
  - **LLM 密钥配置（不进 git）**：真实 `api_key` 写入 `configs/ares.local.yaml`（`.gitignore` 第 22 行忽略，已验证 `git check-ignore` 生效），正式 `configs/ares.yaml` 仅保留注释模板（sensenova `https://token.sensenova.cn/v1`），避免密钥入库。`llm.Client` 用 OpenAI-compatible `/chat/completions`（`base_url` 为根路径，客户端拼接）。
  - **真实 API 冒烟验证（已通过）**：`examples/15-llm-arena-scorer/main.go`（`go run ./examples/15-llm-arena-scorer`）真实调用 sensenova `deepseek-v4-flash`。**关键修正**：API 要求精确 model ID `deepseek-v4-flash`（连字符），非显示名 `deepseek v4 flash`（后者 404 `model is not found`）。实测：好指令评分 `0.850`、坏指令评分 `0.000`，`LLMArenaScorer` 正确区分质量分，支撑门3回归判定。冒烟示例需真实 API、可能产生费用，非 CI 一部分。
  - **顶层编排器**：新增 `internal/evolution/gate3_orchestrator.go`——`BuildRegressionGate3(profileStore, client evosvc.LLMClient, testCases, opts...)`（纯装配：LLMClient → LLMArenaScorer → CandidateRegressionChecker，返回 `func(c *Candidate) error` 供 `CandidateVerifier.WithRegressionCheck` 注入）与 `LoadRegressionGate3(profileStore, configPath, testCases, opts...)`（从 `configs/ares.local.yaml` 加载 llm.Client 再装配）。编排器单测（`gate3_orchestrator_test.go`）用 mock LLMClient 覆盖拒绝回归/通过/无 client/无 profileStore/缺失配置 5 场景，`make check` 全绿。
  - **真实 LLM 回归比较示例 + 日志**：`examples/16-llm-regression-demo/main.go` + `README.md`，用 `LLMArenaScorer` + `ares_arena.RegressionTester` 对旧/新指令做保留案例回归比较，完整日志自动写入 `examples/16-llm-regression-demo/logs/run-<ts>.log`（已保存多份真实运行日志）。
  - **provider 演进（解决 rpm 限制 → 最优 provider）**：`LoadRegressionGate3` 改用 `client.IsEnabled()` 校验凭据（ollama 无需 key，openai/openrouter/anthropic 才要求），兼容多 provider。
    - **sensenova**（`deepseek-v4-flash`）：单次调用正确（好 0.850 / 坏 0.000），但 **rpm 极低**（`429 rpm exhausted`）无法支撑多 runs。
    - **本地 Ollama**（无配额）：`gemma4:e2b`（7.2GB）执行弱——好指令 `[0 0 0 0 1]`，`Confident=false` 无法判定；`gemma4:e4b`（9.6GB）`old avg=0.16`、`new avg=0.03`，`Confident=true` p=0.0051 → REGRESSION，端到端可用但评分偏低。
    - **agnes-ai**（`agnes-2.5-flash`，`https://apihub.agnes-ai.com/v1`）：**无 rpm 限制**，完整跑 `BaselineRuns=5, CompareRuns=5` × 3 案例（约 60 次调用），好策略 `[0.5 0.85 0.2 0.5 0.2]` avg 0.45 vs 坏策略 `[0.3 0.2 0 0.2 0]` avg 0.14，**`Confident=true` p=0.0297 → REGRESSION detected**。执行可靠 + 无配额，推荐作门3回归 provider（当前配置）。
  - **结论：模型能力 + 配额决定门3可用性**。`LLMArenaScorer` 端到端正确；弱模型（gemma4:e2b）因任务执行失败无法区分新旧；高配额 + 可靠执行的 `agnes-2.5-flash` 产出统计显著的干净回归判定。
  - **评分 prompt 优化尝试（负面结论，已回退）**：尝试把评分 prompt 改为**锚点 rubric**（0.9-1.0 完全正确 / 0.1-0.4 严重错误），期望拉大正确/错误分差。实测 agnes 反而**降低区分度稳定性**：锚点版 `old avg=0.26 [0.3 0 0.5 0 0.5]`、`p=0.393`（不显著）；开放式"rate how well"版 `old avg=0.45 [0.5 0.85 0.2 0.5 0.2]`、`p=0.0297`（显著）。**原因**：更严格的 rubric 让评分 LLM 对同一输出在样本间波动更大，放大方差、削弱 t 检验。故保留开放式评分 prompt（已在注释中记录该结论）。另注意 agnes 免费档也有 API 速率限制（`429 ... reached the API rate limit for free users`），连续多次完整回归（每次约 60 次调用）会触发。
  - **完整门3端到端（真实 agnes 通过）**：新增 `examples/17-gate3-e2e-demo/main.go` + `README.md`，串起 `LoadRegressionGate3`（从 `configs/ares.local.yaml` 加载 agnes）→ `CandidateRegressionChecker` → `CandidateVerifier.WithRegressionCheck` → `Verify()` 全链路，日志自动写入 `examples/17-gate3-e2e-demo/logs/run-<ts>.log`。实测（agnes-2.5-flash，2 runs × 2 cases）：**坏候选**（a+b+1）→ `regression: avg dropped 1.000 -> 0.000 (win rate 0.00, p=0.0000)` → **`rejected`** ✅；**好候选**（加数字）→ `verified` ✅。端到端门3回归检查对真实模型正确拒绝回归候选、放行保留行为候选。
  - **LLM 请求批量合并（对 LLM 的请求合并为大的，解决限流）**：`ares_arena` 新增 `BatchScorer` 接口（`ScoreBatch(ctx, strategy, count, testCases)`），`RegressionTester.runStrategy` 检测 scorer 实现该接口则**一次批量调用算完所有 runs**（否则回退逐个并发 Score，向后兼容）；`LLMArenaScorer` 实现 `ScoreBatch`——一次批量执行 + 一次批量评分共 2 次 LLM 调用替代 `2×runs` 次。单测覆盖：`ScoreBatch` 成功/0 count/坏 strategy/输出不足/评分不足、`RegressionTester` 批量路径（2 次批量调用）、非批量 scorer 回退（逐个调用）。**批量实测发现**：① 保留案例必须**带具体数值**（抽象案例让 agnes 拒绝/自由文本，破坏"一行一结果"）；② 坏策略用**无害但错误**指令（"always zero"触发安全拒绝，改用 "a+b+1"）。批量后端到端 ~8s 跑完（每次策略仅 2 次批量调用），门3正确拒绝回归候选。
  - **Release 发布门禁闭环（门3接入 candidate_pipeline）**：`CandidatePipeline` 新增可注入的**发布前门3回归检查**（`CandidatePipelineOption.WithReleaseRegressionCheck`，`NewCandidatePipelineWithOptions` 装配；nil 则跳过，向后兼容）。`Release` 在**任何 patch 构建/应用之前**（`StatusVerified` 校验后）运行门3检查，失败则候选 `Reject("release regression gate: ...")` 且不触碰 runtime/stable。单测：`TestCandidatePipeline_ReleaseRegressionGate_{Rejects,Passes,Skipped}`（拒绝回归/放行/未注入跳过，stable 不变）。新增 `examples/18-release-closed-loop`（完整闭环：Diagnoser→Verify→Release→发布门3→SetStable，日志写入 `examples/18-release-closed-loop/logs/run-<ts>.log`），真实 agnes 批量实测三个场景全过：① 坏候选 Verify 拒绝（avg 1.0→0.0，p=0）；② 手动 verified 坏候选 **Release 门禁拒绝**（`released=false status=rejected reason="release regression gate: ..."`）；③ 好候选 Verify+Release 成功 `promoted`。~8s 跑完。

---

## 三、不做清单（与 improve.md / ares-0.3.0.md 一致）

- ❌ 复杂 Agent Registry（map 够用）
- ❌ Message Bus / A2A 协议（第 10 章：消息总线适合异步并行多 Agent，0.3.0 角色少、串行为主，不需要）
- ❌ 共享文件系统/共享卷（第 10 章第三层，0.3.0 用 Handoff 引用代替，VFS 推迟 0.4.0）
- ❌ GA Evolution（genome/mutation 降级为可选，不参与主线）
- ❌ Agent 自修改 Runtime / Verifier / Safety Gate / Audit Log / Release Gate
- ❌ 自动创造新 Agent
- ❌ 在线自我修改（第 8 章：在线只记录，离线才进化）
- ❌ 第二套证据存储 / 第二套发布管道（v2：统一走 `internal/evidence` + `coordinator` + `deployment`）

---

## 四、两章概念 → 项目代码映射总表（v2 更新）

| 章节概念 | 项目落点 |
|---------|---------|
| 第 10 章 多阶段角色转换 | `agents.ProfileRegistry` + `leader.Process` 角色切换（用户画像 `profileParser` 与 Agent 角色 `AgentProfile` 分离） |
| 第 10 章 结构化交付物（工具参数传递） | `agents.Handoff{Context, Artifacts}` |
| 第 10 章 管理者模式上下文精简 | leader 只持 Handoff 摘要，不持子 Agent 全文 |
| 第 10 章 失败模式二（错误级联）→ 交叉验证/断链器 | DAG Validator 节点 + `result_verifier` 确定性验证 |
| 第 10 章 失败模式三（过早终止）→ 验证 | Evidence 作为执行必经产物 |
| 第 8 章 三层验证 | `result_verifier`（底层真值）/`process_verifier`（中层规则）/`dimension_judge`（上层 Rubric） |
| 第 8 章 维度化评价不压缩标量 | `eval.Evidence`（维度+证据链+置信度），**持久化经 `internal/evidence.KindDimensionEval`** |
| 第 8 章 在线/离线分离 | 在线：Execution→Trace→Evidence；离线：Experience（`ares_experience`）→Diagnosis→Candidate→Verify→Release |
| 第 8 章 经历→经验（improve.md Feature 3） | `internal/ares_experience`（Distillation/Feedback/Ranking）作为 P0-5 生成侧输入，TaskResult → Experience → 失败簇 |
| 第 8 章 可证伪变更契约 + release_manifest | `Candidate{Reason, EvidenceIDs, Diff}` + `coordinator.DecisionHistory`/`deployment.History` 审计 |
| 第 8 章 发布门禁（回归/重放/回滚） | `CandidateVerifier` 三道关 + `coordinator` 决策 + `deployment` canary + `RuntimePatch.Rollback` |
| 第 8 章 进化控制范围外 | Candidate 不可写 Evaluator/Verifier/Audit Log/Release Gate |

---

## 五、执行顺序与验收总表

| 步骤 | 内容 | 验收 | 依赖 |
|------|------|------|------|
| P0-1 | 修编译 + 定依赖方向 + 三个 bug | 三包 build 通过 | — |
| P0-2 | 补四包单测 | go test 全绿 | P0-1 |
| P0-3 | leader 接线 Handoff/Profile | 角色切换测试通过，既有测试不回归 | P0-1 |
| P0-4 | 三层验证器 + Evidence bridge（落 `internal/evidence`） | bridge 单测通过，Query 读回，标量兼容 | P0-1（与 P0-3 并行） |
| P0-5 | Candidate 管线闭环（coordinator + deployment 发布） | 端到端 pipeline 测试通过，审计可查 | P0-4 |

> 进度：P0-1 ✅ / P0-2 ✅ / P0-3 ✅ / P0-4 ✅ / P0-5 ✅（2026-08-10）——0.3.0 全部 P0 完成，每步独立可合入。

---

**维护者**：GoAgent Core Team
**版本**：0.3.0（方案 v2）
**状态**：✅ 实施完成 — P0-1 ~ P0-5 全部完成（2026-08-10），代码已落地并通过 `make check` 质量门

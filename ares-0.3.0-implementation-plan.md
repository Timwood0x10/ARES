# ARES 0.3.0 实施方案（根目录版 · 复用现有管道路线 v2）

> 依据：
> - `improve.md`（项目讨论纪要）
> - 《深入理解 AI Agent》[第 8 章 Agent 的持续进化](https://bojieli.github.io/ai-agent-book/book/chapter8/) / [第 10 章 多 Agent 协作](https://bojieli.github.io/ai-agent-book/book/chapter10/)
> - 项目源码实测（2026-08-10）
> 关联文档：`docs/zh/architecture/ares-0.3.0.md`（架构定义，已冻结）
> 状态：📋 方案 v2 评审中，代码未修改
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

### P0-3 接线 leader（第 10 章"多阶段角色转换"落地）

**目标**：Process 从固定四步升级为"角色切换 + 显式 Handoff"，实现第 10 章实验 10-3 的管理者模式 + 上下文隔离。

改动点（全部在 `internal/agents/`）：

1. `leaderAgent` 增加 `profileRegistry *agents.ProfileRegistry`（`New` 注入，默认 `agents.DefaultProfiles()`）。
2. **命名澄清（v2 新增）**：`internal/agents/leader/profile.go` 的 `profileParser` 解析的是**用户画像**（需求/偏好），与新的 `agents.AgentProfile`（**Agent 角色**）是两个概念——`Process` Step 1 保留用户画像解析，Step 3 dispatch 前按任务类型从 `profileRegistry` 选**角色**，`registry.ApplyToContext(ctx, role)` 切换 system prompt + 工具集。
3. Step 3 dispatch 前构造 `agents.Handoff{From, To, Task, Context, Artifacts}`：
   - `Context` 只带结构化数据（任务要求、约束、上游摘要）——对应第 10 章"工具调用参数传递结构化数据"；
   - `Artifacts` 用 `ArtifactRef{Path, Type, Summary}` 引用而非内联全文——对应第 10 章"共享文件系统/结构化交付物"（0.3.0 用内存/临时路径，不上真实共享卷）。
4. 下游 `internal/agents/sub/` 从 `agents.GetFromContext(ctx)` 取角色，用 Handoff 内容初始化，**不继承上游完整对话历史**。
5. `internal/ares_events/` 新增 `EventHandoff` 事件类型，payload 记 from/to/artifact 数，供 Evidence 阶段追溯。

**第 10 章约束内化**：
- ✅ 管理者模式：leader 只装任务描述/计划/调用记录/进度，不装子 Agent 全文（现有 `dispatcher`/`aggregator` 已符合，Handoff 强化之）。
- ✅ 失败模式二防御：DAG 中 Reviewer/Validator 作为独立视角节点（P0-4 承载）。
- ✅ 失败模式三防御：执行必须接验证节点，validator 产出 Evidence 而非"感觉不错"。
- ✅ 循环失控防御：沿用 `MaxSteps`/步骤上限，预算感知。

**验收**：新增"researcher→coder→reviewer"角色切换测试，断言各阶段 Profile 不同且 Handoff.Context 不含上游原始消息体；`internal/agents/leader/` 既有测试不回归。

---

### P0-4 三层验证器 + Evidence bridge（复用 `internal/evidence`，v2 关键变更）

**目标**：标量评分升级为结构化诊断，**持久化走全系统通用证据原语 `internal/evidence`**，不新建第二套证据存储。

改动点（`internal/ares_eval/` + `internal/eval/` + `internal/evidence/`）：

1. **`internal/evidence` 增加新 Kind**：`KindDimensionEval EvidenceKind = "dimension_eval"`。`eval.Evidence`（领域诊断类型，含维度/证据链/置信度）作为 Payload 序列化写入通用 `Evidence`——两类型职责分离：`eval.Evidence` = 内存中的结构化诊断，`internal/evidence.Evidence` = 可查询可审计的持久化原语。
2. 新增 `internal/ares_eval/evidence_bridge.go`：`DimensionJudgeResponse`（标量 4 维）→ `eval.Evidence`：
   - Verdict：四维均过阈值→Pass；任一失败→Fail；reason 含"无法判断"→Uncertain；
   - Dimensions：每维映射 `DimensionScore`，挂 `EvidenceItem{Type:"llm_judge", Status, Detail}`；
   - Source="dimension_judge"。
   - **持久化**：`evidence.NewEvidence("dimension_judge", evidence.KindDimensionEval, evalEvidence, evidence.WithMetadata("task_id", taskID))` → `Collector.Emit`。
3. 新增底层+中层验证器（第 8 章三层结构，**越靠下越用真值**）：
   - `internal/eval/result_verifier.go`：底层——读测试结果/工具返回/环境状态，"是否真的办成"（确定性，不依赖 LLM）；
   - `internal/eval/process_verifier.go`：中层——查规则/权限/动作序列（依赖 `ares_events` 轨迹），"是否以允许的方式办成"；
   - 上层维持 `dimension_judge`（LLM Rubric，"是否办得合适"）。
   - 三层产物统一经 bridge 落 `internal/evidence`（Source 区分 `result_verifier`/`process_verifier`/`rubric_judge`）。
4. 现有调用方（`concurrent_runner`/`llm_judge`/`comparison`）继续用标量，bridge 提供兼容——两类并存，`eval.Evidence` 成为进化管线的输入格式，`internal/evidence` 成为其存储。

**验收**：bridge 单测通过（pass/fail/uncertain 判定正确 + 写入 `evidence.Store` 后可 `Query(Filter{Kind: KindDimensionEval})` 读回）；`internal/ares_eval/` 测试全绿。

---

### P0-5 Candidate 管线闭环（复用 coordinator + deployment，v2 关键变更）

**目标**：失败 Evidence → Candidate → 三道关 Verify → **已有 coordinator 决策 + deployment 灰度发布**，稳定与候选物理隔离，发布即审计。

改动点（`internal/evolution/`）：

1. **生成侧（含 Experience 归位）**：新增 `internal/evolution/diagnoser.go`：输入 `eval.Evidence`（VerdictFail + FailureFlags，从 `internal/evidence` Store `Query(Filter{Kind: KindDimensionEval})` 取失败簇）+ **失败 Experience**（`internal/ares_experience.DistillationService` 从 TaskResult 提取，`FeedbackService.RecordFailure` 标记的失败经验），输出 `Candidate{Kind: CandidateInstruction, TargetRole, Diff, Reason, EvidenceIDs}`。触发条件对齐架构验收：**同一 TargetRole 同类失败 Evidence ≥2 条**才生成候选（`internal/evidence` Query 失败簇计数）。首版**由开发者/人工确认生成**，不做自动 LLM 生成。Experience 在此的角色（对应 improve.md 四个 P0 Feature 之三）：为 Diagnoser 提供"该角色反复失败的模式 + 可复用解法"，与 Evidence（本次失败的具体诊断）互为补充。
2. **验证侧**：`CandidateVerifier` 三道关补齐（对照第 8 章发布门禁）：
   - 关 1 `staticCheck`：已有（危险模式拒绝）✅；
   - 关 2 `replayFailureCases`：从 `internal/evidence` Store 取该 TargetRole 的失败轨迹（`Filter{Source: "result_verifier"}`），回放 candidate diff 验证改善（首版允许降级断言，接口先立住）；
   - 关 3 `checkRegression`：`ares_arena/regression.go` 保留案例集回放，回归即拒绝。
   - **修复状态机断链**：`Verify()` 返回 Success 时管线显式调用 `c.Verify()` 置 `StatusVerified`，失败调 `c.Reject(reason)`。
3. **发布侧（v2 复用现有管道）**：Candidate 验证通过后**不再自建 promoteToStable 直达稳定区**，改为：
   - **变更载体**：`patch.RuntimePatch{ID, Type: PatchChangeInstruction, Target: "profile:<role>", Value: 新 Instructions, Reason: c.Reason, Source: "candidate", Rollback: 旧 Instructions}`；`patch.Registry.Register("profile:<role>", &ProfileExecutor{profileStore})`（`Executor.Apply` 改候选区并返回回滚补丁）。
   - **决策口**：`coordinator.Submit(PatchProposal{Patch, Source: SourceCandidate(新增 PatchSource 常量), Reason, Priority})` → `coordinator.Evaluate(ctx)`。非 GA source 走 priority 规则：`Priority >= AutoApplyThreshold → DecisionApply`，否则 Delay/Drop。
   - **发布执行**：`DecisionApply` 后经 `deployment.DeploymentPipeline.Deploy(ctx, patch)`（canary：staging 应用 → shadow 评估 → live 应用；`ShadowSampleSize`/`PromotionThreshold`/`RollbackThreshold` 复用）→ 成功后 `ProfileStore.SetStable(role, newProfile)`；回滚由 `RuntimePatch.Rollback` + deployment 自动回滚承担。
   - **审计（对照 `release_manifest.json`）**：`coordinator.DecisionHistory()`（谁决定、为何）+ `deployment.History()`（灰度记录）+ `ProfileStore` 版本号，共同构成 manifest；拒绝的候选保留 `RejectionReason` 供下一轮查阅。
4. **候选空间有边界**（Self-Harness）：Diagnoser 输入 = 失败证据 + 该角色必须保留的成功行为（从 `ares_arena` 保留集取）+ 此前被拒绝的候选（`CandidateStore.ListByStatus(StatusRejected)`）。

**第 8 章安全边界（不可逾越）**：
- ❌ Evaluator/Verifier/Safety Gate/Audit Log/Release Gate 不在 Candidate 可修改范围（coordinator 的 DecisionPolicy / deployment 的 Threshold 均不可被候选写入）；
- ❌ 自动创造新 Agent；
- ✅ Candidate 与 Stable 物理隔离（`ProfileStore` 分区），失败可回滚（`RuntimePatch.Rollback` + deployment）；
- ✅ 只有 verified → released 或 rejected 两个出口（`coordinator` 只发 Apply/Reject/Delay/Drop）。

**验收**：`candidate_pipeline_test.go`：证据驱动生成→verify→coordinator Apply→deployment Deploy→下一轮读到新 Instructions；危险 diff 被 staticCheck 拒绝；回归案例触发 reject；DecisionHistory/PatchHistory 留有审计记录。

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

> P0-3 / P0-4 可并行；P0-5 依赖 P0-4 的 Evidence 输入与 coordinator/deployment 既有接口。每步独立可合入。

---

**维护者**：GoAgent Core Team
**版本**：0.3.0（方案 v2）
**状态**：📋 待评审（代码未修改）

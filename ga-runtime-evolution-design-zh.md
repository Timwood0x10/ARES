# GA 接入 Runtime：可进化 Agent OS 设计方案

> 定位：**进化是 runtime 层的能力**。runtime 主动采集证据、主动评分、主动调度进化、主动验证与回退；
> agent 只负责「被调度、执行、返回结果」，它的进化是**被动**的 —— agent 从不发起进化，只消费 runtime 下发的活跃策略。

---

## 1. 职责边界（先定契约，再谈实现）

| 层 | 职责 | 绝不做 |
| --- | --- | --- |
| **Agent（被动）** | 执行任务；读取 `agents.StrategySource.GetActiveStrategy` 得到当前策略（prompt/params）；把执行结果、耗时、成败、工具使用如实上报事件 | 不触发进化、不选择策略、不评分、不回退 |
| **Runtime 采集面（主动）** | FlightRecorder / EventStore / EvidenceStore：把 agent 的执行事件转成结构化证据（fitness / execution_trace / failure / dimension_eval） | 不做决策 |
| **Runtime 学习面（主动）** | GA 种群、变异/交叉、Scorer（启发式 + LLM + memory-aware）、经验蒸馏引导 | 不直接改线上状态 |
| **Runtime 验证面（主动）** | 影子评估、Eval 套件、Deployment Pipeline staging、Guardrails | 不长期持有状态 |
| **Runtime 治理面（主动）** | 决定 promote / hold / rollback；写 StrategyStore；记录分数窗口 | 不参与打分实现细节 |

一句话：**agent 是传感器与执行器，runtime 是大脑。**

---

## 2. 现状盘点（基于源码核实）

### 2.1 已经打通的部分（可直接复用）

- **证据链路是活的**
  `internal/ares_flight/collector.go:190-217` 把 workflow / scheduler / recovery 三路 fitness 写入 `evidence.Store`；
  `internal/ares_bootstrap/bootstrap_steps.go:568` 的 `recentFitnessSummary(ctx, store, source, limit)` 已能给出窗口均值（仅接受 `[0,1]`）。
- **策略下发链路是活的**
  `evolution.StrategyStore.SetActive` → `NewStrategySource`（`internal/ares_bootstrap/strategy_adapter.go:21`）→ `agents.StrategySource.GetActiveStrategy` → chat/sub executor 与 spawn/quota 策略源。
- **GA 主循环是活的**
  `GenomePopulationAdapter.Run`（`internal/ares_evolution/genome_wiring_run.go:45`）：pre-guardrail → `EvolveAfterScoring` → `recordOutcomesLocked` → post-guardrail → `submitToCoordinator` → `deployBestStrategy`。
- **Coordinator / Patch / Deployment 通路存在**
  `submitToCoordinator` 用 FitnessGenome 均值 ×100 作为 fitness 门限，`deploymentAdapter.Deploy` 只把 `DeploymentPromoted` 视为成功（`internal/ares_bootstrap/deployment_wiring.go:104`）。

### 2.2 断裂点（本方案要解决的核心问题）

| # | 断点 | 源码证据 | 后果 |
| --- | --- | --- | --- |
| **B1** | **回退是死的**：`ActiveStrategyManager.RecordScore` / `Rollback` 在生产代码中零调用点，`RollbackPolicy.Evaluate()` 运行时从不被评估 | `rollback_policy.go:397/468` 仅测试调用 | 滑动窗口、突降/渐降检测全部空转；只剩 `Deploy` 内 guardrail 的即时自动回滚 |
| **B2** | **没有验证门**：`deployBestStrategy` 无条件把每代 best 直接 `Deploy` | `genome_wiring_run.go:291-303` | "验证通过才保留" 不存在；一次坏变异立刻污染线上 agent |
| **B3** | **影子评估在 serve 路径失效**：`ShadowEvaluator` 只被 `DreamCycle` 持有，而 bootstrap 强制 `EnableDreamCycle = false` | `genome_wiring_system.go:529-535`；`bootstrap_steps.go:205` | 灰度对比能力被绕过 |
| **B4** | **两个驱动源并存且语义分裂**：bootstrap 的 `MinInterval` ticker 无条件跑代；`EvolutionScheduler` 事件驱动（含降级检测）在 `comp.Evolution != nil` 时只被 `SetAdapter` | `bootstrap_steps.go:266-297`、`206`、`257-261` | 触发时机不可解释，节流/降级判断被 ticker 旁路 |
| **B5** | **Eval 未接入决策**：`ares_eval` 的 `EvaluatorRegistry` / `LLMJudgeEvaluator` / `AgentTestRunner` 已构建但不参与 promote/rollback | `internal/ares_eval/evaluator.go:127`；`provide_evolution.go setupEvaluators` | 验证只有 GA 自评分，缺少独立裁判 |
| **B6** | **staging 评估维度单一**：`deploymentStagingRuntime.Evaluate` 只看 `source="workflow"` 的 fitness，无证据直接返回 `0.0` | `deployment_wiring.go:40-54` | 冷启动阶段一切 patch 被拒；scheduler/recovery/knowledge 维度被忽略 |

**结论**：项目已经有全部零件，缺的是一个**统一的进化控制平面**把「采集 → 评分 → 提议 → 验证 → 保留/回退」串成一条不可绕过的状态机。

---

## 3. 目标架构：Runtime 进化控制平面

```
                        ┌──────────────────────────────────────────┐
   agent (被动)          │        Runtime Evolution Control Plane    │
  ┌───────────┐         │                                          │
  │ execute   │ events  │  ① OBSERVE   EventStore + EvidenceStore   │
  │ task      ├────────►│              FlightRecorder / eval bridge │
  │           │         │                     │                    │
  │ read      │◄────────┤  ⑤ COMMIT    StrategyStore.SetActive      │
  │ strategy  │ strategy│              (promote | hold | rollback)  │
  └───────────┘         │                     ▲                    │
                        │  ② JUDGE     RuntimeFitnessAggregator     │
                        │              (evidence + eval + cost)     │
                        │                     │                    │
                        │  ③ PROPOSE   GA: EvolveAfterScoring       │
                        │              → candidate strategy         │
                        │                     │                    │
                        │  ④ VERIFY    Guardrails → Shadow →        │
                        │              Eval suite → Deployment      │
                        │              staging → 阈值判定           │
                        └──────────────────────────────────────────┘
```

新增一个**编排器**（建议 `internal/ares_evolution/lifecycle.go`，类型 `StrategyLifecycle`），它是唯一有权调用 `ActiveStrategyManager.Deploy/Rollback/RecordScore` 的组件。`deployBestStrategy` 改为向它提交候选，而不是直接部署。

### 3.1 候选策略状态机

```
        ┌─────────┐  GA 产出 best
        │ CANDIDATE│◄──────────────── GenomePopulationAdapter.Run
        └────┬────┘
             │ Guardrails.PostEvolveCheckForSource 通过
             ▼
        ┌─────────┐  影子评估：候选 vs 当前活跃
        │ SHADOW  │  样本 < MinSamples → 继续留在 SHADOW（不下发）
        └────┬────┘
             │ WinRate ≥ MinWinRate 且 Eval 套件通过
             ▼
        ┌─────────┐  写 StrategyStore.SetActive；previous 保留
        │ ACTIVE  │
        └────┬────┘
             │ 线上证据持续喂入 RollbackPolicy.RecordScore
             ▼
     ┌───────────────┐  Evaluate().ShouldRollback
     │ DEGRADED      ├──────────► Rollback() → previous 重新 ACTIVE
     └───────────────┘            并把该候选加入黑名单（禁止本代重复提名）
```

关键设计原则：

1. **单一提交入口**：只有 `StrategyLifecycle.Submit(candidate)` 能改变活跃策略。
2. **验证不通过就不下发**：CANDIDATE 停在 SHADOW 阶段，agent 完全感知不到。
3. **正确保留 = previous 永不丢**：`Deploy` 时把 current 存为 previous，`Rollback` 依赖它（`rollback_policy.go:401` 已实现，只是没人调）。
4. **回退依据来自线上真实证据**，不是 GA 自评分。

---

## 4. 五个阶段的具体设计

### ① OBSERVE：runtime 主动收集

新增 `RuntimeObserver`（`internal/ares_evolution/observer.go`），订阅事件并把观测折算为 `[0,1]` 的 **运行时适应度样本**：

```go
// StrategySample 是一次线上执行对当前活跃策略的观测。
type StrategySample struct {
    StrategyID string
    Success    bool
    Score      float64       // [0,1]，由 outcome + dimension_eval 折算
    Latency    time.Duration
    CostUSD    float64
    TaskType   string
    At         time.Time
}
```

来源与权重（可配置）：

| 来源 | 事件 / 证据 | 折算 |
| --- | --- | --- |
| 任务成败 | `EventTaskCompleted` / `EventTaskFailed` | 1.0 / 0.0（复用 `scheduler.go:367-369` 的语义，但归一化到 `[0,1]`） |
| 维度诊断 | `evidence.KindDimensionEval` | 直接取分数 |
| 工作流 fitness | `evidence.KindFitness` source=`workflow` | `recentFitnessSummary` |
| 调度 fitness | source=`scheduler` | 同上 |
| 恢复 fitness | source=`recovery` | 同上 |
| 成本/时延惩罚 | flight trace | 超预算按比例扣分 |

`RuntimeObserver` 的输出流向：
- `EvidenceStore`（写 `KindFitness`，source=`strategy`，供 GA scorer、
  staging 评估与 lifecycle 的回退窗口复用）。`StrategyLifecycle` 通过
  aggregator 读取这批证据（窗口均值、min-samples 门控），**不做**逐样本直喂——
  事件→证据→聚合的路径是回退判定的唯一数据来源。
- 订阅事件：`task.completed` / `task.failed` / `task.agent_stopped`。其中
  agent_stopped 仅在 reason 标记为异常终止时计 0.0 样本（正常/显式停止
  不产生样本）。

> 注（tech-debt）：`StrategySample` 的 `Latency` / `CostUSD` 字段已随 §4②
> 的 penalty 项一并移除——task 事件目前不携带 cost/latency，等 flight-trace
> 数据进入 EventStore payload 后再恢复（见 observer.go 的 tech-debt 注释）。
>
> 这一步落实「runtime 主动收集信息」：agent 只发事件，不知道有人在给它打分。

### ② JUDGE：统一适应度聚合

新增 `RuntimeFitnessAggregator`：

```
fitness = w_outcome·outcome + w_dim·dimensionEval + w_flow·workflowFitness
          + w_sched·schedulerFitness + w_recovery·recoveryFitness
```

（默认权重 0.40 / 0.25 / 0.15 / 0.15 / 0.05，和为 1。§4① 表中的
"成本/时延惩罚" 项**未实现**：task 事件目前不携带 cost/latency，实现即引入
死配置——已挂 tech-debt TODO（fitness_aggregator.go），数据源到位后再接。）

- 归一化到 `[0,1]`（与 `recentFitnessSummary` 的过滤区间一致）；
- **判定门控按用途分两级**（review 严重项 4）：
  - **回退路径**（`Window(ctx, activeID)`，带 strategy ID）：`strategy`
    源自身必须 ≥ `MinSamplesBeforeJudge`——workflow/scheduler/recovery 是
    全局无归属证据，只能参与加权平均，**不能**代替该策略自己的样本满足
    判定门槛（§4⑤ 原则 4）；
  - **staging 路径**（`Window(ctx, "")`）：维持跨源总数门槛（原契约）；
- 冷启动返回 `ok=false`，由调用方决定保守策略。

### ③ PROPOSE：GA 只提议，不部署

`genome_wiring_run.go` 修改：

```go
// 原：a.deployBestStrategy(ctx)   —— 无条件部署
// 新：
if a.lifecycle != nil {
    a.lifecycle.Submit(ctx, a.pop.BestStrategy(), a.pop.Stats().Generation)
}
```

`deployBestStrategy` 降级为 `StrategyLifecycle` 内部在「验证通过」分支才调用的私有步骤。

### ④ VERIFY：四道门，串行短路

```go
type VerifyGate interface {
    Name() string
    Check(ctx context.Context, cand, active *mutation.Strategy) (pass bool, score float64, reason string)
}
```

按顺序（当前实现状态如实标注）：

| 门 | 实现 | 不通过的处理 |
| --- | --- | --- |
| **G2 Shadow** | `StrategyLifecycle` 内置 `shadowVerifyGate`（只读消费 `ShadowEvaluator.ShouldDeploy`），fail-closed：**无比较样本即拒绝**（§3.1 "样本 < MinSamples → 留在 SHADOW 不下发"）；阈值来自 `evolution.shadow` | 胜率不足 / 样本不足 / 无数据 → 全部拒绝 |
| **G3 Eval 套件** | `eval_suite` 配置了 YAML 回归集才构建（`buildEvalGate`）：AgentTestRunner 把候选 PromptTemplate 前置到用例输入，llm_judge 打分 [0,1] | 低于 `eval_min_score` → 丢弃；未配置 = 不注册本门 |
| **G1 Guardrail** | **不在 lifecycle 门序列内**：仍是 adapter 层 population 级 pre/post 检查（`genome_wiring_run.go`），拦的是整代而非单候选 | 记 guardrail metric，候选随代丢弃 |
| **G4 Deployment staging** | 仅候选携带 RuntimePatch 时经 Coordinator → DeploymentPipeline；与 `lifecycle.Submit` 是两条独立路径 | 按 pipeline 语义拒绝 |

> **诚实注记（review 阻断项 1 的处置）**：G2 之所以 fail-closed，是因为
> 生产默认 `EnableDreamCycle=false`，过去**没有任何组件调用
> `StartShadow/RecordResult` 喂影子比较**。**P0-9（见 §6 任务表）已落地**：
> `ShadowSampler` 在候选提交时同步喂「候选 vs 活跃」比对样本，复用
> evaluator 的独立 scorer，仅在 `!cfg.EnableDreamCycle` 时接线（与 DreamCycle
> 互斥、只留一个 feeder）。**仍存两条边界**：
> 1. 默认 bootstrap 未接独立 scorer（`llm_scoring` off）时 sampler 刻意产出
>    zero comparison，G2 维持 fail-closed，候选留在 SHADOW；
> 2. 已接 scorer 时，N 次比较是对**同一对策略**重复打分，只有 LLM scorer
>    （非确定）才构成独立样本；确定性 scorer 下 MinSamples 退化为重复计数。
>
> 二者都指向同一个后续项：per-task 实执行 A/B 采样。这是"宁可不进化，
> 不无验证上线"的安全取向；放行（pass-through）曾是原实现，但它使整条
> 验证管线沦为橡皮图章，已被否决。
>
> **seed 部署例外**：无活跃策略时的首个候选不做门禁直接 promote——
> 影子比较需要对照物，且 §9 的回退机制依赖这个基线作为 previous。

**修复 B3**：`ShadowEvaluator` 只要 `evolution.shadow.enabled` 即构建
（不再要求 LLM scorer 存在），并同时挂到 DreamCycle（启用时）与
`StrategyLifecycle` 的 G2 门。

**修复 B6**：`deploymentStagingRuntime.Evaluate` 改为多维聚合 + 显式冷启动策略：

```go
sources := []string{"workflow", "scheduler", "recovery"}
// 加权平均；全部无证据时返回 cfg.ColdStartScore（默认 0.5，可配 0.0 保守）
```

### ⑤ COMMIT / ROLLBACK：让回退真正活起来

`StrategyLifecycle` 内部一个后台 tick（复用 `comp.bgGroup`）：

```go
func (l *StrategyLifecycle) watch(ctx context.Context) {
    for range ticker.C {
        // 1) 把 observer 累积的线上样本喂进窗口
        if mean, n, ok := l.aggregator.Window(ctx, l.activeID()); ok && n >= minSamples {
            l.asm.RecordScore(l.generation(), mean)      // ← 修复 B1
        }
        // 2) 评估降级
        if d := l.asm.RollbackPolicy().Evaluate(); d.ShouldRollback {
            prev, err := l.asm.Rollback(ctx)             // ← 修复 B1
            l.emitRollbackEvent(ctx, d, prev, err)
            l.asm.RollbackPolicy().Reset()               // 避免同一窗口反复触发
            l.blacklist(l.lastCandidateID)
        }
    }
}
```

要点：
- `RecordScore` 的分数**与 `DegradationThreshold` 同量纲**：`[0,1]`。
  `EvolutionScheduler.RecordScore` 入口做 clamp（§8 验收 6 的落地口径是
  clamp 而非断言——越界输入降级为"恒好/恒差"，而不是 panic / 丢弃）。
- **回退窗口去自相关**：watch 每个tick 只在证据窗口**新增**（count 前进）
  时才 `RecordScore`，同一批证据不会被反复平均（否则窗口 5/最小样本 3
  ⇒ 90 秒内 3 个高度重叠的样本：突降被平滑、渐降检测被噪声触发）。
- **promote 即清窗**：每次成功 promote 都 `RollbackPolicy().Reset()`，
  新策略不会用旧策略的低分窗口被误判突降。
- `Rollback` 依赖 `previous != nil`，所以首次部署后至少要有一次成功 promote
  才具备回退能力；冷启动期由 seed 基线策略充当 previous。
- 回退后候选进入黑名单 `banUntil = generation + blacklist_generations`
  （默认 3 代，§9 震荡抑制）。
- 每次 promote / rollback 都写一条决策证据（source=`lifecycle`，独立于
  `[0,1]` fitness 窗口——GA 分数是 0–100 刻度，不得混入）。
- watch 循环是**受纳管**的后台 goroutine：bootstrap 侧注册到
  `comp.bgGroup`（ctx 结束时 Stop 并等待退出），循环内带 recover（K3）。
- `Snapshot()`（HTTP /api/evolution/lifecycle）内的 Window 查询带 2s 超时，
  慢存储不会挂住控制面请求。

---

## 5. 触发口径统一（修复 B4）

删除 bootstrap 里"无条件 ticker 跑代"的语义，改为**ticker 只做心跳，判定权交回 `EvolutionScheduler`**：

- `EvolutionScheduler` 成为唯一入口，永远启用：`gaCfg.EnableScheduler = true`（不再取决于 `comp.Evolution == nil`）。
- 给 scheduler 增加一个 `Tick(ctx)` 方法，走同一套 `shouldEvolve` + `checkGuardrails` + `MinInterval` 节流逻辑；bootstrap 的 ticker 改为调用 `scheduler.Tick(ctx)` 而不是 `popAdapter.Run(ctx)`。
- 旧系统 scheduler 存在时仍 `SetAdapter`，但不再产生第二条判定路径。
- `scheduler.RecordScore` 的量纲同步改为 `[0,1]`（`taskScoreSuccess=1.0` / `taskScoreFailure=0.0`），与 `RollbackPolicy` 对齐。

好处：进化时机可解释（日志里能说清是 Idle / Threshold / Demand 哪个触发）。

> **已知缺口（如实登记，review §5 复核）**：
> 1. bootstrap 优先驱动 **legacy scheduler**（`provide_evolution.go` 构造时
>    只传 `WithEnabled/WithMinInterval`，**没有挂 guardrails**）⇒ ticker 路径
>    的 `checkGuardrails` 恒 true。G1 的真实防线目前只有 adapter 层的
>    pre/post 检查。后续：为 legacy scheduler 构造补 guardrails 选项。
> 2. 两个 scheduler 并存（legacy Registered + wired 未注册）是过渡态；
>    收敛为单一实例是后续清理项。
> 3. `default:` 分支仍保留无 EventStore 配置下的无条件 `popAdapter.Run`
>    （极简配置兼容），与"删除无条件 ticker"的目标态不符——记 TODO。

---

## 6. 落地改造清单

### P0 — 让闭环闭合（必须）

| 项 | 文件 | 改动 |
| --- | --- | --- |
| P0-1 | `internal/ares_evolution/lifecycle.go`（新增） | `StrategyLifecycle`：`Submit` / `watch` / 四道 `VerifyGate` / 黑名单 |
| P0-2 | `internal/ares_evolution/observer.go`（新增） | `RuntimeObserver` + `StrategySample`，订阅 `EventTaskCompleted/Failed/AgentStopped` |
| P0-3 | `internal/ares_evolution/fitness_aggregator.go`（新增） | `RuntimeFitnessAggregator.Window(ctx, strategyID)` 归一化到 `[0,1]` |
| P0-4 | `genome_wiring_run.go:94-97` | `deployBestStrategy` → `lifecycle.Submit`，部署改由 lifecycle 决策 |
| P0-5 | `genome_wiring_system.go:513-543` | ShadowEvaluator 脱离 DreamCycle 独立装配；构造并注入 `StrategyLifecycle` |
| P0-6 | `bootstrap_steps.go:205-209` | `ShadowEvalConfig.Enabled=true`；补 `RollbackPolicyConfig` 的阈值/窗口/最小样本（从 YAML 读取） |
| P0-7 | `bootstrap_steps.go:266-297` | ticker 改调 `scheduler.Tick(ctx)`；`EnableScheduler=true` 恒定 |
| P0-8 | `scheduler.go:367-369` | 分数量纲改 `[0,1]`；新增 `Tick(ctx)` |
| **P0-9（已落地，有限制）** | `internal/ares_evolution/shadow_sampler.go`（新增 `ShadowSampler`）；`lifecycle.go`（`Submit` 前 `Prime`）；`genome_wiring_system.go`（`!cfg.EnableDreamCycle` 时接线） | **影子比较 feeder**：新增 `ShadowSampler` 在候选提交时同步产出「候选 vs 活跃」的比对样本，复用 evaluator 的独立 scorer。判据是 `!cfg.EnableDreamCycle`（不是 `system.DreamCycle == nil`：DreamCycle **实例**在 `EnableScheduler` 时也会构建，而 bootstrap 正是 `DreamCycle=false + Scheduler=true`）。**限制**：同一对策略被重复打分 N 次，仅当 scorer 非确定（LLM，temperature>0）时才是独立样本；确定性 scorer 下 MinSamples 只是重复计数。真正的 per-task A/B 实执行采样仍待后续 |

### P1 — 让验证有独立裁判

| 项 | 文件 | 改动 |
| --- | --- | --- |
| P1-1 | `internal/ares_evolution/gate_eval.go`（新增） | 把 `ares_eval.EvaluatorRegistry` + `AgentTestRunner` 包成 `VerifyGate`（G3） |
| P1-2 | `provide_evolution.go setupEvaluators` | 把已构建的 `LLMJudgeEvaluator` / `DimensionJudgeBridge` 注入 G3，而不是仅注册后闲置 |
| P1-3 | `deployment_wiring.go:40-54` | staging `Evaluate` 多维聚合 + `ColdStartScore` 配置 |

### P2 — 可观测与治理

| 项 | 改动 |
| --- | --- |
| P2-1 | 指标：`ares_evolution_promote_total{result}`、`ares_evolution_rollback_total{reason}`、`ares_evolution_shadow_win_rate`、`ares_evolution_gate_reject_total{gate}` |
| P2-2 | HTTP：`GET /evolution/lifecycle` 返回当前状态机快照（active / previous / shadow / 窗口分数 / 最近决策） |
| P2-3 | 策略血缘：promote/rollback 写入 knowledge graph（复用 `attachEvolutionKnowledgeProvider`） |
| P2-4 | 人工闸门：`evolution.require_manual_approval=true` 时候选停在 SHADOW，等 API 放行 |

---

## 7. 配置（`configs/*.yaml`）

> **实现注记**：YAML 键是**扁平权重键**（`outcome_weight` …），不是嵌套的
> `weights:` 块；`lifecycle.enabled` / `rollback.enabled` / `shadow.enabled`
> 三个开关由 bootstrap 固定置 true（这三个子系统的存在性由编译期接线决定，
> 不暴露 YAML 开关——避免"关了 shadow 又留着 G2 门"这类组合矛盾）；
> `penalty:` 块**故意不存在**（无数据源，见 §4②）。

```yaml
evolution:
  min_interval: 5m              # 已有，ticker 心跳
  lifecycle:
    fitness_window: 50          # 线上样本窗口
    min_samples_before_judge: 10 # 少于此值不做 promote/rollback 判定
    cold_start_score: 0.5       # staging 无证据时的保守分
    watch_interval: 30s         # 回退 watch 循环周期
    blacklist_generations: 3    # 回退候选的禁提名代数（§9 震荡抑制）
    outcome_weight: 0.40        # JUDGE 权重（扁平键；全部缺省=代码默认，
    dimension_eval_weight: 0.25 #  部分设置=按原样使用，聚合器按权重和归一）
    workflow_weight: 0.15
    scheduler_weight: 0.15
    recovery_weight: 0.05
  rollback:
    degradation_threshold: 0.15  # 与 [0,1] 量纲一致
    window_size: 5
    min_samples: 3
  shadow:
    min_samples: 20
    min_win_rate: 0.55
  gates:
    eval_min_score: 0.7
    require_manual_approval: false
    eval_suite: configs/eval/regression.yaml   # G3 回归套件（ares_eval.TestSuite YAML）；缺省 = 无 G3 门
```

> **G3 门配置语义（B5/B2 落地注记）**：`eval_suite` 指向 ares_eval
> `TestSuite` 格式的 YAML 文件（`{name, test_cases: [{id, input}, ...]}`）。
> 配置后，bootstrap 构建 AgentTestRunner（执行器把候选策略的 PromptTemplate
> 前置到每个用例输入上，llm_judge 打分归一化到 [0,1]），候选必须 ≥
> `eval_min_score` 才能 promote。**未配置 = 不注册 G3 门**（诚实的缺省，
> 而不是恒放行的假门）；配置了但加载失败 = Bootstrap 直接报错（fail-closed，
> 不允许拼写错误静默削弱验证管线）。
>
> **P2-4 语义**：`require_manual_approval: true` 时 Submit **立即返回**，
> 候选停在 SHADOW（挂起的是候选，不是进化心跳的 goroutine）；批准走
> `POST /api/evolution/approve`（401 未鉴权 / 409 无挂起候选 / 200 返回
> 新 active_id）。挂起期间的新 Submit 一律拒绝，不得静默顶替。

---

## 8. 验收标准（可测）

在 `internal/ares_bootstrap/closure_feedback_loop_test.go`（build tag `closure`）的既有断言基础上补：

1. **验证门生效**：注入一个明显更差的候选（score 低于 active），断言 `StrategyStore.GetActive` 的 ID **不变**。
2. **正确保留**：注入更优候选并喂足影子样本，断言 active 切换，且 `asm.Previous()` 指向旧策略。
3. **失败回退**：promote 后连续注入 `EventTaskFailed`，断言窗口分数下降触发 `Rollback`，`GetActive` 回到旧 ID。
4. **agent 被动性**：断言 agent 侧只通过 `GetActiveStrategy` 读到策略，全链路无 agent 触发的进化调用（可用接口隔离 + 编译期断言保证）。
5. **触发单一化**：断言在 `MinInterval` 内多次 `Tick` 只跑一代（节流生效）。
6. **量纲一致性**：单测断言 `RecordScore` 对越界输入 **clamp 到 `[0,1]`**
   （实现口径是 clamp 而非断言失败——错误量纲的调用方降级为"恒好/恒差"，
   而不是污染窗口或 panic；见 `scheduler.go` RecordScore 与
   `TestClosure_RecordScore_ClampsToUnitInterval`）。

---

## 9. 风险与缓解

| 风险 | 缓解 |
| --- | --- |
| 影子评估拖慢 promote（样本积累慢） | 允许 `min_samples` 分级：低风险参数（temperature）用小样本，prompt 级变更用大样本 |
| 回退震荡（promote → rollback → promote 同一候选） | rollback 后把候选 ID 加黑名单 `blacklist_generations`（默认 3）代——实现为 `banUntil = rollBackGen + N`，跨代有效；`RollbackPolicy.Reset()` 清窗口（promote 与 rollback 两侧都清） |
| LLM 打分成本 | 沿用现有 `TieredScorer` + `budget`（`buildRunScorer` 已实现缓存/批量/预算门） |
| 冷启动无证据全拒 | `cold_start_score` 配置化；seed 基线策略永远可作为 previous |
| 量纲混用引入静默 bug | 在 `RecordScore` 入口加 `[0,1]` clamp（§8 单测 6） |
| 手动审批挂起拖死进化心跳 | Submit 非阻塞持有候选；审批期间拒绝新 Submit（不静默顶替） |

---

## 10. 实施顺序建议

> **状态注记（0.3.1 实况）**：本节是原方案的渐进式开门计划；实际落地是
> **一次性全量**（P0/P1 一个 PR 系列），依赖 §8 的 6 条验收断言
> （`closure` build tag）与两道架构门禁承担"逐步回退"的安全职责。
> 未按五步走的原因：§8 断言与门禁先行落地后，每一步的独立验证已被
> 测试替代。遗留的渐进项：
> 1. ~~**P0-9**~~：影子比较 feeder 已落地（`ShadowSampler`，见 §6/§4④）。
>    剩余两条边界：默认无独立 scorer 时仍 fail-closed；已接 scorer 时
>    N 次比较是同一对策略的重复打分，需 per-task 实执行 A/B 采样才有统计意义；
> 2. legacy scheduler 补 guardrails 构造选项（§5 已知缺口 1）；
> 3. 双 scheduler 收敛（§5 已知缺口 2）；
> 4. 无 EventStore 配置的 `default:` 无条件 Run 分支移除（§5 已知缺口 3）；
> 5. 两份白名单 12 条（架构门禁 6.1/6.2）按计划另立 PR 消化。

原五步计划（存档）：

1. **第一步只做 P0-2/P0-3**（观测 + 聚合），先把线上真实分数打出来看日志，不改任何决策 —— 零风险。
2. **第二步做 P0-1/P0-4/P0-5**，引入 lifecycle，但把所有门配成"直通"（等价当前行为），确认无回归。
3. **第三步逐个打开门**：Guardrail → Shadow → Eval，每次只开一个，观察 `gate_reject_total`。
4. **第四步接回退**（watch loop），先只打日志不真的 `Rollback`（dry-run 开关），确认判定准确后再放行。
5. **最后统一触发口径**（P0-7/P0-8），删掉无条件 ticker。

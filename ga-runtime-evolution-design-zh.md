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

`RuntimeObserver` 的输出**同时**流向两处：
- `StrategyLifecycle`（喂 `RollbackPolicy.RecordScore`，用于回退判定）；
- `EvidenceStore`（写 `KindFitness`，source=`strategy`，供 GA scorer 与 staging 评估复用）。

> 这一步落实「runtime 主动收集信息」：agent 只发事件，不知道有人在给它打分。

### ② JUDGE：统一适应度聚合

新增 `RuntimeFitnessAggregator`：

```
fitness = w_outcome·outcome + w_dim·dimensionEval + w_flow·workflowFitness
          + w_sched·schedulerFitness − penalty(cost, latency)
```

- 归一化到 `[0,1]`（与 `recentFitnessSummary` 的过滤区间一致，避免 `bootstrap_steps.go:591` 把样本丢掉）；
- 冷启动（样本 < N）返回 `ok=false`，**由调用方决定保守策略**（见 B6 修复）；
- 该聚合器同时作为 GA `memoryScorer` 的证据输入，让 GA 的自评分与线上真实表现对齐。

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

按顺序：

| 门 | 实现 | 不通过的处理 |
| --- | --- | --- |
| **G1 Guardrail** | `EvolutionGuardrails.PostEvolveCheckForSource` | 直接丢弃候选，记 metric |
| **G2 Shadow** | `ShadowEvaluator.StartShadow` + `Evaluate` + `ShouldDeploy`（`shadow_evaluator.go:125/273/162`） | 样本不足 → 保持 SHADOW；胜率不足 → 丢弃 |
| **G3 Eval 套件** | `ares_eval.EvaluatorRegistry` + `AgentTestRunner` 跑固定回归集，取 `EvalScore` 加权 | 低于 `eval_min_score` → 丢弃 |
| **G4 Deployment staging** | `deployment.DeploymentPipeline` staging（仅当候选携带 RuntimePatch 时） | 按 pipeline 语义拒绝 |

**修复 B3**：让 `ShadowEvaluator` 不再依赖 `DreamCycle`。
`genome_wiring_system.go:529` 改为把 `system.ShadowEvaluator` 也挂到 `popAdapter` / `StrategyLifecycle`；bootstrap 保持 `EnableDreamCycle=false`，但必须设置 `gaCfg.ShadowEvalConfig.Enabled = true` 且提供 `Scorer`（LLM scorer 或启发式，二者都可）。

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
- `RecordScore` 的分数**必须与 `DegradationThreshold` 同量纲**。`RollbackPolicy` 默认阈值 `0.15`（`rollback_policy.go:122`），因此喂入的必须是 `[0,1]`，**不能**沿用 `scheduler.go` 的 `100/0`。这是一个必须在实现时统一的口径。
- `Rollback` 依赖 `previous != nil`（`rollback_policy.go:401`），所以首次部署后至少要有一次成功 promote 才具备回退能力；冷启动期由 `bootstrap-root` 基线策略充当 previous。
- 每次 promote / rollback 都写一条事件 + `KindFitness` 证据，形成可追溯轨迹（供 `/evolution/trajectory` 与 knowledge graph 消费，`attachEvolutionKnowledgeProvider` 已就绪）。

---

## 5. 触发口径统一（修复 B4）

删除 bootstrap 里"无条件 ticker 跑代"的语义，改为**ticker 只做心跳，判定权交回 `EvolutionScheduler`**：

- `EvolutionScheduler` 成为唯一入口，永远启用：`gaCfg.EnableScheduler = true`（不再取决于 `comp.Evolution == nil`）。
- 给 scheduler 增加一个 `Tick(ctx)` 方法，走同一套 `shouldEvolve` + `checkGuardrails` + `MinInterval` 节流逻辑；bootstrap 的 ticker 改为调用 `scheduler.Tick(ctx)` 而不是 `popAdapter.Run(ctx)`。
- 旧系统 scheduler 存在时仍 `SetAdapter`，但不再产生第二条判定路径。
- `scheduler.RecordScore` 的量纲同步改为 `[0,1]`（`taskScoreSuccess=1.0` / `taskScoreFailure=0.0`），与 `RollbackPolicy` 对齐。

好处：进化时机可解释（日志里能说清是 Idle / Threshold / Demand 哪个触发），并且 guardrail 无法被旁路。

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

```yaml
evolution:
  min_interval: 5m              # 已有，改为 scheduler.Tick 的心跳
  lifecycle:
    enabled: true
    fitness_window: 50          # 线上样本窗口
    min_samples_before_judge: 10 # 少于此值不做 promote/rollback 判定
    cold_start_score: 0.5       # staging 无证据时的保守分
    weights:                    # JUDGE 阶段权重，和为 1
      outcome: 0.45
      dimension_eval: 0.25
      workflow: 0.15
      scheduler: 0.15
    penalty:
      cost_usd_budget: 0.05
      latency_budget: 30s
  rollback:
    enabled: true
    degradation_threshold: 0.15  # 与 [0,1] 量纲一致
    window_size: 5
    min_samples: 3
  shadow:
    enabled: true
    min_samples: 20
    min_win_rate: 0.55
  gates:
    eval_min_score: 0.7
    require_manual_approval: false
```

---

## 8. 验收标准（可测）

在 `internal/ares_bootstrap/closure_feedback_loop_test.go`（build tag `closure`）的既有断言基础上补：

1. **验证门生效**：注入一个明显更差的候选（score 低于 active），断言 `StrategyStore.GetActive` 的 ID **不变**。
2. **正确保留**：注入更优候选并喂足影子样本，断言 active 切换，且 `asm.Previous()` 指向旧策略。
3. **失败回退**：promote 后连续注入 `EventTaskFailed`，断言窗口分数下降触发 `Rollback`，`GetActive` 回到旧 ID。
4. **agent 被动性**：断言 agent 侧只通过 `GetActiveStrategy` 读到策略，全链路无 agent 触发的进化调用（可用接口隔离 + 编译期断言保证）。
5. **触发单一化**：断言在 `MinInterval` 内多次 `Tick` 只跑一代（节流生效）。
6. **量纲一致性**：单测断言 `RecordScore` 输入恒在 `[0,1]`。

---

## 9. 风险与缓解

| 风险 | 缓解 |
| --- | --- |
| 影子评估拖慢 promote（样本积累慢） | 允许 `min_samples` 分级：低风险参数（temperature）用小样本，prompt 级变更用大样本 |
| 回退震荡（promote → rollback → promote 同一候选） | rollback 后把候选 ID 加黑名单 N 代；`RollbackPolicy.Reset()` 清窗口 |
| LLM 打分成本 | 沿用现有 `TieredScorer` + `budget`（`buildRunScorer` 已实现缓存/批量/预算门） |
| 冷启动无证据全拒 | `cold_start_score` 配置化；基线策略 `bootstrap-root` 永远可作为 previous |
| 量纲混用引入静默 bug | 在 `RecordScore` 入口加 `[0,1]` 断言与 clamp，并在 CI 加单测 6 |

---

## 10. 实施顺序建议

1. **第一步只做 P0-2/P0-3**（观测 + 聚合），先把线上真实分数打出来看日志，不改任何决策 —— 零风险。
2. **第二步做 P0-1/P0-4/P0-5**，引入 lifecycle，但把所有门配成"直通"（等价当前行为），确认无回归。
3. **第三步逐个打开门**：Guardrail → Shadow → Eval，每次只开一个，观察 `gate_reject_total`。
4. **第四步接回退**（watch loop），先只打日志不真的 `Rollback`（dry-run 开关），确认判定准确后再放行。
5. **最后统一触发口径**（P0-7/P0-8），删掉无条件 ticker。

这样每一步都可独立验证、可独立回退，符合「活着的系统要能安全地改变自己」这个前提。

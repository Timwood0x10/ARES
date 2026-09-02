# ARES 进化控制平面闭环计划（Evolution Loop Closure Plan）

> 生成日期：2026-09-01
> 分支：`dev`　`VERSION`：`0.3.1`
> 依据：2026-09-01 进化平面逐符号复核（`shadow_sampler.go` / `observer.go` /
> `fitness_aggregator.go` / `lifecycle.go` / `rollback_policy.go` 全文读码）
> 编码规范：`plan/rules/code_rules_v2.md`（§0.1 禁止擅自提交；§0.4 方向优先于接线）
> 关联：`ga-runtime-evolution-design-zh.md`（设计源）、`ares-0.3.1-ga-blockers-plan-zh.md`（GA 阻塞项）

---

## 0. 这份计划要解决什么

`evolution.enabled: true` **不会让进化转起来**。这不是"默认关闭"的正常行为，
而是"用户做了该做的事，系统仍然不动"。

链条断在三处，且每一处都不是逻辑写错：

| 断点 | 位置 | 性质 |
|---|---|---|
| **归因缺失** | 任务完成事件不带 `strategy_id`（`sub/agent.go:421,585` payload 无此键） | 样本全部归到"此刻的 active"，per-task A/B 物理上不可能，且 promote 一旦放开，rollback 归因立刻变成实时正确性 bug |
| **入口全封** | G2 fail-closed（`lifecycle.go:364-370`）+ 默认无独立 scorer（`shadow_sampler.go:119-124` 直接 return） | 种子候选走 seed 例外无条件 promote，之后每一个候选都被拒 |
| **出口空转** | `RuntimeObserver` → `Window` → `RecordScore` → `Rollback` → 黑名单，整条链完整且有测试，但**从未被触发** | 已建成、已验证、闲置 |

**关键认识**：现在的状况不是"选择压力弱"，而是**入口的门把所有东西挡住，
导致出口那套完整的选择机制永远空转**。所以放开晋升不只是"让候选通过"，
是激活一整套已经建好、测过、闲置的淘汰机制。

### 0.1 一个必须先做的方向裁决（§0.4）

代码库里同时存在两套验证范式的完整机器：

| 范式 | 机器 | 现状 |
|---|---|---|
| **事前** shadow 预验证 | `ShadowEvaluator` + G2 门 | fail-closed，挡住一切 |
| **事后** canary + 自动回退 | `RuntimeObserver` + `RollbackPolicy` + `watch` loop（30s） | 完整，从未触发 |

本计划的方向选择：**0.3.1 以事后为主路径，事前降级为"有 scorer 时的可选增强"；
0.4.x 补齐 per-task 真 A/B 后，事前恢复为真验证门。**

理由：
1. 事后那套已经是完整可用的，只是入口把它饿死了 —— 修入口的成本远低于补强事前。
2. 事前要做到"真"必须有独立证据源，而唯一现成的证据源（LLM scorer）成本不可控，
   且设了 seed 之后退化成确定性重复（`shadow_sampler.go:37-45` 自己承认）。
3. Canary + 自动回退是业界对"无法事前充分验证的变更"的标准答案。

**这一条需要你确认后才动手**（涉及 `enabled: true` 的行为语义变更）。

---

## 1. 任务总表

| # | 级别 | 任务 | 阶段 | 预估 |
|---|---|---|---|---|
| **E1** | **P0** | `strategy_id` 任务级归因标记（一切的硬前置） | 0.3.1 | 1.5 人日 |
| **E2** | **P0** | G2 从 fail-closed 改为"诚实缺席 + 安全不变量" | 0.3.1 | 1.5 人日 |
| **E3** | P1 | P2-3 决策证据接上知识图谱消费方 | 0.3.1 | 0.5 人日 |
| **E4** | P1 | G1 定性修正 + G4 不对称登记（纯文档） | 随时 | 0.3 人日 |
| **E5** | P1 | per-task 真 A/B 采样（G2 从 Submit 内联移到 watch loop） | **0.4.x** | 4-6 人日 |
| **E6** | P2 | 观测面：新 metrics + lifecycle 快照补字段 | 0.3.1 | 0.5 人日 |

**依赖图**：

```
E4（纯文档，零风险，可立即做）
E1 ──┬──→ E2 ──→ E6      ← 0.3.1 到此为止，"enabled:true 真的会转"
     └──→ E5              ← 0.4.x，"验证是真的"
E3（独立，随时可做）
```

**E1 是唯一的硬前置**：E2 放开晋升之后 rollback 才会被触发，而 rollback 依赖
`Window(ctx, strategyID)` 的归因正确。归因不修就放开晋升 = 用错误证据武装回退。

---

## 2. 复用清单（新建的东西极少）

这份计划的成本之所以低，是因为绝大部分设施已经在了：

| 需求 | 已有设施 | 位置 | 需要做什么 |
|---|---|---|---|
| 归因字段随任务跨 quantum 存活 | `CheckpointEnvelope`（v1 版本化信封） | `taskfabric/checkpoint_schema.go:27` | 加一个字段，bump v2 |
| 归因字段随任务跨重启存活 | `restoreKey*` 常量 + `foldRestoreEvent` | `taskfabric/restore.go:37-49` | 加一个 key |
| 事件 payload 带归因 | `recordLocked` 统一构造 payload | `taskfabric/fabric.go:658-671` | 加一行 |
| 事件→样本读归因 | `eventToSample` **已经在读** `evt.Payload["strategy_id"]` | `ares_evolution/observer.go:241` | **零改动** |
| 按策略过滤证据窗口 | `querySourceMean(..., strategyID)` **已支持** | `ares_evolution/fitness_aggregator.go:331` | **零改动** |
| 按策略算 fitness | `Window(ctx, strategyID)`，`sources[0] = {"strategy", w, strategyID}` | `fitness_aggregator.go:205` | **零改动** |
| 周期性重评 | `StrategyLifecycle.watch`（30s tick） | `lifecycle.go:677` | E5 时挂载新判定 |
| 自动回退 | `RollbackPolicy.Evaluate` + `asm.Rollback` + 黑名单 3 代 | `lifecycle.go:743`（Evaluate）、`:749`（Rollback）、`:779-782`（黑名单） | **零改动** |
| "门没配就不注册"的先例 | `buildEvalGate` → `errEvalGateNotConfigured` → G3 不注册 | `eval_gate_wiring.go:80,84` + `bootstrap_steps.go:311-341` | E2 照此模式改 G2 |
| 三态配置开关的先例 | `MemoryConfig.DistillationEnabled()`（`*bool` + 访问器） | `ares_config/config.go:490-492` | E2 照此给 rollback 加 |
| 可选接口升级的先例 | `populationInspector`（在 `populationSizer` 之上扩展，type switch） | `ares_evolution/scheduler.go:533-556` | E5 照此扩展 `StrategySource` |
| 回调注入而不反向依赖 | `Scheduler.WithRecoveryHint`（B1 新增） | `kernelscheduler/scheduler.go:127-166` | E1 照此注入 strategy 源 |
| 决策证据写入 | `writeDecisionEvidence`（`Source:"lifecycle"`） | `lifecycle.go:921-945` | **零改动**，E3 只补消费方 |
| 知识图谱 provider | `EvolutionProvider.Stream` | `knowledge/provider/evolution/provider.go:67` | E3 加一个分支 |
| 指标 | `PrometheusMetrics` + `RecordEvolution*` | `ares_observability/prometheus.go:152-178` | E6 加 3 个 |

**真正新建的只有**：E1 的一个 scheduler option、E2 的一个 gate 构造判定、
E3 的一个 provider 分支、E5 的流量分配器。

---

## 3. 验收基线（每项完成后重跑）

```bash
gofmt -l .                                   # 无输出
go build ./... && go vet ./...
golangci-lint run --timeout=15m              # 0 issues
go test -count=1 ./...
go test -race -count=1 ./...
go test -race -count=1 -coverprofile=cover.out ./...   # B1 新增的硬门槛
make gate                                    # G1/G2/G3 + G4 closure
git diff --check
```

**额外要求**：E1/E2 触及 checkpoint schema 与晋升语义，必须
`go test -race -count=5 ./internal/taskfabric/ ./internal/ares_evolution/ ./internal/ares_bootstrap/ ./cmd/ares/`。

**禁止**：`git commit` / `git push`（§0.1）。

---

## E1（P0）`strategy_id` 任务级归因标记

### 问题证据

`observer.go:236-246` 已经在优先读事件里的 `strategy_id`：

```go
strategyID := "unknown"
if o.activeID != nil {
    if id := o.activeID(); id != "" { strategyID = id }   // 回退：当前 active
}
if evt.Payload != nil {
    if id, ok := evt.Payload["strategy_id"].(string); ok && id != "" {
        strategyID = id                                    // 优先：事件携带
    }
}
```

但全仓 grep `"strategy_id"` 的写入点，**任务生命周期事件里没有它**：
- `taskfabric/fabric.go:658-671` `recordLocked` 构造的 payload 只有 `restoreKey*` 那批
- `agents/sub/agent.go:421,585` 的 `emitEvent` payload 有 task/result/tenant/used_experience_id，无 strategy_id

于是回退分支恒生效：**每个样本都被归因到"落库那一刻的 active"**。

### 为什么这是 P0

1. **per-task A/B 在物理上不可能**。不管怎么分流量，候选跑出的样本都会记到 active 头上。
2. **更要紧**：现在的 rollback 归因本身就是近似的 —— 策略 A 期间产生、promote 到 B
   之后才落库的样本会被算到 B。今天因为没人 promote 所以无害；
   **E2 一放开晋升，这就变成实时的正确性 bug**。

所以顺序被硬约束：**先标归因，再放晋升**。

### 关键设计约束：必须在任务粒度粘住，不是 quantum 粒度

一个任务跨多个 quantum（yield/resume）。若每个 quantum 各自读一次
`GetActiveStrategy`，就会出现 quantum 1 用 A、quantum 2 用 B —— 样本失去意义。

**落点：`CheckpointEnvelope`**（`taskfabric/checkpoint_schema.go:27`）。
理由：这个信封已经是"跨 yield/resume + 跨进程重启存活的任务级元数据"载体，
`kernelscheduler/scheduler.go:753-756` 的注释明确说它的作用就是"keeps UserProfile alive through an
arbitrary number of yield→resume cycles"。`strategy_id` 与 `UserProfile` /
`UsedExperienceID` 是同一类东西（提交时确定、整个任务生命周期不变、需要跨恢复存活）。

### 实施步骤

**Step 1：信封加字段 + bump schema version**

`internal/taskfabric/checkpoint_schema.go`：

```go
// CurrentCheckpointSchemaVersion: 1 → 2
const CurrentCheckpointSchemaVersion = 2

type CheckpointEnvelope struct {
    SchemaVersion    int            `json:"schema_version"`
    UserProfile      any            `json:"user_profile,omitempty"`
    Payload          map[string]any `json:"payload,omitempty"`
    UsedExperienceID string         `json:"used_experience_id,omitempty"`
    StepCheckpoint   any            `json:"step_checkpoint,omitempty"`
    // StrategyID is the evolution strategy active when this task was
    // SUBMITTED. It is stamped once and never re-read, so every sample the
    // task produces is attributed to the strategy that actually chose its
    // prompt and LLM params — even when a promote happens mid-flight.
    StrategyID string `json:"strategy_id,omitempty"`
}
```

`DecodedCheckpoint` 同步加 `StrategyID string`；`DecodeCheckpoint` 三条分支
（`*CheckpointEnvelope` / `map[string]any` / raw）各补一处提取；`EncodeCheckpoint` 补透传。

**v1 → v2 兼容性**：v2 只是**新增可选字段**，v1 信封在 v2 代码下解出
`StrategyID == ""`（等价于"未标记"，回退到 `activeID()`）。
`DecodeCheckpoint` 的版本检查是 `env.SchemaVersion > CurrentCheckpointSchemaVersion`
才拒绝（`:87`），所以读 v1 天然兼容，**无需迁移代码**。
反向（v1 代码读 v2 信封）会被拒 —— 这是既有设计意图，滚回旧版本前需清空未完成任务，
必须写进 CHANGELOG 的 Breaking-ish 注记。

**Step 2：`recordLocked` 把它放进事件 payload**

`internal/taskfabric/restore.go` 加常量：

```go
restoreKeyStrategyID = "strategy_id"
```

`internal/taskfabric/fabric.go:658` 的 payload 构造里加：

```go
// 归因键随每个持久化事件走，理由同 epoch（见上方注释）：观测型事件
// task.acquired/completed 才是 RuntimeObserver 订阅的那批，只放
// must-persist 事件上等于观测端永远读不到。
if sid := strategyIDFromCheckpoint(t.Checkpoint); sid != "" {
    payload[restoreKeyStrategyID] = sid
}
```

`strategyIDFromCheckpoint` 是本文件内的小 helper：`DecodeCheckpoint` 后取字段，
解码失败返回 ""（不影响状态机 —— 归因缺失只降级为 `activeID()` 回退）。

**注意**：`recordLocked` 在 `f.mu` 内运行且**不做 I/O**（`:645-650` 注释明确要求），
`DecodeCheckpoint` 是纯内存操作，符合该约束。

`foldRestoreEvent`（`restore.go:148+`）补一处回填，让重启后的任务保住归因。

**Step 3：提交时打标**

三个提交入口都要打，且都通过同一个 helper：

| 入口 | 位置 | 现状 |
|---|---|---|
| kernel bridge | `cmd/ares/kernel_bridge.go:104` `submitFabricTask` | `Create` 时不带 checkpoint |
| create_task syscall | `internal/agentsyscall/` | 需确认打标点 |
| create_plan / PlanLoop | `taskfabric/plan_loop.go`（每轮 CompilePlan） | 每轮任务都要打，且**同一轮内一致** |

打标源用**回调注入**，不让 `taskfabric` 反向依赖 evolution
（照 B1 `WithRecoveryHint` 的模式，也照 `agents.StrategySource` 定义在消费方的先例）：

```go
// taskfabric
// WithStrategyStamp wires the submission-time attribution source. The fabric
// calls it once per Create to stamp the checkpoint envelope. It must be
// cheap and non-blocking (it runs on the submission path); returning "" means
// "no strategy deployed", which downstream reads as the activeID fallback.
func (f *Fabric) WithStrategyStamp(fn func() string) *Fabric
```

生产接线在 `cmd/ares/peer_mode.go` —— 那里已经持有 `strategySrc agents.StrategySource`
（`:78`），包一层即可：

```go
fabric.WithStrategyStamp(func() string {
    st, err := strategySrc.GetActiveStrategy(ctx)
    if err != nil || st == nil { return "" }
    return st.ID
})
```

**Step 4：`sub/agent.go` 的 emitEvent 也带上**

`agents/sub/agent.go:421,585` 那两处 `EventTaskCompleted` 是 agent 自己发的
（与 fabric 的 `recordLocked` 是两条并行的事件源）。它们已经有
`task.UsedExperienceID`，说明 `models.Task` 能携带任务级元数据 ——
同样从 checkpoint 解出的 `StrategyID` 塞进 `models.Task`（由
`ToModelTask`，`kernelscheduler/scheduler.go:1001` 负责），再加进 payload。

新增 `ares_events` 常量（与既有 `EventKey*` 同列）：

```go
// EventKeyStrategyID carries the evolution strategy that was active when the
// task was submitted. RuntimeObserver attributes fitness samples by it, so a
// promote mid-flight cannot mis-credit the new strategy.
EventKeyStrategyID = "strategy_id"
```

**G3 事件契约门禁不受影响**：这是加 payload 键，不是加事件类型。

### 验收标准

- [ ] `CurrentCheckpointSchemaVersion == 2`；`CheckpointEnvelope.StrategyID` 存在
- [ ] **v1 兼容性测试**：手工构造 v1 信封（无 `strategy_id`）→ `DecodeCheckpoint`
      成功且 `StrategyID == ""`，不报 `ErrCheckpointSchemaVersion`
- [ ] **粘性测试**（核心）：任务 quantum 1 打标 A → 中途 `asm.Deploy(B)` →
      quantum 2 完成 → 断言 `task.completed` 事件的 `strategy_id` **仍是 A**
- [ ] **跨重启测试**：打标 → `RestoreFromStore` → 断言归因仍在
      （扩展既有 `taskfabric/restore_test.go`）
- [ ] **归因隔离测试**：策略 A 与 B 各产生 N 个样本 → 断言
      `Window(ctx, "A").Count == N` 且 `Window(ctx, "B").Count == N`（互不污染）
- [ ] 未接 stamp 时（leader/SDK 路径）行为回归：`strategy_id` 缺失，
      `eventToSample` 回退到 `activeID()`，与今天完全一致
- [ ] `go test -race -count=5 ./internal/taskfabric/ ./internal/ares_evolution/` 绿
- [ ] `make gate` 绿（G3 事件契约不受影响，因为只加 payload 键）
- [ ] CHANGELOG 记 schema v1→v2：新版可读旧信封，**旧版不可读新信封**

---

## E2（P0）G2：从 fail-closed 改为"诚实缺席 + 安全不变量"

### 问题证据

```go
// lifecycle.go:364-370
if report == nil || report.TotalComparisons == 0 {
    return false, 0, "no shadow comparisons recorded — fail-closed (no independent scorer wired)"
}
```

而默认配置下 `TotalComparisons` 恒为 0：

```go
// shadow_sampler.go:119-124
if !s.evaluator.HasIndependentScorer() {
    return   // 不伪造证据 —— 这个判断本身是对的
}
```

`HasIndependentScorer` 为假的原因链：`llm_scoring.enabled` 默认 false
→ `wireLLMScorer` 返回 nil（`bootstrap_steps.go:658-661`）
→ `gaCfg.Scorer == nil` → `buildShadowEvaluator` 不设 scorer（`genome_wiring_system.go:676-692`）。

于是：seed 候选走一次性例外无条件 promote（`lifecycle.go:462-477`），
**之后每一个候选都被 G2 拒**。且因为没有第二次 promote，
`asm.Previous()` 永远是 nil，**自动回退也永久不可用**
（`lifecycle.go:751-763` 那条 "expected in the fail-closed default config" 的日志
正是这个状态的自白）。

### 为什么不是退回 rubber stamp

两者的区别是**是否假装验证过**：

| | 行为 | 性质 |
|---|---|---|
| 旧的 rubber stamp（已被否决） | G2 已注册，无证据却 `return true` | **假实现**（§0.2）—— 门在，报 pass，但什么都没验 |
| fail-closed（现状） | G2 已注册，无证据则 `return false` | 诚实但把系统锁死 |
| **诚实缺席**（本方案） | **G2 不注册**，快照与日志明确报告"事前门缺席，由事后回退兜底" | 显式的架构选择，可观测、可审计 |

**这个模式在同一个代码库里已有先例**：`buildEvalGate`（`eval_gate_wiring.go:84`）在没配 `eval_suite` 时返回
`errEvalGateNotConfigured`（`eval_gate_wiring.go:80`），G3 就不注册，`bootstrap_steps.go:311-315` 的注释原话是
"otherwise NO gate is registered — honest absence, not a permanent pass-through
pretending to be verification"。**G2 应当服从同一条规则。**

### 安全不变量（这是本项的核心，不是附注）

> **允许跳过事前验证，当且仅当事后验证已武装。**

形式化：

| 独立 scorer | rollback 启用 | G2 | 理由 |
|---|---|---|---|
| 有 | — | **注册**（真验证） | 有证据源，事前门是真的 |
| 无 | **是** | **不注册** | canary + 自动回退接管；晋升是可撤销的 |
| 无 | **否** | **注册且 fail-closed** | 两端都无验证，拒绝是唯一正确答案 |

第三行是关键：它保证"没有门"永远不是一个可以静默落入的状态。

### 实施步骤

**Step 1：rollback 开关三态化**

现状 `bootstrap_steps.go:221-226` 把 `Enabled` 硬编码为 `true`，
YAML 无法关闭 —— 因此上表第三行今天不可达。改为三态（照
`MemoryConfig.DistillationEnabled()` 的先例，`config.go:490-492`）：

```go
// ares_config
type EvolutionRollbackConfig struct {
    // Enabled arms automatic rollback. nil (absent) = true: an operator who
    // does not mention rollback gets it, because it is the safety net the
    // promote path relies on. Explicit false disables it AND re-arms the G2
    // fail-closed gate — see buildShadowGate.
    Enabled *bool `yaml:"enabled"`
    ...
}
func (c EvolutionRollbackConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }
```

**Step 2：G2 构造判定**

新增 `internal/ares_bootstrap/shadow_gate_wiring.go`（与 `eval_gate_wiring.go` 对称）：

```go
// errShadowGateNotConfigured mirrors errEvalGateNotConfigured: an absent gate
// is reported, never silently substituted with a pass-through.
var errShadowGateNotConfigured = errors.New("bootstrap: shadow gate not configured")

// shadowGateMode decides whether the G2 shadow gate is registered.
//
// The invariant: skipping PRE-deployment verification is allowed only when
// POST-deployment verification is armed. Concretely — no independent scorer
// means no shadow evidence can exist, so a registered G2 would reject every
// candidate forever (measured: only the seed strategy ever promotes, and
// asm.Previous() stays nil so automatic rollback is ALSO unreachable). In that
// state the honest options are (a) let canary + rollback carry the risk, or
// (b) refuse to promote at all. Which one applies depends on whether rollback
// is actually armed — never on a default we picked for the operator.
func shadowGateMode(hasScorer, rollbackArmed bool) (register bool, reason string)
```

三分支返回：
- `hasScorer` → `(true, "independent scorer wired")`
- `!hasScorer && rollbackArmed` → `(false, "no scorer; canary+rollback armed")`
- `!hasScorer && !rollbackArmed` → `(true, "no scorer and rollback disarmed — fail-closed")`

**Step 3：`NewStrategyLifecycle` 接受"不注册 G2"**

现状 `lifecycle.go:319-321` 只要 `l.shadow != nil` 就 prepend G2。
改为由一个显式 option 控制，默认保持现行为（向后兼容测试）：

```go
// WithShadowGateDisabled suppresses the automatic G2 registration for the
// documented no-scorer-plus-armed-rollback case. It is deliberately explicit:
// the gate's absence must be a decision at the wiring layer, never an
// emergent property of nil-checking.
func WithShadowGateDisabled(reason string) LifecycleOption
```

`reason` 存进 lifecycle，由快照与启动日志报告 —— **缺席必须可见**。

**Step 4：启动可见性（不可省）**

跳过 G2 时，bootstrap 必须 warn（照 `serve_routine.go:196-199` 对
`0.0.0.0`+无鉴权的告警口径）：

```
WARN evolution: pre-deployment shadow gate NOT registered
     reason="no independent scorer wired (llm_scoring disabled)"
     mitigation="candidates promote directly; degradation triggers automatic rollback"
     rollback_armed=true rollback_threshold=0.15 rollback_window=5
```

同时 `LifecycleSnapshot()`（`lifecycle.go:869`）补字段：`gates`（已注册门名列表）、
`shadow_gate_skipped_reason`。运维在 `/api/evolution/lifecycle` 一眼能看出当前是哪种模式。

**Step 5：晋升节流（防抖动）**

放开晋升后，GA ticker 每 `min_interval`（默认 5m）一代，每代都会 Submit 最优候选。
无节流会导致"每 5 分钟换一次策略"，而 rollback 窗口（`window_size: 5`，
`min_samples: 3`）根本来不及积累证据 —— **策略换得比证据攒得快，
rollback 永远判不出退化**。

因此必须加最小驻留期：

```go
// LifecycleConfig
// MinActiveDuration is how long a promoted strategy must stay active before
// another candidate may replace it. Without it the GA ticker could rotate
// strategies faster than the rollback window accumulates evidence, making
// degradation undetectable in principle. Default: 3 × watch_interval, so at
// least three windows are observed. Zero falls back to the default.
MinActiveDuration time.Duration `json:"min_active_duration"`
```

`Submit` 在门链之前检查：未满驻留期则拒绝并记
`gate_reject_total{gate="min_active_duration"}`。

这一条**不是可选优化，是 E2 的正确性前提**。

### 验收标准

- [ ] `shadowGateMode` 三分支表驱动单测，每个分支断言 `register` 与 `reason`
- [ ] **不变量测试**：`!hasScorer && !rollbackArmed` → G2 **仍然注册且 fail-closed**
      （这是安全底线，必须有独立用例）
- [ ] **闭环测试（本项的主验收）**：默认配置 + `evolution.enabled: true` →
      提交两个候选 → 断言**第二个候选也被 promote**（今天被拒），
      且 `asm.Previous()` 指向第一个 —— 即"第二次 promote"真的发生
- [ ] **回退可达性测试**：接上面，再灌入连续失败事件 →
      断言 `Rollback` 触发且 `GetActive` 回到前一个 ID。
      这条是全计划最有价值的断言：它证明**出口那套机器第一次真的转了**
- [ ] **驻留期测试**：驻留期未满时的 Submit 被拒，且
      `ARES_evolution_gate_reject_total{gate="min_active_duration"}` 递增
- [ ] **抖动回归测试**：模拟 ticker 每 tick 提交一个更优候选，
      断言单位时间内 promote 次数受驻留期约束（不是每 tick 都换）
- [ ] `rollback.enabled: false` 的 YAML 路径可生效（今天硬编码 true）
- [ ] 启动日志包含 skip 告警 + mitigation + rollback 参数
- [ ] `LifecycleSnapshot()` 含 `gates` 与 `shadow_gate_skipped_reason`
- [ ] **closure 断言 1-3 不回退**：它们现在手动灌 `se.RecordResult(1.0, 0.0)`
      模拟"有证据"的路径，属于 `hasScorer` 分支，**行为不变**
- [ ] `make gate` 全绿（含 `-tags closure`）
- [ ] `configs/ares.yaml` 的 shadow 段落注释重写 —— 现在那段（`:142-164`）
      详细描述了 fail-closed 行为，改完必须同步，否则文档立刻变成错的

---

## E3（P1）P2-3：决策证据接上知识图谱消费方

### 问题证据

`lifecycle.go:921-945` `writeDecisionEvidence` 在 promote（`:666`）与 rollback（`:801`）
时写入证据，`Source: "lifecycle"`（`:940`）。`:798-800` 的注释声称：

> "the knowledge graph's EvolutionProvider ... can consume the decision trail"

**实测：`internal/knowledge/**` 里没有任何消费 `Source == "lifecycle"` 的代码。**
决策轨迹写进去就停在那里。

而 `EvolutionProvider.Stream`（`provider.go:67-137`）现在只读 `StrategyStore` 的
`GetActive` + `GetHistory`，产出 `ObjectDecision`（`adapter/evolution.go:32-51`）。
也就是说：**血缘（parent_id 链）有，决策理由没有**。

### 为什么接而不是删

血缘回答"这个策略从哪来"，决策证据回答"**为什么当时选它/弃它**" ——
后者才是知识图谱里真正有检索价值的部分（Agent 问"上次为什么回退了"时，
血缘给不出答案）。而且写入侧已经建好了，删掉是净损失。

### 实施步骤

`internal/knowledge/provider/evolution/provider.go` 的 `Stream` 加第三段
（现有两段：active、history）：

```go
// Emit the promote/rollback decision trail. The strategy store carries
// LINEAGE (which strategy came from which); this carries the REASONING
// (why it was promoted or rolled back, and at what score). An agent asking
// "why did we roll back last time" can only be answered by the latter.
//
// Source is the discriminator, NOT Kind: writeDecisionEvidence currently
// writes Kind=KindFitness (lifecycle.go:941), the same Kind the fitness
// samples use, so filtering by Kind alone would pull in every runtime
// sample. See the Kind decision below.
if p.evStore != nil && limit > 0 {
    evs, err := p.evStore.Query(gCtx, evidence.Filter{
        Source: "lifecycle",
        Kind:   evidence.KindFitness,
        Limit:  limit,
    })
    ...
}
```

需要的配套：
1. `EvolutionProvider` 加可选 `evStore evidence.Store` 字段 + `WithEvidenceStore` option
   （零值 = 不发决策对象，保持向后兼容）。
2. `adapter/evolution.go` 加 `FromDecisionEvidence(ev evidence.Evidence, ns string) *KnowledgeObject`
   —— 与 `FromStrategy` 对称，`Type: knowledge.ObjectDecision`，
   `Tags: ["evolution","decision", action]`，Metadata 带 `action`/`strategy_id`/`score`/`reason`。
   payload 无 `action` 字段的记录（即普通 fitness 样本）返回 nil —— 这是第二道过滤。
3. `attachEvolutionKnowledgeProvider`（`bootstrap_steps.go:705`）传入 `newEvol.EvidenceStore`。

**注意时序**：该函数在 `bootstrap_steps.go:201` 调用，那时 `StrategyStore` 刚建好、
lifecycle 还不存在。但它需要的是 **EvidenceStore**（`comp.EvidenceStore`，
更早就位），不是 lifecycle —— 所以时序没有问题，不需要挪动调用点。

### 一个需要顺手决定的事：Kind 语义

实测 `writeDecisionEvidence`（`lifecycle.go:941`）写的是 `Kind: evidence.KindFitness`,
与 `RuntimeObserver.writeEvidence`（`observer.go:283`）**完全相同的 Kind**。
两者只靠 `Source` 区分（`"lifecycle"` vs `"strategy"`）。

`evidence.go:28-33` 现有 6 个 Kind，**没有 `KindDecision`**。

两个选项：

| 选项 | 做法 | 代价 |
|---|---|---|
| **(a) 只按 Source 过滤**（推荐 0.3.1） | 保持 `KindFitness`，provider 按 `Source=="lifecycle"` + payload 有 `action` 双重过滤 | 零 schema 变更；但"决策"和"样本"共享 Kind，语义混淆 |
| (b) 新增 `KindDecision` | 加枚举值，改 `writeDecisionEvidence` | 需检查 `Window`/`querySourceMean` 是否会因 Kind 变化漏掉记录 —— **`querySourceMean` 传的是 `KindFitness`（`:225`），改 Kind 后决策证据会从 fitness 窗口消失**，这其实是好事（决策不该被当成 fitness 样本喂给 rollback），但要确认没有其他消费方 |

**我倾向 (b)，但建议在 0.3.1 先做 (a)**：(b) 会改变 `Window` 的输入集合
（决策记录目前**正在被当作 fitness 样本参与 rollback 计算** —— 这本身可能是个 bug，
`Source: "lifecycle"` 不在 `Window` 的 `sources` 表里（`fitness_aggregator.go:205-209`
只有 strategy/workflow/scheduler/recovery），所以实际上没被算进去）。
确认这一点后再决定，不要在 E3 里顺手改 Kind。

**行动项**：E3 用 (a) 实现；同时登记一条 0.4.x 项 ——
"决策证据与 fitness 样本共用 `KindFitness`，应分离为 `KindDecision`，
分离前需确认 `Window` 的 sources 表不会受影响"。

### 验收标准

- [ ] `FromDecisionEvidence` 表驱动单测：promote / rollback / **payload 无 `action`
      字段（普通 fitness 样本）返回 nil** / 畸形 JSON 返回 nil 不 panic
- [ ] Provider 测试：写入 2 条决策 + 3 条普通 fitness 样本 → `Stream` 只产出
      2 个 `ObjectDecision`（验证双重过滤真的生效）
- [ ] `evStore == nil` 时 `Stream` 行为与今天完全一致（向后兼容）
- [ ] `limit` 在三段之间正确分配，不溢出 `intent.Scope.MaxObjects`
- [ ] 端到端：promote → rollback → 断言知识图谱可检索到两条决策对象
- [ ] 登记 0.4.x 项：`KindDecision` 分离 + `Window` sources 表影响确认
- [ ] `lifecycle.go:800-802` 那句 "the knowledge graph's EvolutionProvider ... can consume" 注释更新为指向真实消费方 file:line
      —— 它现在描述的是一个不存在的链路
- [ ] `go test -race ./internal/knowledge/... ./internal/ares_evolution/` 绿

---

## E4（P1）G1 定性修正 + G4 不对称登记（纯文档）

### 判断：G1 本质上不属于候选级门链

`ga-runtime-evolution-design-zh.md` §4④ 把 G1-G4 描述为四道串联的候选验证门。
但 G1（`EvolutionGuardrails`）回答的问题是：

- 未评估个体是否过半（`guardrails.go:207-231`）
- 是否连续 N 代无改进（`:234-254`）
- 血缘是否过度集中（PostEvolve）

这些全是**"这一代种群健康到可以进化吗"** —— 整个 cycle 的前置条件，
不是对单个候选的裁决。它的调用点也印证这点：`genome_wiring_run.go:67`（Evolve 前）
与 `:82`（Evolve 后），作用于整个种群、失败则中止整代。

**硬塞进候选门链需要给它编造一个它没有的候选级语义。** 这属于 §0.2 的假实现范畴。

### 应做的修正（改文档，不改代码）

`ga-runtime-evolution-design-zh.md` §4④ 的门链定义改为：

| 层级 | 门 | 作用域 | 位置 |
|---|---|---|---|
| **Cycle 前置** | G1 guardrail | 种群级（未评估/停滞/血缘集中） | `genome_wiring_run.go:67,82`，adapter 层。**B2 修复后真实生效** |
| **候选门链** | G2 shadow | 单候选 | `lifecycle.go` 门序列，E2 后条件注册 |
| | G3 eval suite | 单候选 | 同上，配了 `eval_suite` 才注册 |
| **部署阶段** | G4 staging | 仅携带 RuntimePatch 的候选 | Coordinator → DeploymentPipeline，**独立路径** |

这样"四门串联"这个不成立的说法直接消失，而不是被伪造出来。

### 顺带登记一个真实缺口：G4 的不对称

G4（deployment staging）只在候选携带 RuntimePatch 时才走
Coordinator → DeploymentPipeline；**普通策略候选（只改 prompt/params）
完全不经过 staging**。

这个不对称在设计文档里没说，大概率不是有意的 —— 一个改 prompt 的策略同样可能
让线上变差，凭什么不过 staging？但改动它属于架构方向调整，本期只登记：

> **登记（0.4.x 评估）**：G4 staging 当前只覆盖 RuntimePatch 候选。
> 策略候选（prompt/params 变更）绕过 staging 直达 ACTIVE。
> E2 之后这些候选由 canary + rollback 兜底，风险可控但不对称。
> 需决定：(a) 策略候选也走 staging，(b) 明确 staging 只管 patch 并写进文档。

### 验收标准

- [ ] `ga-runtime-evolution-design-zh.md` §4④ 门链定义改为三层表述，
      G1 明确为 cycle precondition
- [ ] §5 已知缺口 1 更新：B2 已修复"guardrails 从未构造"，
      但保留"G1 不在候选门链内"的定性说明（这是设计选择，不是缺口）
- [ ] 新增"G4 不对称"登记项，标注 0.4.x 评估 + 两个候选方案
- [ ] `ares-repair-plan-zh.md` §14.3 的 "G1 仍在 adapter 层" 一条改为
      "**按设计**在 adapter 层"，并交叉引用本项
- [ ] 全文搜索"四道门"/"G1-G4 串联"类表述，逐处修正
      —— 不改这些，文档会继续教后人去做一件不该做的事

---

## E6（P2）观测面

### 现有指标

`prometheus.go:152-178` 已有四个（`ARES_` 前缀，**注意与设计文档的 `ares_` 字面量不一致**，
已在 GA 阻塞项 §B7.4 登记）：
`promote_total{result}` / `rollback_total{reason}` / `gate_reject_total{gate}` / `shadow_win_rate`。

### 新增（复用 `RecordEvolution*` 模式）

| 指标 | 标签 | 用途 |
|---|---|---|
| `ARES_evolution_gate_skipped_total` | `gate`, `reason` | E2 的缺席必须可计量，否则"门没注册"只存在于启动日志里 |
| `ARES_evolution_active_duration_seconds` | — | Gauge：当前 active 已驻留时长。配合驻留期看是否抖动 |
| `ARES_evolution_window_samples` | `strategy_id`, `source` | Gauge：证据窗口样本数。E1 之后可按策略拆开看归因是否生效 |

第三个是**验证 E1 是否真的生效的运行时手段**：如果归因没打上，
所有样本会堆在同一个 `strategy_id` 标签值上，一眼可见。

### 快照补字段

`LifecycleSnapshot()`（`lifecycle.go:869`）补：
`gates`（已注册门名列表）、`shadow_gate_skipped_reason`、
`active_since`、`min_active_duration`、`rollback_armed`。

### 验收标准

- [ ] 三个新指标声明 + 注册 + 在真实代码路径递增（不是注册完不用）
- [ ] `LifecycleSnapshot()` 新字段有测试
- [ ] `GET /api/evolution/lifecycle` 返回体含新字段（沿用 T7 读鉴权，不开后门）
- [ ] `/metrics` 抓取测试断言三个新指标出现

---

## E5（0.4.x）per-task 真 A/B 采样

> **章节顺序说明**：E5 编号在 E6 之前，但排在 E6 之后 —— 因为 E1/E2/E3/E4/E6
> 属于 0.3.1，E5 属于 0.4.x。按阶段而非编号排列，避免读者以为 0.3.1 要做 E5。

> **本项不属于 0.3.1。** 它是特性开发，不是缺陷修复，且硬依赖 E1。
> 放在这里是为了让 0.3.1 的取舍有明确的下一站，而不是留一个开放式 TODO。

### 核心认识：per-task A/B 与"Submit 内联裁决"架构不兼容

现在的流程是同步的：

```
Submit → Prime（灌 N 个对比）→ 内联跑门 → 当场裁决 promote/reject
```

而 per-task A/B 的证据**必须来自候选真实执行过的任务**。候选在 Submit 时刻
样本数必然为 0 —— 它还没跑过任何东西。所以裁决必须变成异步：

```
Submit → 候选进 SHADOW，分到一份流量份额
       → 样本随真实任务累积（分钟~小时级）
       → watch loop 周期性重评 → promote / reject
```

**好消息一**：`StrategyLifecycle` 已经有 watch loop（`lifecycle.go:677`，30s tick）。
这不是新建机制，是把 G2 的判定**从 Submit 内联移到 watch loop**。

**好消息二**：判定所需的数据设施也都在了。
`RuntimeFitnessAggregator.Window(ctx, strategyID)`（`fitness_aggregator.go:187`）
已经按 strategyID 过滤，`sources[0] = {"strategy", cfg.Weights.Outcome, strategyID}`
（`:205`）就是策略维度，`querySourceMean` 的 `strategyID != "" && fe.StrategyID != strategyID`
（`:331`）就是过滤逻辑。

所以**新 G2 = `Window(candidate.ID)` vs `Window(active.ID)`**，
两侧都要求 ≥ `MinSamplesBeforeJudge`。这是真正独立的证据（来自不同的真实任务），
且**完全复用现有设施**。

### 实施拆分

**Step 1：流量分配器 + arm 粘性（1-2 天）**

在 `agents.StrategySource` 之上做**可选接口扩展**（照 `populationInspector` 的
type-switch 模式，`ares_evolution/scheduler.go:533-556`）：

```go
// agents
// ABStrategySource is the optional extension a StrategySource may implement to
// participate in candidate/active A/B sampling. Sources that do not implement
// it keep the single-strategy behaviour unchanged.
type ABStrategySource interface {
    StrategySource
    // PickStrategy returns the strategy to use for ONE task, plus the arm
    // label ("active"/"candidate") for attribution. Callers must stamp the
    // returned ID on the task and never re-pick mid-task.
    PickStrategy(ctx context.Context, taskID string) (*ActiveStrategy, string, error)
}
```

分流比例走 YAML（`evolution.shadow.candidate_traffic`，默认 0.1）。
分流决策**必须由 taskID 确定性哈希**，不能用随机数 —— 否则同一任务的
不同 quantum 可能落到不同 arm。E1 的 checkpoint 打标使 arm 天然粘住，
但哈希分流让"同一任务重启后仍在同一 arm"也成立。

**Step 2：G2 判定移到 watch loop（1-2 天）**

- `Submit`：候选进 SHADOW，记录 `shadowSince`，**不裁决**
- `watch` tick：对 SHADOW 中的候选，比 `Window(cand)` 与 `Window(active)`
- 三个退出条件：
  - 两侧样本 ≥ MinSamples 且候选胜 → promote
  - 两侧样本 ≥ MinSamples 且候选负 → reject + 进黑名单
  - 超时（`shadow_max_duration`，默认 1h）仍不够样本 → reject（保守）+ 记
    `gate_reject_total{gate="shadow_timeout"}`
- **黑名单复用现有的**（`lifecycle.go:496-509` 剪枝 + `:779-782` 写入，banUntil = gen + N）

**Step 3：closure 断言 1-3 重写（1 天）**

现在这三条**手动灌** shadow 证据：

```go
se.StartShadow(cand); se.RecordResult(1.0, 0.0)   // closure_feedback_loop_test.go:286-289,314-317,346-349
```

E5 之后必须改为驱动真实 A/B：发若干任务 → 部分走候选 arm → watch tick → 断言裁决。
**这一步不能省**：不改的话这三条断言会继续验证一条已经不存在的路径。

**Step 4：`ShadowSampler` 退役或降级（0.5 天）**

两个选项：
- (a) 删除 —— per-task A/B 完全取代它
- (b) 保留为"流量不足时的 fallback"（低流量部署可能永远攒不够样本）

倾向 (a)。若选 (b)，必须明确标注它产出的**不是独立证据**
（`shadow_sampler.go:37-45` 已经写清楚了这一点），且在快照里区分证据来源。

### 验收标准（0.4.x）

- [ ] `ABStrategySource` 可选接口 + type-switch 降级（未实现者行为不变）
- [ ] 分流确定性测试：同一 taskID 多次 `PickStrategy` 返回同一 arm
- [ ] arm 粘性测试：跨 quantum、跨重启 arm 不变（依赖 E1）
- [ ] 分流比例测试：1000 个 taskID 的候选占比在 `candidate_traffic ± 5%` 内
- [ ] **真 A/B 晋升测试**：候选 arm 任务成功率显著高于 active → watch tick → promote
- [ ] **真 A/B 拒绝测试**：候选 arm 显著更差 → reject + 进黑名单 + 黑名单期内
      重复提交被拒
- [ ] 超时测试：样本不足且超过 `shadow_max_duration` → 保守 reject
- [ ] closure 断言 1-3 已改为真实 A/B 驱动，不再手动灌证据
- [ ] `MinSamples` 恢复统计意义：断言 N 个对比来自 N 个**不同任务**
      （即 `shadow_sampler.go:142-147` 那条 TODO 可以删除）

---

## 4. 阶段划分与发布节奏

### Phase A：0.3.1（"enabled: true 真的会转"）

```
E4（文档，零风险，可立即做）
  ↓
E1（归因，硬前置）
  ↓
E2（放开晋升 + 安全不变量 + 驻留期）
  ↓
E3（决策证据消费方） ∥ E6（观测面）
  ↓
全量验收基线 + closure 套件
```

估算 4.3 人日。完成后的状态：
- `evolution.enabled: true` → 候选持续晋升，退化自动回退
- 事前门在有 scorer 时是真验证，无 scorer 时诚实缺席且**可见**
- 两端都无验证时**拒绝晋升**（安全底线）
- 决策轨迹进知识图谱，可被 Agent 检索

### Phase B：0.4.x（"验证是真的"）

E5，4-6 人日。完成后 `MinSamples` 恢复统计意义。

### 不做的事（明确砍掉）

**不用确定性 scorer 当门。** `DeterministicScore`（`service/llm_scorer.go:101-131`）
打的是参数形状分：temperature 低加分、top_k 靠近 30 加分、prompt 长度加分。
它是**参数偏好的代理，不是性能度量**。用它当门会以任意理由拒掉好候选，
同时给人"有验证"的错觉 —— 比诚实缺席更糟。

**不把 `llm_scoring` 设为默认开。** 引入不可控 LLM 成本；且设了 seed 之后
温度归零，又退化成确定性重复（`bootstrap_steps.go:287-289` 已识别并设
`DeterministicScorer=true`）。它适合作为可选增强，不适合作为让环转起来的手段。

**不在 0.3.1 动 G4 的不对称。** 只登记（E4）。

---

## 5. 风险与缓解

| 风险 | 后果 | 缓解 |
|---|---|---|
| E2 放开晋升后策略抖动 | 换得比证据攒得快，rollback 判不出退化 | `MinActiveDuration`（默认 3×watch_interval）+ 抖动回归测试。**这是 E2 的正确性前提，不是可选优化** |
| E1 schema v1→v2 单向兼容 | 滚回旧版本无法读新信封 | 新版可读旧信封（只加可选字段）；反向不可 —— 写进 CHANGELOG，滚回前需清空未完成任务 |
| `recordLocked` 里解 checkpoint | 该函数在 `f.mu` 内且约定不做 I/O | `DecodeCheckpoint` 是纯内存操作；解码失败静默降级为 `activeID()` 回退，不影响状态机 |
| 无 scorer + rollback 关闭 | 无任何验证却在晋升 | 安全不变量第三行：此状态下 G2 **仍 fail-closed**。有独立测试 |
| 低流量部署攒不够 A/B 样本 | 候选永远超时被拒 | E5 Step 4 选项 (b) 保留 sampler 作 fallback；或 `shadow_max_duration` 可配 |
| `enabled: true` 语义变更 | 已有用户的行为改变 | 属 §0.4 方向变更，**需用户先认可**；CHANGELOG 记为 Changed（非 Breaking，因默认仍是 false） |
| 归因回退掩盖 E1 失效 | 打标坏了但不报错，静默回退到旧行为 | E6 的 `window_samples{strategy_id}` 指标：打标失效时所有样本堆在同一标签值，可见 |

---

## 6. 全局验收（Phase A 完成的判据）

一句话版本：**在默认配置 + `evolution.enabled: true` 下，
第二个候选能被晋升，退化能被自动回退，且这两件事都能在
`/api/evolution/lifecycle` 与 `/metrics` 上看到。**

清单：

- [ ] `evolution.enabled: true`（其余默认）→ 提交两个候选 → 第二个也 promote
- [ ] 接上，灌连续失败 → `Rollback` 触发 → `GetActive` 回到前一个 ID
      （**出口那套机器第一次真的转了**）
- [ ] `rollback.enabled: false` → G2 恢复 fail-closed → 只有 seed 晋升
      （安全底线可达）
- [ ] `llm_scoring.enabled: true` → G2 注册为真验证门 → 行为与今天一致（不回退）
- [ ] 归因隔离：`Window("A")` 与 `Window("B")` 样本互不污染
- [ ] 归因粘性：promote 发生在任务执行中途，该任务样本仍归旧策略
- [ ] promote/rollback 决策可在知识图谱检索到
- [ ] 启动日志明确报告当前门配置与 mitigation
- [ ] `/api/evolution/lifecycle` 含 gates / skip reason / active_since / rollback_armed
- [ ] `/metrics` 含三个新指标且真实递增
- [ ] 全量基线绿（含 `-race -coverprofile` 与 `make gate`）
- [ ] `-race -count=5` 绿：taskfabric / ares_evolution / ares_bootstrap / cmd/ares
- [ ] `configs/ares.yaml` 的 shadow 段落已重写（现注释描述 fail-closed 行为，改完必须同步）
- [ ] `ga-runtime-evolution-design-zh.md` §4④/§5 已按 E4 修正
- [ ] CHANGELOG 记：checkpoint schema v2、`enabled:true` 语义变更、
      三个新指标、`rollback.enabled` 新开关

**提交纪律**：不自动提交（§0.1）。每完成一项跑 §3 基线，结果贴回对应勾选项。

---

## 7. 与其他计划的关系

| 计划 | 关系 |
|---|---|
| `ga-runtime-evolution-design-zh.md` | 设计源。本计划的 E4 修正其 §4④ 门链定性；E5 落实其"per-task A/B 后续项"（§4④ 注、`shadow_sampler.go:142-147`） |
| `ares-0.3.1-ga-blockers-plan-zh.md` | B2 已修好 G1 guardrail 的空转（本计划 E4 为其补正定性）；B7.4 的 metrics 命名不一致在 E6 中一并处理 |
| `ares-0.3.1-release-readiness-plan.md` | T11 的"GA 控制平面纳入发布面"，本计划是其功能闭环部分。§0 状态表需补 E1-E6 行 |
| `ares-repair-plan-zh.md` | §14.3 的 "P2-3 无消费方" 由 E3 关闭；"G1 仍在 adapter 层" 由 E4 改判为按设计 |

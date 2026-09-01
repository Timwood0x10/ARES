# ARES 0.3.1 GA 阻塞项修复计划（GA Blockers Plan）

> 生成日期：2026-09-01
> 分支：`dev`　`VERSION`：`0.3.1`
> 依据：2026-09-01 全仓实测复核（编译 / vet / lint / 全量 test / `-race` + coverprofile / `make gate` / 4 份根计划账目核对）
> 编码规范：`plan/rules/code_rules_v2.md`（铁律 §0 第 1 条：**禁止擅自 Git 提交**）
> 关联计划：`ares-0.3.1-release-readiness-plan.md`（本计划是其 T9/T11 与新增 P0 的执行细化）

---

## 0. 实测基线（2026-09-01，本计划的事实起点）

| 检查 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | 绿 |
| 静态检查 | `go vet ./...` | 绿，0 问题 |
| Lint | `golangci-lint run --timeout=10m` | **0 issues** |
| 全量测试（×3 轮） | `go test -count=1 ./...` | 186 包全绿 |
| 门禁 | `make gate`（G1/G2/G3 + G4 closure） | 全绿 |
| **竞态+覆盖率** | `go test -race -count=1 -coverprofile ./...` | **FAIL（`cmd/ares`）** |
| 总覆盖率 | 同上 | **58.24%**（68043 语句 / 39630 覆盖），目标 65% |

**唯一红灯**：`TestE2E_GrandLoop_RealSchedulerChaosRecovery` 在 `-race -coverprofile`
下 20 轮失败 1 轮（复现率 ~5%），普通 `go test` 与单跑 `-race` 均不复现。
`-coverprofile` 的插桩开销放大了调度器里的一个真实竞态窗口，所以 CI 的
covera 步骤会周期性变红，而 `make check` 一直是绿的。

---

## 1. 任务总表

| # | 级别 | 任务 | 涉及文件 | 预估 |
|---|------|------|---------|------|
| **B1** | **P0** | 调度器 stale-winner 竞态：agent 死亡后主动触发 recovery，不再被动等 TTL | `internal/kernelscheduler/{scheduler,executor_registry}.go`、`cmd/ares/kernel_loop.go` | 0.5 人日 |
| **B2** | **P1** | bootstrap 真实构造 `gaCfg.Guardrails`，让 G1 从纸面门变成生效门 | `internal/ares_bootstrap/{bootstrap_steps,provide_evolution}.go` | 0.5 人日 |
| **B3** | **P1** | `/api/evolution/approve` 401/409/200 三态测试 | `cmd/ares/actions_evolution_test.go`（新增） | 0.3 人日 |
| **B4** | **P1** | 引入 `goleak`，覆盖 SDK `Runtime.Close()` 与 plan loop | `go.mod`、`sdk/goleak_test.go`（新增） | 0.3 人日 |
| **B5** | **P1** | 总覆盖率 58.24% → ≥65% | `internal/storage/postgres/repositories/*`、`internal/ares_memory/experienceadapters`、`internal/logger` | 1.5 人日 |
| **B6** | P2 | `compat/` 决策（补测试 / 删除）+ `examples/` 计入口径 | `compat/*`、Makefile `cover` 目标 | 0.3 人日 |
| **B7** | P2 | 账目对齐：修正 `ares-repair-plan-zh.md` 的错误引用结论，更新 release 计划状态表 | 两份 md | 0.3 人日 |

**关键路径**：B1 →（B2 ∥ B3 ∥ B4）→ B5 → B6 → B7。
B1 必须先做：它是唯一会让 CI 变红的项，其余项的验收都依赖一条稳定的绿基线。

---

## 2. 验收基线（每项完成后都必须重跑）

```bash
gofmt -l .                                   # 无输出
go build ./... && go vet ./...
golangci-lint run --timeout=10m              # 0 issues
go test -count=1 ./...
go test -race -count=1 ./...
make gate                                    # G1/G2/G3 + G4 closure
go test -race -count=1 -coverprofile=cover.out ./... \
  && go tool cover -func=cover.out | tail -1  # 总覆盖率
git diff --check                              # 无空白错误
```

**禁止项**：不得 `git commit` / `git push`（`code_rules_v2.md` §0.1）。

---

## B1（P0）调度器 stale-winner 竞态：任务卡在 LEASED

### 问题证据

```
--- FAIL: TestE2E_GrandLoop_RealSchedulerChaosRecovery (10.03s)
    e2e_grand_loop_real_test.go:279: task must complete after recovery, got LEASED
日志：kernel scheduler: winner "agent-A" for task "t1" is no longer executable
      and no capable replacement exists; task stays leased for recovery
```

复现：`go test -race -count=1 -coverprofile=/tmp/c.out -run TestE2E_GrandLoop ./cmd/ares/`
连跑 20 次，PASS=19 / FAIL=1。

### 根因

竞态窗口在 `internal/kernelscheduler/scheduler.go:650-669`（`executeWithCandidates`
的 stale-winner 分支）：

1. `drain` 从候选快照里选出 winner=agent-A，`fabric.Schedule` 成功发放 lease
   （任务进入 LEASED，epoch 已递增）。
2. 在 `Schedule` 返回和 `LookupExecutor(winner)` 之间，chaos kill 把 agent-A
   从 agentfabric 摘除 → `fabricExecutor(winner)` 返回 nil，静态注册表也没有它。
3. `HasCapableExecutor(taskID)` 此刻返回 false：replacement executor 还没被
   recovery loop 创建，而 `s.agents != nil`（peer 模式）使得 `executor_registry.go:146`
   直接 `continue` 掉所有静态注册项，fabric 里又已经没有活的 capable agent。
4. 于是走 `scheduler.go:668` 的分支——**保持 lease，把恢复责任交给 TTL 过期**。

这个"交给 TTL"的假设有一个隐含前提：时间会继续前进。它在两种情形下不成立：

- **假时钟测试**：`e2e_grand_loop_real_test.go` 用 `advance(7*time.Minute)`
  一次性推过 5 分钟 TTL。若第 3 步发生在 `advance` 之后，`CheckExpiredLeases`
  已经扫过一轮，新发放的 lease 的 `ExpiresAt = f.now() + ttl` 又落在未来，
  而测试不会再推进时钟 → 任务永久停在 LEASED。
- **生产**：任务停顿整个 lease TTL（默认 5 分钟）才被 recovery 捡起。不是死锁，
  但是一次可观测的 5 分钟空转，且日志把它描述成"预期行为"。

### 修复方案

方向按用户裁定：**不再被动等 TTL，主动触发一次 recovery 扫描**。

不能让 `kernelscheduler` 反向依赖 `cmd/ares` 的 recovery loop（架构铁律：
调度器不得 import runtime，见 `internal/kernelscheduler/architecture_test.go`
的 `TestSchedulerMustNotImportRuntime`）。因此用**回调注入**：

1. `Scheduler` 增加一个可选字段 + functional option：

   ```go
   // recoveryHint, when wired, is invoked at the stale-winner boundary: the
   // winner died between candidate build and executor lookup and no capable
   // replacement exists, so the task would otherwise stall for the full lease
   // TTL. The hint asks the recovery loop to sweep NOW instead. It must be
   // non-blocking (the drain goroutine holds no lock but is on the hot path).
   recoveryHint func(taskID string)
   ```

   `WithRecoveryHint(fn func(taskID string)) *Scheduler`，零值保持现状（无 hint
   时行为与今天完全一致，不改变 leader 路径 / SDK 路径语义）。

2. `scheduler.go:668` 的分支改为：先 `Release(taskID, winner, epoch)` 把任务
   还回 READY（清 owner + lease，checkpoint 保留 —— `Release` 已经是这个语义），
   再触发 hint。这样即使 hint 未接线，任务也回到 READY 而不是继续占着 lease，
   下一轮 drain 就能重新调度；接线后 recovery loop 立刻补 replacement executor。

   **为什么 Release 是安全的**：`Release` 走 `ownerLocked` 做 epoch fencing，
   只有当前持有者能释放；`transition(StateReady)` 保留 `Checkpoint` 字段
   （`fabric.go:375-383` 只清 Owner/Lease）。E1 的"从 checkpoint 恢复而非重启"
   语义不受影响 —— 恢复方仍从 `tk.Checkpoint` 解码。

3. `cmd/ares/peer_mode.go` 在构造 recovery loop 时把提名信号接进来。
   `runKernelRecoveryLoop` 已有 `sem`（容量 1）保护重入，再加一个 buffered
   channel 作为外部触发源，与 ticker / event 并列进 select。

4. `HasCapableExecutor` 不改。它的语义（"当前有没有能接手的 executor"）是正确的；
   问题不在判断，而在判断为 false 之后的处置。

### 实际实现（与上面方案的三处偏离，均为实测驱动）

**偏离 1：不能无条件 Release。** 方案第 2 步写的"先 Release 再触发 hint，即使 hint
未接线任务也回到 READY"是错的 —— 实测 30 轮 6 次失败（`got READY`）。
原因：`Release` 清掉 lease，而 `CheckExpiredLeases` 只扫**仍持有过期 lease** 的任务
（`fabric.go:398-408`）。所以在"无 capable executor 且无 recovery loop"的场景下，
Release 会让任务**永久脱离恢复视野**，比原来的 TTL 停顿更糟。

最终实现是三分支（`scheduler.go:704-736`）：

| 情形 | 处置 | 理由 |
|---|---|---|
| 有其他 capable executor | Release | 下一轮 drain 一个 poll interval 内重新调度（EDGE-4） |
| 无 capable executor，但接了 recovery loop | Release + 提名该 taskID | recovery 立刻补执行体，这是生产路径 |
| 两者皆无 | **保留 lease** | TTL 过期是唯一剩余的恢复触发器，而保留 lease 正是让任务留在 `CheckExpiredLeases` 视野里的前提。leader / SDK / chaos-sandbox 走这条 |

新增 `hasRecoveryHint()`（`scheduler.go:161-166`）供第 2、3 分支区分。

**偏离 2：提名必须携带 taskID，且不能丢。** 方案里 kick 是 `chan struct{}`（纯唤醒信号，
容量 1，满则丢）。两处都不成立：
- 被提名的任务已被 Release、不持有 lease，`CheckExpiredLeases` 找不到它 ——
  所以 sweep 式的"扫一遍"无效，**ID 必须随信号传递**（`chan string`，容量 32）。
- 丢弃语义不对称：丢掉的 sweep 会被下一个 tick 重试，而**丢掉的提名永久丢失**
  （任务无 lease，没有后续 sweep 会重新发现它）。实测这是修复后残留的 1/30 失败。
  最终 `bindNominated`（`kernel_loop.go:417-452`）改为**等待**信号量而非丢弃，
  并以 `ctx.Done()` 兜底退出。等待是有界的：channel 容量 32、循环逐个消费、
  信号量由纯内存扫描释放。

**偏离 3：测试自身有两个缺陷，与产品代码无关但会掩盖真相。**
- `e2e_grand_loop_real_test.go` 原本不接 recovery hint，因此**根本没有覆盖生产链路**。
  接上后（`:226-238`）才真正跑通"死亡 → 释放 → 提名 → 绑定替补 → 完成"。
- 事件流断言直接读 `taskEvents.types` 并即时判断，两个问题：读共享 slice 触发
  `-race`（约 1/40）；事件经 EventStore 异步投递，任务在 fabric 里已 COMPLETED 时
  `task.completed` 可能还没落到日志里。改为 `waitForEvents`（有界轮询）+ `snapshot()`
  （持锁拷贝）。

### 验收标准

- [x] `go test -race -count=1 -coverprofile -run TestE2E_GrandLoop ./cmd/ares/`
      **连跑 60 轮全绿**（修复前 20 轮 1 失败；中间两版分别是 30 轮 6 失败、60 轮 1 失败）
- [x] `go test -race -count=1 -coverprofile=cover.out ./...` 整体绿（`cmd/ares` 不再 FAIL）
- [x] 新增回归测试：`TestStaleWinnerReleasedAndNominatedWhenRecoveryWired`
      断言任务在**不推进时钟**的前提下回到 READY、owner/lease 已清、checkpoint 保留、
      hint 被调用恰好一次
- [x] `TestStaleWinnerKeepsLeasedWithoutRecoveryHint` 锁死偏离 1：
      无 executor 且无 recovery loop 时**必须保留 lease**（Release 会让任务脱离
      `CheckExpiredLeases` 视野）
- [x] `TestStaleWinnerWithReplacementSkipsRecoveryHint` 锁死两分支不混淆：
      有替补时 Release 但**不提名**（否则每次 agent 正常churn 都会唤醒 recovery）
- [x] `TestSchedulerMustNotImportRuntime` 仍绿（回调注入没有引入反向依赖）
- [x] `go test -race -count=5 ./internal/kernelscheduler/ ./cmd/ares/ ./sdk/ ...` 绿

---

## B2（P1）bootstrap 真实构造 `gaCfg.Guardrails`

### 问题证据

设计文档 `ga-runtime-evolution-design-zh.md` §5 已知缺口 1 说"legacy scheduler
没有挂 guardrails，G1 的唯一真实防线是 adapter 层 pre/post 检查"。**实测比这更糟**：

```
$ rg 'Guardrails' internal/ares_bootstrap/ --type go | grep -v '_test'
（仅 3 处注释，零处赋值）
```

- `gaCfg.Guardrails` 在整个 `internal/ares_bootstrap` **从未被赋值**
  → `genome_wiring_system.go:328` 的 `WithAdapterGuardrails` 分支不进
  → `genome_wiring_run.go:210-242` 的 `runPreGuardrails` 因 `a.guardrails == nil`
    直接 `return nil`（第 212 行），post 同理。
- `provide_evolution.go:73-83` 只传 `WithEnabled` + `WithMinInterval`
  → `scheduler.go:550-553` 的 `checkGuardrails` 因 `s.guardrails == nil` 恒返回 true。
- 唯一构造 `EvolutionGuardrails` 的生产位点是 `service/service.go:192`，
  而 `NewService` / `CreateWiredSystem` **没有任何非测试调用者**。

结论：G1 在 bootstrap 驱动的运行时里是**完全空转**的。进化控制平面目前只有
G2 shadow + G3 eval 两道门，而 G2 在默认配置下 fail-closed（P0-9 限制），
G3 只在配了 `eval_suite` 时注册 —— 三道门实际生效数可能为 0。

### 修复方案

1. `internal/ares_bootstrap/bootstrap_steps.go` 的 `wireGAEvolution` 中，在
   `applyGATuning` 之后构造一次 guardrails 并注入 `gaCfg.Guardrails`：

   ```go
   // G1: the guardrail gate was structurally inert — gaCfg.Guardrails was
   // never assigned, so both WithAdapterGuardrails and the legacy
   // scheduler's checkGuardrails degraded to unconditional pass. Construct
   // it here from the YAML tuning so the population-level pre/post checks
   // (unevaluated majority / stagnation / baseline regression / lineage
   // concentration) actually run.
   ```

   参数来源（复用既有 YAML，不新增配置键，避免制造新的死配置）：
   - `WithMaxStagnantGenerations(ec.Generations)` —— `Generations` 当前是死配置
     （`bootstrap_steps.go:249-252` 自己标注了），把它接到 stagnation 阈值上
     既消一条死配置，语义也吻合（"最多允许多少代不改进"）。零值/未设时退回
     `NewEvolutionGuardrails` 的默认 10。
   - `WithBaselineScore(ec.TargetFitness / 100.0)` —— `TargetFitness` 同为死配置，
     量纲是 0-100（config 注释明示），guardrails 的 `BaselineScore` 与
     `bestBySource` 同尺度比较；除以 100 归一到 [0,1]。零值时不设（保持
     `baseline = bestKnown` 的自适应语义）。
   - `MaxLineageShare` 保持构造函数默认 0.8（无对应 YAML，不臆造）。
   - `WithGuardrailEventHandler` 接 Prometheus：guardrail 事件目前只打日志，
     接一个 handler 让 critical 事件可观测（复用 `gaCfg.Metrics`）。

2. `provide_evolution.go` 的 legacy scheduler 同步接入。`ProvideEvolution` 的
   签名不改（它只收 `*ares_config.EvolutionConfig`），在函数内构造同一套
   guardrails 并 `append(opts, evolution.WithSchedulerGuardrails(g))`。

   **两处各建一个实例，不共享**：guardrails 内部有 `stagnantCount` /
   `bestBySource` 可变状态，两条驱动路径（legacy scheduler ticker 与 adapter
   population 层）的代数与分数尺度不同，共享会互相污染 stagnation 计数。
   `bestBySource` 的 source 隔离设计（`guardrails.go:106-110`）证明作者也是
   这个意图。这一点必须在代码注释里写清楚，否则后人会"顺手"合并成单例。

3. 抽一个 `buildEvolutionGuardrails(ec *ares_config.EvolutionConfig) *evolution.EvolutionGuardrails`
   放在 `bootstrap_steps.go`，两处共用构造逻辑、各自持有实例。
   `NewEvolutionGuardrails` 返回 `(*EvolutionGuardrails, error)`（error 恒 nil，
   注释已说明"reserved for future validation"），按 `code_rules_v2.md` §3.1
   仍需显式处理：失败则 warn + 返回 nil（降级为今天的行为），不阻断 bootstrap。

### 实际实现（一处必要的追加修复）

方案第 1-3 步按原样落地：`buildEvolutionGuardrails`（`bootstrap_steps.go:600-651`）、
`wireGAEvolution` 注入（`:257`）、`provide_evolution.go:85-98` legacy 侧独立实例。

**但只做这些，门依然不会响。** 实测发现 `checkGuardrails`（`scheduler.go:588`）
向 `PreEvolveCheck` 传的是**死值** `unevaluatedCount=0`（以及 `generation=0`），
而"未评估过半"是 `PreEvolveCheck` **唯一**的 `ShouldStop` 条件
（`guardrails.go:207-231`；另一条 stagnation 分支只产生 Warning 事件）。
换句话说：接了 guardrails 但不修这个死值，legacy scheduler 的门在任何配置下都不会拦下任何东西。

追加修复：新增 `populationInspector` 接口（`scheduler.go:533-556`），
在 `populationSizer` 之上补 `PopulationUnevaluated()` / `PopulationGeneration()`；
`GenomePopulationAdapter` 实现之（`genome_wiring.go:355-375`）；
`checkGuardrails` 用 type switch 优先取完整形状，只实现 `populationSizer` 的 adapter
退化为 `unevaluated=0`（并在注释里说明这是"无法自省种群"的记录性降级，不是遗漏）。

这一处是"装配的最后一厘米"的典型：配置字段接上了、构造器调用了、
option 传进去了，唯独喂给判定函数的那个参数是硬编码 0。

### 验收标准

- [x] `rg 'Guardrails' internal/ares_bootstrap/ --type go | grep -v _test` 出现真实赋值
      （`bootstrap_steps.go:257` `gaCfg.Guardrails = buildEvolutionGuardrails(...)`、
      `provide_evolution.go:97` `WithSchedulerGuardrails`）
- [x] `TestBuildEvolutionGuardrailsMapsYAML`：5 个用例覆盖
      `Generations` → `MaxStagnantGenerations`、`TargetFitness/100` → `BaselineScore`、
      零值退回配置层默认、`TargetFitness=0` 保持自适应 baseline、`MaxLineageShare` 保持 0.8
- [x] `TestAdapterPreGuardrailsBlockUnevaluatedPopulation`：多数种群未评估时
      `runPreGuardrails` **返回错误并中止本代进化**（经 adapter 而非直调
      `PreEvolveCheck`，这样 adapter 若不再咨询 guardrail 也会失败）
- [x] `TestSchedulerTickBlockedByGuardrails`：guardrails 判定 ShouldStop 时
      `Tick` **不推进 generation**（断言执行被拦，而非只是记了日志）
- [x] `TestSchedulerGuardrailsSeeRealPopulationShape`：锁死追加修复 ——
      种群形状（size / unevaluated / generation）必须真实到达 `PreEvolveCheck`
- [x] `TestBuildEvolutionGuardrailsReturnsDistinctInstances`：**行为级**断言两实例隔离
      （驱动一侧 stagnation 后另一侧不受影响），不只比指针
- [x] `TestBuildEvolutionGuardrailsForwardsEventsToMetrics`：guardrail 事件递增
      `ARES_evolution_guardrail_total{code}`（取增量而非绝对值，因默认 registry 幂等注册）
- [x] `TestAdapterPreGuardrailsNilPassesThrough` / `TestSchedulerCheckGuardrailsNilPassesThrough`：
      把"未接 guardrails 则不拦"钉成显式 opt-out 契约，而非事故
- [x] `Generations` / `TargetFitness` 脱离死配置（`bootstrap_steps.go:626,629-630` 消费）
- [x] `go test -race ./internal/ares_bootstrap/... ./internal/ares_evolution/...` 绿
- [x] `make gate` 全绿（含 `-tags closure` 的 §8 六断言不回退）

---

## B3（P1）`/api/evolution/approve` 三态测试

### 问题证据

```
$ rg 'evolution/approve|EvolutionApprove|StatusConflict' --type go -g '*_test.go'
（零命中）
```

实现是完整的（`cmd/ares/actions.go:440-469`）：503（lifecycle nil）/ 409（无 pending）
/ 200（正常批准），401 由 `checkAuth`（`actions.go:107-144`）提供。lifecycle 侧的
`Approve()` 也有单测（`internal/ares_evolution/lifecycle_test.go:206,256,290,361`，
含并发批准 exactly-once）。**但 HTTP 层零覆盖** —— 这是全仓唯一"实现完成、对外可写、
零验证"的端点。`ares-0.3.1-release-readiness-plan.md:685` 自己也标着未打勾。

### 修复方案

新增 `cmd/ares/actions_evolution_test.go`，复用既有测试设施
（`testActionJWT` / `testActionJWTSecret`，见 `cmd/ares/actions_test.go:57-63`），
不引入新 mock 框架（`code_rules_v2.md` §7、release 计划 §1 术语约定）。

表驱动，四个用例：

| 用例 | 构造 | 期望 |
|---|---|---|
| `no_credentials_returns_401` | `auth` 已配置，请求不带 Authorization | 401 |
| `lifecycle_absent_returns_503` | `h.lifecycle == nil`，带 admin JWT | 503 |
| `no_pending_candidate_returns_409` | 真实 `StrategyLifecycle`，无 pending | 409 |
| `approve_pending_returns_200` | `Gates.RequireManualApproval=true` + Submit 一个 候选使其进 SHADOW | 200，且 body 含 `active_id`，`pending_after == false` |

200 用例的关键在于**构造出 pending 状态**：需要一个真实 lifecycle，
`RequireManualApproval=true`，并让候选通过所有门。因为默认无 shadow evaluator
时 G2 门不注册（`lifecycle.go:319-321` 仅在 `l.shadow != nil` 时 prepend），
所以只要不注入 evaluator，门序列为空，Submit 会直达 `RequireManualApproval`
分支（`lifecycle.go:561-573`）—— 这是最小构造，不需要伪造 shadow 证据。

注意 seed 例外（`lifecycle.go:462-477`）：第一个候选在无 active 时无条件 promote，
所以需要 **Submit 两次**（或预置一个 active strategy），第二次才会被 hold。
测试里要显式注释这一点，否则后人会误以为 Submit 一次就该 pending。

另需补一条断言：**审计落库**。`handleEvolutionApprove` 调
`h.auditAction("evolution_approve", ...)`，用既有的 `auditBuf` 模式
（`actions_test.go:78-79`）断言 `action=evolution_approve` 出现。

### 实际实现（构造 pending 状态比方案预想的更绕）

新增 `cmd/ares/actions_evolution_test.go`，复用 `testActionJWT` / `testActionJWTSecret`，
不引入新 mock 框架。7 个用例：401 / 403 / 503 / 409 / 200 / 幂等 / 非 POST。

三处实测细节值得记录：

1. **`inner` 必须返回 404 而不是 200。** 既有测试设施把 `inner`（未匹配路径的兜底）
   写成返回 200，这会让"路由没匹配上"和"路由匹配且成功"无法区分 ——
   `TestEvolutionApproveRejectsNonPost` 一开始就是因此假通过的。

2. **200 用例需要 Submit 两次。** `lifecycle.Submit` 的第一次是 seed deploy
   （无 active strategy 时无条件 promote，`lifecycle.go:462-477` 的一次性 `seeded` 标志），
   第二次才会被 manual gate 拦住。

3. **刻意不注入 ShadowEvaluator。** `NewStrategyLifecycle` 只在 `l.shadow != nil` 时
   才 prepend G2 门（`lifecycle.go:319-321`）。不注入 → 门序列为空 → Submit 直达
   `RequireManualApproval` 分支。若注入了，默认配置下 G2 是 fail-closed（零对比样本即拒绝），
   候选根本到不了 manual hold。这是最小构造，不需要伪造 shadow 证据。

**反向验证**（确认断言不是恒真）：临时把 `actions.go:451` 的 `if !pendingBefore`
改成 `if !pendingBefore && false`，`TestEvolutionApproveNoPendingCandidateReturns409`
与 `TestEvolutionApproveIsIdempotentAfterSuccess` 双双失败；已还原。

### 验收标准

- [x] `go test -race -count=3 -run TestEvolutionApprove ./cmd/ares/` 绿
- [x] 401 / 403 / 409 / 200 / 503 各有独立断言，不合并（403 是方案外补的：
      `checkAuth` 刻意区分"已认证但无权"与"未认证"，`actions.go:133-140`）
- [x] 200 用例断言 `active_id == "cand-v2"`、`pending_before=true`、`pending_after=false`
- [x] 审计日志含 `action=evolution_approve` 与 `subject=ops-user`
- [x] 401/403 用例额外断言**未消费 pending 候选**（拒绝的请求不得有副作用）
- [x] `TestEvolutionApproveIsIdempotentAfterSuccess`：双击场景第二次必须 409 而非二次 promote
- [x] 无 `time.Sleep` 同步（promote 在 `Approve` 内同步完成，无需轮询）
- [x] 反向验证通过：屏蔽 409 分支后相关测试失败
- [x] `release-readiness-plan.md` L685 该项可勾选

---

## B4（P1）引入 `goleak`

### 问题证据

```
$ rg goleak --type go | wc -l
0
$ grep -n goleak go.mod
（无）
```

`ares-0.3.1-release-readiness-plan.md:405`（T4 验收项）明确要求"`Runtime.Close()`
后所有 loop goroutine 退出，用 `go.uber.org/goleak` 或等价物断言"。现状是
4 处手写 `runtime.NumGoroutine()` 比较
（`internal/ares_bootstrap/closure_lifecycle_test.go:40,67`、
`internal/ares_events/memory_store_close_test.go:28`、`internal/ares_mcp/transport_test.go:249`），
这种写法对并行测试与 runtime 内部 goroutine 敏感，是既有 flaky 的来源之一。

### 修复方案

1. `go get go.uber.org/goleak@v1.3.0`（最新稳定版），`go mod tidy`。
   理由说明（`code_rules_v2.md` §10.1）：标准库无等价能力；
   `runtime.NumGoroutine()` 只能给出计数，无法给出泄漏 goroutine 的栈，
   定位成本高且对 runtime 内部 goroutine 误报。

2. 新增 `sdk/goleak_test.go`，覆盖用户指定的两条路径：

   - `TestRuntimeCloseLeaksNoGoroutines`：`NewRuntime(...)` →
     `ensureScheduler()`（把 scheduler drain / syscall kernel 都拉起来）→
     `Close()` → `goleak.VerifyNone(t, opts...)`。
   - `TestPlanLoopCloseLeaksNoGoroutines`：起一个带 `loop` 的 `create_plan`
     （复用 `sdk/syscall_loop_test.go` 的构造），确认 `LivePlanLoops()` 非空后
     `Close()`，再 `VerifyNone`。

3. **必须显式声明 ignore 列表**，且每条都写明理由。已知的合法长驻/延迟退出
   goroutine 至少包括：`modernc.org/sqlite` 的后台、`database/sql` 的
   `connectionOpener`/`connectionResetter`、OTel BSP 的 flush worker。
   做法：先跑一次拿到真实栈，再按栈逐条加 `goleak.IgnoreTopFunction(...)`，
   **禁止用 `goleak.IgnoreCurrent()` 一把兜掉**（那会连真实泄漏一起放过，
   属于 §0.2 的"假实现"）。

4. 现有 4 处 `runtime.NumGoroutine()` 断言**本期不动**（它们在各自包内且当前
   稳定）。在 `sdk/goleak_test.go` 顶部注释登记："后续把这 4 处迁到 goleak"
   —— 按 §0.3 留 `TODO(tech-debt)` 痕迹。

### 实际实现：goleak 立刻抓出两处真实泄漏 + 一处 flaky

这是本轮性价比最高的一项 —— 引入即见效，且抓到的都是手写
`runtime.NumGoroutine()` **不可能发现**的问题（只有计数没有栈）。

**泄漏 1：`EvolutionScheduler.Register()` 的 goroutine 无人回收。**
`Register()`（`scheduler.go:351`）在自己的 `context.Background()` 上订阅 EventStore
并 park 一个 goroutine，`Shutdown()`（`scheduler.go:652`）**从未被任何生产代码调用**。
后果是该 goroutine 连同喂它的 EventStore subscriber goroutine 泄漏至进程结束 ——
每次 `NewRuntime` / `Bootstrap` 各泄漏一对。两条注册路径都中招：
`provide_evolution.go:99`（legacy）与 `bootstrap_steps.go:417`（wired fallback）。
修复：各挂一个 `comp.bgGroup.Go(<-ctx.Done() → sched.Shutdown())`
（`bootstrap.go:501-520`、`bootstrap_steps.go:414-424`）。

**泄漏 2：`sdk/e2e_h2_test.go` 的 Submit goroutine 永久阻塞。**
该测试硬编码查 `sdk-task-1`，而 `sdkTaskSeq`（`sdk/scheduler.go:74`）是**包级计数器**：
`-count≥2` 时生成的 ID 变成 `sdk-task-2`、`sdk-task-3`……查找永不命中，
测试以"task never SUSPENDED"失败，而后台那个 `rt.Submit` goroutine 无人唤醒、永久阻塞。
这解释了 `-count=5 ./sdk/` 为何同时报 `TestSDKH2_ChaosRecoveryChain` 失败与三个 goleak 失败：
**是同一个根因的两种表现**。修复：显式指定 task ID。

**ignore 列表（7 条，逐条带理由）**：`modernc.org/sqlite` worker、
`database/sql` 的 `connectionOpener`/`connectionResetter`、OTel BSP `processQueue`、
`net/http` 的 `persistConn.readLoop`/`writeLoop` 与 `internal/poll.runtime_pollWait`。
**未使用 `goleak.IgnoreCurrent()`** —— 它会把测试启动时已在运行的一切（含先前代码引入的真实泄漏）
一并放过，属 `code_rules_v2.md` §0.2 的"假实现"。

### 验收标准

- [x] `go.mod` 含 `go.uber.org/goleak v1.3.0`，`go mod tidy` 后无残留
- [x] `go test -race -count=5 ./sdk/` 绿（修复前：`TestSDKH2_ChaosRecoveryChain`
      + 3 个 goleak 测试同时失败）
- [x] 反向验证：临时把 `sdk/sdk.go:404` 的 `r.schedCancel()` 改为 `false && …`，
      `TestRuntimeCloseLeaksNoGoroutines` 失败（证明断言非恒真）；已还原
- [x] 反向验证：临时屏蔽 `bootstrap.go` 的 `sched.Shutdown()` 挂钩，
      `TestRuntimeCloseLeaksNoGoroutines` 失败；已还原
- [x] ignore 列表 7 条每条带理由注释，无 `IgnoreCurrent()`
- [x] 顶部注释登记 `TODO(tech-debt)`：把现有 4 处 `runtime.NumGoroutine()` 迁到 goleak
- [x] `golangci-lint run` 0 issues

---

## B5（P1）总覆盖率 58.24% → ≥65%

### 缺口实测（按未覆盖语句数排序）

总语句 68043，已覆盖 39630（58.24%）。到 65% 需**再覆盖 4597 条语句**。

| 包 | 语句 | 已覆盖 | 缺口 | 覆盖率 |
|---|---|---|---|---|
| `cmd/ares` | 4361 | 1363 | 2998 | 31.3% |
| `internal/storage/postgres/repositories` | 2362 | 0 | **2362** | **0.0%** |
| `internal/storage/postgres` | 1389 | 244 | 1145 | 17.6% |
| `internal/ares_bootstrap` | 1954 | 908 | 1046 | 46.5% |
| `examples/11-knowledge-import` | 1024 | 0 | 1024 | 0.0% |
| `internal/storage/postgres/services` | 1185 | 325 | 860 | 27.4% |
| `internal/ares_memory/experienceadapters` | 111 | 0 | 111 | 0.0% |
| `internal/logger` | 30 | 3 | 27 | 10.0% |

**口径先算清**，否则会做无用功：

| 口径 | 语句 | 覆盖率 |
|---|---|---|
| 全部（含 examples + compat） | 68043 | **58.24%** |
| 排除 `examples/` | 63059 | **62.83%** |
| 排除 `examples/` + `compat/` | 62250 | **63.59%** |

`examples/` 独占 4984 条语句、覆盖 12 条（0.24%）。它们是**可运行的示例程序**，
不是产品代码，把它们计入总覆盖率会把 4.6 个百分点的分母白送出去。

### 修复方案（按性价比排序）

**第 1 步：`repositories` 的 0% 是假 0%，不是没测。**

该包有 8 个测试文件、共约 6000 行，但**全部带 `//go:build integration`**
（`repository_test_helper.go:1-2` 等），默认构建标签下一行都不编译。
`getTestDB` 连的是 `localhost:5433` 的 Docker PG（`repository_test_helper.go:21-29`），
本机 Docker daemon 未运行。

两条路都要走：

- (a) **CI 加 integration job**：`docker-compose` 起 pgvector，
  `go test -tags integration ./internal/storage/postgres/...`。
  这是唯一能真实覆盖 SQL 的方式。但它**不该计入 GA 的 65% 门槛**——
  门槛应当由默认标签下可复现的测试满足。
- (b) **给纯逻辑部分补默认标签测试**：该包里有大量不碰 DB 的代码：
  `experience_repository_memory.go`（101 语句，纯内存实现，0% 覆盖）、
  各 `*_interface.go` 的类型契约、`FormatVector`/`ParseVectorString` 周边的
  参数校验分支、`Create` 里的 `embeddingStr = nil` 空向量分支
  （`experience_repository.go:45-50`，正是本轮 REVIEW #13 改动的地方）。
  `memoryExperienceRepository` 一个包就能拿回 ~101 条语句且零依赖。

**第 2 步：`internal/logger`（27 条缺口，30 语句）** —— 最便宜的一块，
纯函数，一个表驱动测试可拉到 90%+。

**第 3 步：`internal/ares_memory/experienceadapters`（111 条缺口）** ——
`adapters.go` 383 行全是字段映射 + 防御性丢弃（空 ID 丢弃、nil repo 报错）。
用 `NewMemoryExperienceRepository()` 做 fake repo（**该包已有内存实现，不需要新造 mock**），
表驱动覆盖 `SearchByVector` / `GetByMemoryType` 的映射与边界。

**第 4 步：`internal/storage/postgres` 非 DB 部分（1145 条缺口）** ——
`write_buffer.go`（184 语句 0%）、`embedding_queue.go`（184 语句 3.8%）、
`vector_utils.go` 已有测试可扩展。`write_buffer` 是纯内存批量缓冲，可完整测试。

**第 5 步：`cmd/ares`（2998 条缺口，31.3%）** —— 缺口最大但单位成本最高
（多为 CLI 装配与 serve 长流程）。优先补**纯函数与决策分支**：
`serverBindAddr`（已有测试）、`parseKernelPollInterval`、
`handleChaos` 的权限分支（`actions.go:517` 已有测试）、
B3 新增的 approve 测试也计入这里。不追求整包达标。

### 目标与取舍

按上述 1–4 步保守估算可回收 ~1400 条语句 → 排除 examples 口径下约 **65.0%**，
全口径约 **60.3%**。因此**必须同时做 B6 的口径决策**，两者是一件事的两面：

- 若采纳"排除 `examples/`"口径 → 第 1(b)+2+3+4 步即可达标 65%。
- 若坚持全口径 → 还需再啃 `cmd/ares` 约 3200 条语句，工作量翻倍且性价比很低。

**建议**：采纳排除 `examples/` 的口径，理由是 examples 是文档的可执行形态，
其正确性由 `go vet ./examples/...` + `go build` 保证（release 计划 §1 已有此项），
而非单元测试。`compat/` 单独在 B6 决策。

### 验收标准

> **⏸ 2026-09-01 用户指示暂缓**："覆盖率不着急提升，继续其他的任务。"
> 本项挂起，验收标准保留不动。下方数字仍是当前实测基线，可直接续做。
> 当前总覆盖率 **58.9%**（B1-B4/B6 新增测试带来 +0.6pt）。
>
> 注意 B6 的口径决策也随之挂起：Makefile 的 `cover` 目标未改，仍是全口径。
> 因此"排除 examples 后 62.8%"这一数字目前只存在于本文档，不体现在任何命令输出里。

- [ ] `make cover` 报出的总覆盖率 ≥ **65%**（口径在 Makefile 中显式声明并注释）
- [ ] `internal/logger` ≥ 85%
- [ ] `internal/ares_memory/experienceadapters` ≥ 70%
- [ ] `internal/storage/postgres/repositories` 在默认标签下 > 0%（内存实现被覆盖）
- [ ] `internal/storage/postgres` ≥ 40%
- [ ] 新增测试全部 `-race -count=5` 稳定，无 `time.Sleep`
- [ ] 新增测试不引入新 mock 框架（复用 `NewMemoryExperienceRepository` 等既有内存实现）
- [ ] CI 新增 integration job（`-tags integration` + docker pgvector），
      **与 65% 门槛解耦**，失败不阻塞 GA 但必须可见

---

## B6（P2）`compat/` 决策与覆盖率口径

### 现状

- `compat/` 共 809 语句、覆盖 35 条（**4.33%**），19 个文件。
- **唯一生产引用**：`internal/ares_bootstrap/provide_llm.go:7-10`
  （`compat.RegisterLLM` + `compat/llm/{ollama,openai}` 适配器），
  且注册失败只 warn（release 计划 §4 已登记）。
- release 计划 §4 的结论是"保留，但不要让它长出新依赖"；
  T9 的 `怎么做`（L600-601）留了"若长期保留则补基础测试，若移除则按 Breaking change 处理"。

### 决策建议：保留 + 补最小测试，不删

理由：

1. 它有真实生产引用（LLM provider 注册），删除是 Breaking change，
   而 0.3.1 是 patch 版本，不该带破坏性变更。
2. 809 语句只占总量 1.2%，删掉对覆盖率的贡献（+0.9pt）不值一次 Breaking change。
3. 但 4.33% 的覆盖率意味着 `RegisterLLM` 的注册/查找路径几乎未验证，
   而它是 bootstrap 的一部分 —— 应当补最小契约测试。

行动：
- `compat` 根包（`RegisterLLM` / 注册表查找 / 重复注册语义）补契约测试。
- `compat/llm/{ollama,openai}` 的 `New(config)` 参数解析补表驱动测试（不发真实请求）。
- 其余（`loader/pdf`、`vector/pgvector`、`protocol/*`）**明确登记为无生产引用**，
  在 `compat/doc.go`（若无则新建）中写清边界：这是 0.2.x 生态兼容层，
  新代码不得依赖；并按 `code_rules_v2.md` §0.3 留 `TODO(tech-debt)`：
  0.4.x 评估删除。

### 覆盖率口径

Makefile `cover` 目标改为排除 `examples/`，并在目标上方注释说明理由：

```make
# Coverage — full race-enabled coverage report (prints total coverage line).
# examples/ is excluded from the ratio on purpose: they are executable
# documentation, verified by `go build` + `go vet ./examples/...` (see the
# release plan's acceptance baseline), not by unit tests. Counting their 4984
# statements as uncovered product code understates the real figure by ~4.6pt.
```

同时**保留一个全口径目标** `cover-all`，这样"排除"是一个显式选择而非藏起来的
数字修饰 —— 任何人都能一条命令看到全口径值。

### 实际实现（compat 决策已执行；口径部分随 B5 挂起）

**决策：保留，不删。** 复核逐子树确认了引用状况：

| 子树 | 生产引用 |
|---|---|
| `compat/llm`、`compat/llm/ollama`、`compat/llm/openai` | ✅ `internal/ares_bootstrap/provide_llm.go` |
| `compat/loader`（markdown/pdf/html）、`compat/protocol`（mcp/openai_api）、`compat/tool`、`compat/vector`（pgvector） | ❌ 全部零生产引用 |

补的测试（3 个新文件，均 `-race` 绿）：
- `compat/llm/llm_test.go` → 100%：注册守卫、first-writer-wins、`errors.Is(ErrNotFound)`、
  `Names()`、**并发 Register/Lookup**（registry 是进程级单例 `compat.Default`，
  bootstrap 写、runtime 读，并发是常态而非边界情况）。
- `compat/llm/ollama/ollama_test.go` → 88.2%：表驱动覆盖 config map 解析
  （缺 model / nil config / **类型错误的 model 读作缺失**）。
- `compat/llm/openai/openai_test.go` → 95.7%：同上 + provider 覆盖。

**顺带修了一个真 bug。** `compat/llm/openai` 的文档注释写 `base_url` 是
"optional override"，但 `internal/llm.NewClient` 对所有非 Ollama provider **强制要求**
BaseURL（`client.go:198-200`）—— 所以 `New(map[string]any{"api_key": "sk-…"})`
一直是失败的，注释是假的。补了 `defaultOpenAIBaseURL`（与 `sdk/options.go:201`、
`internal/llm/output/openai.go:46` 两处既有默认对齐），**且只对 `provider == "openai"` 生效**：
openrouter / vLLM / azure 没有规范 URL，猜一个等于把用户的 key 发到错误的 host，
宁可让 `NewClient` 拒绝。这条边界有专门用例锁定。

`compat/doc.go`（新增）写明两条边界规则（compat 可依赖 internal，internal 不可依赖 compat，
唯一例外是 `provide_llm.go`）、逐子树的引用审计结果、registry 语义，
以及 0.4.x 重新评估删除的 `TODO(tech-debt)` —— 并说明这是个 release-note 问题
（需要"是否有下游用户 import"这个本仓库没有的数据），不是代码问题。
`registry.go` / `compat.go` 的重复 package 注释收敛到 `doc.go`，
并把两个 sentinel 从 `fmt.Errorf` 改为 `errors.New`（R3 纪律）。

### 验收标准

- [x] `compat` 根包覆盖率 **100%**（原已 100%，保持）
- [x] `compat/llm` **100%**（原 0%）
- [x] `compat/llm/ollama` **88.2%**、`compat/llm/openai` **95.7%**（原均 0%）
- [x] `compat/doc.go` 写明边界规则、逐子树引用审计、0.4.x 删除评估的 `TODO(tech-debt)`
- [x] 顺带修复 openai adapter 的 `base_url` 文档与行为不一致（且不为非 openai provider 臆造默认）
- [ ] ⏸ Makefile 同时提供 `cover`（排除 examples）与 `cover-all`（全口径）
      —— **随 B5 一并挂起**：口径切换的意义在于配合 65% 门槛，单独改数字定义没有价值
- [ ] ⏸ `make cover` ≥ 65%；`make cover-all` 数值在 CHANGELOG 中同时记录

---

## B7（P2）账目对齐

2026-09-01 的代码复核发现根目录计划文档与实际代码存在双向错位。**文档错误比代码
错误更危险**：按错误的引用统计执行删除会破坏生产代码。

### B7.1 `ares-repair-plan-zh.md` 必须修正的错误结论

| 项 | 计划原文结论 | 代码实测 | 后果 |
|---|---|---|---|
| **D15** | "实测生产 0，仅测试"（L1141） | `NewDistillationRepo` 有 3 个生产调用者（`knowledge_akg.go:109`、`sdk/memory_wiring.go:122,334`）；`NewKnowledgeRetrieverAdapter` 有 2 个（`retriever_wiring.go:148`、`sdk/memory_wiring.go:433`）；`SearchByVector` / `GetByMemoryType` 经接口在 `distillation/resolver.go:98`、`manager_impl.go:742`、`distiller.go:790` 被调用 | **按计划删会炸编译** |
| **D16** | 列为 test-only | `RecoverTaskCheckpoint`（`recovery.go:309` 内部调用）、`RecoverFromAgentDeath`（`chaos.go:90` ← `serve_chaos.go:486`）、`NewEvolutionAdapter`（`evolution_population.go:58` ← `serve_agents.go:136`）均生产可达 | **3 个符号删不得** |
| **D14** | 7+3 个符号"真 0 引用" | 仅 `NewKnowledgeDistiller` 与 `NewEvidenceAggregatorProvider` 真 0 引用；`NewPopulationGenealogyRecorder` 在 `genome_wiring_system.go:494` **无条件调用**（每次 bootstrap 都活） | 10 个里 8 个判断错误 |
| **W7 / C4** | `Server.Host` 为"display-only"（L485） | 真实驱动 bind：`serve_routine.go:184` → `serverBindAddr`（`:439-444`），`:196-199` 有 wildcard+auth 告警 | 结论已过期，T1 已改掉 |
| **C1** | 未标完成 | `memory.enable_distillation` 三态门控已落地（`config.go:490-492` 访问器 + `bootstrap_steps.go:43`），`agent-os-loop-wiring-plan-zh.md:179-187` 已记录 | 应标完成 |

### B7.2 应从"未完成"改为"已完成"的项

| 项 | 证据 |
|---|---|
| **W5** PermAdmin | `rbac.go:36-38,49` + `actions.go:517` handler 层校验，三条 chaos 路由全覆盖；`actions_chaos_test.go:79-93` 有测试。（实现在 handler 而非 middleware，需在文档中说明这一设计选择） |
| **D7** storage 删除 | `internal/storage/memory/`、`internal/storage/postgres/query/` 均已不存在，无 importer |
| **E1/E2/B37** 结构化错误 | `internal/errors/kernel_error.go:18-24,50,54` 存在；`internal/kernelscheduler/errors.go:10,12` 存在；`scheduler.go:608,843,846` 已用 `Kernel(...)` |
| **B38 / R1 / R2** | 生产代码 `fmt.Errorf("%s", err)` = **0**，`err.Error() !=` = **0**，`err.Error() ==` = **0**；`actions.go:748` 已改 `errors.Is(err, io.EOF)` |
| **G1–G4** CI 门禁 | `scripts/g1_reachability_gate.sh`、`TestG2ConfigContract`、`TestEventContract_SubscribedMustHaveEmitter` 均存在；`ci.yml:71-72` 与 `release.yml:30-31` 真实调用 `make gate` |
| **P2-1** 进化 metrics | 四个指标均已声明+注册+在 `lifecycle.go:643,663,761,770,795,900,362` 递增 |

### B7.3 仍然为真的未完成项（不要误标完成）

- **E3**：`internal/agentipc/` 未 import `internal/errors`，`primitives.go:156-169`
  仍返回裸 `ErrTimeout`，无 `Kernel("ipc_request","timeout",...)` 包装。
- **R3**：非测试文件仍有 **61 处**静态 `fmt.Errorf("常量")` 未改 `errors.New`
  （`evaluation/evaluation.go:37`、`api/evolution/genome/genome.go:68,85`、
  `api/mcp/stdio.go:89,103` 等）。§2.5.4 的"静态 fmt.Errorf 为 0"未达成。
- **W9 步骤 2/4**：`cmd/ares/workflow_plan.go` 与 `projectWorkflow` 不存在
  （全仓零命中）；`internal/introspect/` 无 plan 视图。步骤 1/3 已完成。
- **C2**：`Tools.Defaults` / `Tools.Agents` 全仓仅出现在 G2 白名单里，仍是死配置。
- **C4**：26 个字段仅 4 个被消费（`Server.Host`、`LLM.Extra`、`Kernel.PollInterval`、
  `Kernel.Policy`；后者仅用于展示/告警，不驱动行为）。
- **P2-3**：promote/rollback 写的是 evidence（`lifecycle.go:921,940`，`Source:"lifecycle"`），
  `internal/knowledge/**` 无任何消费方 —— 决策轨迹写进去没人读。
- **P0-9 后续**：per-task 实执行 A/B 采样仍未做（`shadow_sampler.go:37-45,142-147`
  自己承认"同一对策略重复打分"）。

### B7.4 metrics 命名不一致

设计文档写 `ares_evolution_promote_total` 等小写前缀，实际导出名是 `ARES_` 前缀
（`prometheus.go:152-178`）。按文档字面量写的 dashboard 查询会全部落空。
需在文档与 CHANGELOG 中统一为实际名，或在文档中显式标注前缀差异。

### B7.5 G2 门禁的假绿

`contract_test.go` 的扫描是**基于行的子串匹配**而非 AST，`containsAccess`
（`:200`）无 receiver 校验。后果：`Knowledge.TopK`、`Memory.MaxHistory`、
`Embedding.Dimension`、5 个 `Sub.*` 字段在 `ares_config` 侧无消费者，
却靠 SDK / examples 里的**同名字段**蒙混过关，因此不在 16 条白名单里。

修复方向（本期只登记，不实施）：改为 AST 遍历 + 类型解析，
或把扫描范围限定到 `cfg`/`c`/`acfg` 等已知 config receiver 的选择器表达式。
登记为 0.4.x 项。

### 验收标准

- [x] `ares-repair-plan-zh.md` 的 D14/D15/D16 条目补上"2026-09-01 复核：
      引用统计有误，以下符号生产可达，禁止删除"的更正块（保留原文，追加更正，
      不静默改写 —— 保留判断演进的痕迹）
- [x] W5/D7/E1/E2/B37 标记为已完成，各带证据 file:line；W7 的
      "Server.Host = display-only"定性标注为已过期
- [x] B38 改判为**部分完成**（R1/R2-EOF/R4-内核两包 达成；R3 61 处 + E3 未达成）
- [x] E3/R3/W9-2/W9-4/C2/C3-剩余/C4 保持未完成，并补上逐项实测证据
      （R3 更新为 61 处；C4 逐字段列出"已消费 4 / 仍死 22"）
- [x] W3 / C3 补"8 个已接线 + defaults-equality 取舍"的复核块
- [x] G1/G2/G3 标记已完成（含 CI 接入点 file:line）；G4 标为部分
      （`-race`+closure 已进 CI，12h soak 未执行）
- [x] G2 假绿问题登记在 §7 与 G2 条目内，转 0.4.x
- [x] 新增 §14「附录 F：2026-09-01 全仓复核总表」，4 张表分别是
      应改判已完成 / 禁止删除 / 确认未完成 / 本轮新发现并已修的代码问题
- [x] `ares-0.3.1-release-readiness-plan.md` 状态表补 B1–B7 行 + 任务总表 + 关键路径修订
      + §1 验收基线新增 `-race -coverprofile` 硬门槛 + §5 出口清单新增 8 项
- [ ] metrics 命名差异（`ARES_` vs `ares_`）待写入 CHANGELOG `[0.3.1]`
      —— 文档侧已在 §B7.4 记录，CHANGELOG 属发布收尾动作，留待打 tag 前统一处理

---

## 3. release 计划的结构问题（附带建议）

`ares-0.3.1-release-readiness-plan.md` 共 86 个 checkbox，**仅 9 个打勾**
（全在 K 组 L794-820），而它自己在 L876-879 声明"§0 状态表才是唯一真相，
§5 不回填 task 级 checkbox"。

后果：任何人（包括三个月后的作者）扫一眼文档都会误判项目状态为"几乎没做"，
而实际 T1–T8 + K1–K6 全部落地。这个"双真相"设计是为了消除矛盾，
但代价是文档失去了可扫读性。

建议二选一：
- (a) 回填 T1–T8 的 checkbox，让 checkbox 与状态表一致；
- (b) 删掉 T1–T8 的 task 级 checkbox，只保留 §5 的 GA 出口清单作为唯一勾选面。

推荐 (b)：出口清单本来就是唯一有决策意义的那一层，task 级验收标准更适合
作为"做的时候照着做"的说明文字，而不是需要维护一致性的状态位。

---

## 4. 发布节奏

**Phase 1（回到绿基线）**：B1 → 全量基线重跑 → 可打 `v0.3.1-rc2`
（若 rc1 已发；否则并入 rc1）。

**Phase 2（GA 门槛）**：B2 ∥ B3 ∥ B4 → B5 → B6 → B7 → §5 出口清单逐项复核
→ 12h soak（含 GA 控制平面的构建）→ GA。

**GA 硬门槛**（沿用 release 计划 §3 并补充）：
1. `make check` / `make gate` / `go test -race -count=1 ./...` 全绿；
2. **`go test -race -count=1 -coverprofile ./...` 全绿**（本轮新增 —— 这正是 B1 暴露的缺口）；
3. `make cover` ≥ 65%（口径显式声明），`make cover-all` 数值同时记录；
4. 12h soak 基线在含 GA 控制平面的构建上归档；
5. `internal/ares_evolution` 生产路径无自由裸 `go`；
6. CHANGELOG `[0.3.1]` 的 Fixed / Changed / Breaking / Known limitations 齐备。

**提交纪律**：本计划的所有改动**不自动提交**。每完成一项，运行 §2 验收基线，
把结果贴回对应验收标准的勾选项，由用户决定何时提交
（`plan/rules/code_rules_v2.md` §0.1）。

---

## 5. 执行结果（2026-09-01 收尾）

### 5.1 完成状态

| # | 级别 | 任务 | 状态 |
|---|---|---|---|
| **B1** | P0 | 调度器 stale-winner 竞态 | ✅ **完成**（60 轮 `-race -coverprofile` 全绿） |
| **B2** | P1 | `gaCfg.Guardrails` 真实构造 + 种群形状可见 | ✅ **完成** |
| **B3** | P1 | `/api/evolution/approve` 三态测试 | ✅ **完成**（7 用例，含反向验证） |
| **B4** | P1 | 引入 goleak | ✅ **完成**，并修出 2 处真实泄漏 + 1 处 flaky |
| **B5** | P1 | 覆盖率 → 65% | ⏸ **用户指示暂缓** |
| **B6** | P2 | `compat/` 决策 | ✅ **完成**（保留 + 测试 + doc.go 边界 + 顺带修 1 个 bug）；口径部分随 B5 挂起 |
| **B7** | P2 | 账目对齐 | ✅ **完成**（CHANGELOG 的 metrics 命名一条留待打 tag 前处理） |

### 5.2 最终质量门实测

| 检查 | 结果 |
|---|---|
| `gofmt -l .` | 无输出 |
| `go build ./...` | 绿 |
| `go vet ./...` / `go vet ./examples/...` | 绿 |
| `go build -tags "integration closure" ./...` | 绿 |
| `golangci-lint run --timeout=15m` | **0 issues** |
| `go test -count=1 ./...` | 全绿 |
| `go test -race -count=1 ./...` | 全绿 |
| **`go test -race -count=1 -coverprofile ./...`** | **全绿**（修复前 FAIL） |
| `make gate`（G1/G2/G3 + G4 closure） | 全绿 |
| `go test -race -count=5` on cmd/ares · kernelscheduler · ares_evolution · ares_bootstrap · sdk | 全绿 |
| `TestE2E_GrandLoop` `-race -coverprofile` × 60 | **60/60** |
| `git diff --check` | 无空白错误 |
| 总覆盖率 | 58.9%（B5 挂起，基线 58.24% → +0.6pt） |

### 5.3 本轮真正的收获：修的不是"写错的代码"，而是"没通电的门"

四个 P0/P1 里没有一个是逻辑写错。全部是**装配的最后一厘米断了**，
而且每一处都能通过既有的全部门禁（`make check` 一直是绿的）：

| 症状 | 断在哪一厘米 |
|---|---|
| B1 | 分支逻辑正确，但"交给 TTL 恢复"隐含假设时间会自己前进；只在覆盖率插桩放大窗口后才暴露 |
| B2 | 配置字段有、构造器有、option 有 —— 唯独没人调用构造器；补上后又发现喂给判定函数的参数是硬编码 0 |
| B3 | 401/409/200 实现完整，零测试。全仓唯一"实现完成、对外可写、零验证"的端点 |
| B4 | `Shutdown()` 写好了，从未被调用 |

对应的方法论结论：**门禁命令的形态本身也是验收对象**。
`make check` 绿 ≠ `make gate` 跑过（前者不依赖后者）；
`-race` 绿 ≠ `-race -coverprofile` 绿（后者才是 CI coverage 步骤的真实形态）；
`repositories` 有 6000 行测试 ≠ 那些测试会跑（全被 `//go:build integration` 关在门外）。
这三条已分别写进 release 计划的 §1 验收基线与 §5 出口清单。

### 5.4 未完成项（明确登记，不含糊）

**代码侧**
- E3：`agentipc` 未接结构化错误（`primitives.go:156-169` 裸 `ErrTimeout`）
- R3：61 处静态 `fmt.Errorf("常量")` 未改 `errors.New`
- W9 Step 2/4：`projectWorkflow` 与 introspect plan 视图不存在
  —— 建议先做方向裁决（`engine.Workflow` 在 0.3.x 已被判定不会有执行器），而非照单实现
- C2/C3-剩余/C4：22 个死配置字段
- P2-3：promote/rollback 的决策证据写入 evidence 后无消费方
- P0-9 后续：per-task 实执行 A/B 采样（当前是同一对策略重复打分）
- G1 guardrail 仍在 adapter 层（population 级），不在 lifecycle 门序列内
- G2 门禁子串匹配导致 7 个真死字段漏网（转 0.4.x）

**环境侧**
- 12h soak（须在含 GA 控制平面的构建上）
- 跨主机绑定 e2e、`kill -9` e2e、`docker-compose` 验证
- `TEST_POSTGRES_DSN` + 真实 LLM 的 integration job

**挂起**
- B5 覆盖率 58.9% → 65%，及配套的 Makefile 口径决策

### 5.5 提交状态

**未提交**（`plan/rules/code_rules_v2.md` §0.1）。改动清单：

产品代码 7 个文件 —— `internal/kernelscheduler/scheduler.go`、
`internal/kernelscheduler/genome`…（详见 `git status`）、`cmd/ares/kernel_loop.go`、
`cmd/ares/peer_mode.go`、`internal/ares_bootstrap/{bootstrap,bootstrap_steps,provide_evolution}.go`、
`internal/ares_evolution/{scheduler,genome_wiring}.go`、`compat/llm/openai/openai.go`、
`compat/{registry,compat}.go`。

新增测试 6 个文件 —— `internal/kernelscheduler/scheduler_stale_winner_test.go`（改写）、
`internal/ares_bootstrap/guardrails_wiring_test.go`、`internal/ares_evolution/guardrails_gate_test.go`、
`cmd/ares/actions_evolution_test.go`、`sdk/goleak_test.go`、
`compat/llm/llm_test.go`、`compat/llm/{ollama,openai}/*_test.go`。

新增文档 2 个 —— `compat/doc.go`、本文件。
修改文档 2 个 —— `ares-repair-plan-zh.md`、`ares-0.3.1-release-readiness-plan.md`。

依赖变更 —— `go.mod`/`go.sum` 新增 `go.uber.org/goleak v1.3.0`。

# ARES 深度 Code Review —— 不足 / 未闭环 / 修复方案 / Bug / 测试缺口

> 审查对象：`~/go/src/goagent`（dev 分支，HEAD `c5f36d45`，工作区含未提交的 C3.2 ReplayScorer 进化闭环改动）
> 审查时间：2026-09-02
> 审查方式：静态分析 + 三路并行 subagent（runtime/evolution/storage）+ 实测编译/vet/测试与覆盖
> 实测：`go build ./...` exit 0 · `go vet ./internal/ares_evolution/...` exit 0 · 门禁 `make check/gate/cover` 全绿（上轮 #13 已入库 `8e5ff616`）

***

## 0. 执行摘要

当前 dev 分支在做一件明确且正确的事：**让零 LLM（零 token）模式的进化闭环真正闭合**。C3.2 `ReplayScorer` + 窗口化 `ShadowSampler.Prime` + `cmd/ares/peer_mode.go` 装配 + `kernelscheduler` 真实 latency/retry 捕获，是一段自洽的证据链，代码质量高、自我诊断强（46 处自述未闭合、10 处 `TODO(tech-debt)`、GAP 编号登记）。

但"闭环"仍是**逐零件正确**，尚未端到端导通验证。以下是支撑这一判断的核心拆分问题：

1. **没有测试证明**：零 LLM 默认配置下，一个候选策略能走完 `Submit → G1 → G2 → G3 → G4 → Promote → ACTIVE`。当前 G2 为 fail-closed，存在"永不晋升"的常态风险。
2. **`ReplayScorer`** **存在隐蔽的平局（tie）路径**：候选策略无自身历史 → 每窗口落回冷启动 prior；当 active 在某个窗口也无记录时，两侧同分 → `ShadowWon(shadow>active)` 为 false → 胜率向 0 塌缩，重启 fail-closed 死锁。这是"缺陷转移"的特例，值得显式处理。
3. **架构收敛仍未启动**：双进化系统（v1 `ares_evolution` ≈56.7k 行 / v2 `evolution` ≈9k 行）+ `cmd/` 18k 行编排，是最大的复利负债。
4. **覆盖率距目标有差距**：总覆盖 ≈58%<65%；关键闭环包存在 0% 分支与 0% 生产函数。

***

## 1. 项目体检数据

| 指标                       | 数值                                         | 判读                                  |
| ------------------------ | ------------------------------------------ | ----------------------------------- |
| Go 总行数                   | 360,605                                    | 大型单体                                |
| 非测试 / 测试                 | 181,446 / 179,159（**49.7%**）               | 测试占比极高，正确性有保障，但维护成本随之放大             |
| internal 包数              | 49                                         | 超出单人导航范围                            |
| cmd/                     | 18,015 行                                   | **异常**：CLI 层承载内核编排（三个 kernel\_loop） |
| internal/ares\_evolution | ≈56,708 行                                  | 进化 v1 主战场                           |
| Git 分支                   | `main/master/dev/0.3.x/...` + 远端 `pd/` 多分支 | 分支收敛需关注                             |

***

## 2. 未闭环位置（优先修）

### P0-1 端到端零-LLM 闭环未验证（代码正确但可能"永不晋升"）

**现象**：`ReplayScorer` 已注入、`ShadowSampler.Prime` 已给每比较分配不相交窗口，但没有任何集成测试证明链条导通。G2 `ShouldDeploy` 是 fail-closed（`shadow_evaluator.go`：insufficient samples → 拒绝）。

**风险**：当前状态逻辑正确、系统无用；单测全绿会让这不可见。

**修复**：补一条集成测试，预置多 10 分钟窗口的 `KindFitness(source="strategy")` 记录 → 提交候选 → 断言终态 `StateActive` 且选率≥MinSamples；反向用例空证据仓 → 断言停留 `StateShadow`（fail-closed 生效）。

### P0-2 `ReplayScorer` 冷启动平局（tie deadlock 复活）

**现象**：`replay_scorer.go` 每窗口若无记录则 `coldStart()` 返回先验（`peer_mode.go` 单值 `det.ScoreAttribution`）。候选永远无自身历史 → 每个窗口都对该单值 vs active 历史均值比较。**当 active 在窗口内也无记录时两侧都回先验 → 平局 → fail-closed**。

**与现有语义的冲突**：`peer_mode.go` 注释声称"同一全局分数 → 平局死锁"已被消除，但冷启动窗口仍可能产生平局。差异只在于 active 有历史时它不平局；历史稀疏（刚启动/低流量）窗口仍平局。

**修复**：

1. 显式处理"窗口内 active 也无记录" → 该比较**不计入** TotalComparisons（而非记平局），避免用平局稀释胜率；
2. 或在先验处区分：无记录的 active 用其长期均值（跨全历史），而非单点先验，避免两策略同回一个数。

### P1-1 Per-task A/B 执行路径缺失（自行标注）

`shadow_sampler.go`：*"Per-task A/B execution ... that path does not exist yet."* 现行判定实质是"线上机队质量 vs 线上策略历史"，**非候选特异性**。补齐前 G2 判定语义是"线上策略是否拖累质量"，README/设计文档应写明，避免外部读者误判。

### P1-2 零-token 下 G3 / G4 可用性未审计

- **G3** 依赖 `ares_eval.EvaluatorRegistry`——零 LLM 下 registry 若无则 `eval_gate_wiring.go` 有意"不起 G3"（honest absence）。空/未配置时是 pass 还是显式跳过，应写进 CHANGELOG。

- **G4** deployment staging 默认配置下是否接线，未单独验证。

### P1-3 双进化系统未收敛（最大复利负债）

| <br /> | v1 `ares_evolution` | v2 `evolution`          |
| ------ | ------------------- | ----------------------- |
| 外部引用文件 | 51                  | 32                      |
| 进化对象   | 策略/记忆               | 工作流 DAG                 |
| G3 门控  | `gate_eval.go`      | `gate3_orchestrator.go` |

两套都在用，都不能删。建议**先抽共享验证流水线**（`verify/` 层：G1–G4 门控语义 + evidence 读写 + promotion），再统一命名空间。第 1 步不改外部行为、可独立验证、收益最大。

**说明**：Subagent 已在门控/生命周期处验收了 `args`/占位符/句柄等大部分"半闭环"，绝大多数已是显式标注而非暗病。

***

## 3. 真实代码 Bug

### B-1 `kernelscheduler` `Attempts` 时序误读（P1）

`scheduler.go` diff 中：

```go
retries := tk.RetryPolicy.Attempts   // ← 在 RunQuantum 前采样
...
s.endQuantumOutcome(..., retries)     // ← 作为 retry 数传入
```

`taskfabric.go:389` 的 `RetryPolicy.Attempts++` 发生在 `Fail()` 内。若本轮 quantum 可能触发重试，传入的 `retries` 是**执行前**的值（本轮的失败尚未计入），而 `RecordWithMetrics(..., retries, ...)` 语义是"该次执行的重试次数"。

**建议**：在 `RunQuantum` 返回后再读一次 Attempts（或由 `Fail` 回调回写），确保归因的是本轮实际重试，而非起始预算；并在注释中明示"预算 vs 实际"。

### B-2 `lifecycle.go` 文件头注释与实现可能不一致（P1）

文件头：*"lifecycle is nil-safe: when not wired (legacy mode), the adapter falls back to the old unconditional deploy path"*。但 `Submit`（已替代旧的无条件部署）与 `shadowVerifyGate` fail-closed（:520）并存。若无 legacy 兼容路径触发条件应写明；若已废弃应删注释，避免维护者以为存在绕过门控的路径。

### B-3 潜在平局稀释胜率（由 P0-2 派生，见上）

`ShadowEvaluator.RecordResult`/`ShouldDeploy` 只累计 `shadowScore > activeScore` 为胜。平局比较（equal）既不算胜也不算总样本之外，仍计入 `total` → 稀释 winRate。同样建议：平局（含冷启动先验对先验）不计入 `total`。

### B-4 已知/已修项（确认为非开放 bug，记录以备回归）

- `knowledge/store/postgres/store.go:217` `OFFSET $N` 已参数化（`//nolint:gosec`）——安全。

- `store.go:394` `ANY($1)`+`$2` 绑定 bug——已修，现为正确 `{ids, model}`。

- `pool.go` tenant `set_config` 事务/连接级混用——已用 `is_local=false`+`ManagedRows` 关闭时清理，防 RLS 泄漏——实现严谨。

***

## 4. 测试不足与覆盖薄弱区

| 位置                                                | 现象                                                                                       | 建议                                                                 |
| ------------------------------------------------- | ---------------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| `shadow_sampler.go:93` 构造路径                       | 覆盖 **0.0%**                                                                              | 补 `NewShadowSampler` 输入归一化用例                                       |
| `shadow_evaluator.go:168/271/282/293/306/357/367` | 多个 0% 分支（`RecordResult` 累增、`ActiveStrategy`/`ShadowStrategy`、`HasIndependentScorer` 返回值） | 补句柄/空态断言                                                           |
| `replay_scorer.go` 冷启动                            | 测试覆盖了 disjoint + real win rate，但**未覆盖 active 窗口空 → 平局**的正反例                              | 补"两侧先验平局不计入样本"用例                                                   |
| `internal/storage/postgres/repositories/`         | **0% 单测**（仅集成、需真 PG）                                                                     | 用 `pgerr`/接口 fake 或透明重放补逻辑单测，重点 experience\_repository 的 NULL 向量路径 |
| 端到端晋升                                             | 无 Submit→ACTIVE 零-LLM 链式测试（P0-1）                                                         | 新增集成测试作门禁                                                          |
| `postgres` / `knowledge` 其余                       | `pool/embedding_queue/hybrid` 均有测试但在真 PG 上                                               | 保持，但管道需 CI 起 pgvector 容器                                           |

***

## 5. 修复路线（按优先级）

| 优先级       | 动作                                | 产出                             | 判据                   |
| --------- | --------------------------------- | ------------------------------ | -------------------- |
| **W1**    | 端到端零-LLM 晋升集成测试（P0-1）             | 1 个 integration test           | 候选能晋升；空仓 fail-closed |
| **W1**    | 冷启动平局不计样本（P0-2 → B-3）             | `shadow_evaluator`/`replay` 改动 | 稀疏历史不再坍缩胜率           |
| **W1**    | 修 `Attempts` 时序（B-1）              | `kernelscheduler` ２ 处          | 归因为本轮实际重试            |
| **W2**    | 审计 G3/G4 零-token 行为（P1-2）         | CHANGELOG 条目                   | 每级门控默认行为明确           |
| **W2**    | `replayWindowSpan/QueryLimit` 配置化 | SystemConfig 字段                | 参数可见可调               |
| **W3–W4** | 抽共享验证流水线（P1-3 步 1）                | `evolution/verify/`            | 双系统复用门控语义            |
| **W5+**   | cmd 三循环下沉                         | `internal/system_runtime`      | SDK 可复用编排            |

**顺序理由**：W1 决定"闭环是否真的闭合"，工作量小，应最先做；架构重构等闭环验证通过后再启动，避免在不确定地基上施工。

***

## 6. 工程卫生

- `cover.out`（覆盖率产物）已 `gitignore`，OK；根目录 `*.excalidraw` 已忽略，OK。

- 根目录计划文档（`ares-evolution-loop-*`，883+182 行）建议移 `docs/`。

- `REVIE-2026-09-02.md` 与本文同为 review 产物，建议保留但明确版本（对应 HEAD），避免混淆。

***

## 7. 值得肯定的部分

1. **冷窗口数学正确**：`Prime` 以单次时钟读数作锚点、向后切片不相交窗口，避免批内执行时间致窗重叠——这是"统计显著性伪装"的实修，非纸面。
2. **诚实失败优先**：`ReplayScorer` 冷启动不编造有利值，回退到可辩护 prior，并把语义写清。
3. **缺陷转移意识**：`peer_mode.go` 注释论证"只修症状会让 defect 换位"，并据此选择候选特异性证据源。
4. **技术债显式登记**：46 处未闭合 + `TODO(tech-debt)`——隐藏的债远比登记的险。

***

## 附录：验证记录

```
go build ./...                        exit 0
go vet ./internal/ares_evolution/...  exit 0
make check  (lint + 全量单测)           PASS（上轮 #13 后基线）
make gate   (G1/G2/G3 + closure)      PASS
make cover  (-race + 覆盖率)           PASS，总 ≈58.8%
coverage: shadow_sampler.go:93 0% · ares_evolution 多分支 0% · repositories 0%
```


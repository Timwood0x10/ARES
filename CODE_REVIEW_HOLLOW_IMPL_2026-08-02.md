# ARES「空心实现」类问题清单与解决方案（2026-08-02）

> **范围**：本次聚焦 code review 中 **「空心实现 (hollow implementation)」** 一档 High 发现——代码看似实现、编译通过、接口齐全，但运行时并不产生真实效果。
> **经 2026-08-02 当日重新实测**，4 条原始发现中 **2 条已自洽（非问题）、2 条确为真实空洞**；当日用户已修复 GA fitness 三阶段 + Evidence 总线接线（**未提交 git**），故当前**仅剩 VectorProvider 伪 embedding 1 条真实空洞**。
> **本次已完成（非空心类）**：arena 中间件空 key 放行（R-1，改 deny-by-default）、leader 二次 Stop 死锁（R-3）、ResolveConflict 塌缩（R-4），编译/vet/`-race` 测试全绿，已补回归测试，未提交 git。

---

## 0. 已推翻的旧结论（避免重复投入）

| 原结论 | 实测现状 | 证据 |
| --- | --- | --- |
| 蒸馏不落盘 = 孤儿 sink | 写回路已通 | `sdk/distill_events.go:165 bridge.DistillConversation` → `adapter/distill.go:316 persistAndPromote` 真落盘 |
| arena 指标恒 0 | 已是真三维加权 | `internal/ares_arena/score.go:37 CalculateScore`（可用性 40% / 恢复 30% / 一致性 30%） |

---

## 1. 问题一：VectorProvider 伪 embedding（字母重排同向量）

- **严重度**：High（检索质量 silently 退化，且存在系统性碰撞）
- **位置**：`internal/knowledge/provider/vector/provider.go:239`

**现象**
```go
for _, c := range intent.Goal {
    seed += int64(c)
}
```
种子 = `intent.Goal` 各字符码点**求和** → `"部署失败排查"` ≡ `"排查部署失败"`（字母任意重排得到同一向量）。这是带系统性碰撞的伪随机，代码注释自认「需替换为真 embedding 模型」。

**根因**：仓库已有真 embedding 客户端 `internal/storage/postgres/embedding/client.go`（真 HTTP `POST /embed`、Redis 缓存、批量 `EmbedBatch`、超时与 `fallback.go` 降级），但 vector provider 没接线，自己造了个伪函数。

**解决方案**（用户决策：接真客户端，但不可硬依赖——服务不可用时降级为关键词检索 + 告警，绝不用伪向量顶替）
1. `provider.go` 注入 `*postgres.EmbeddingClient`（依赖注入，非包级单例）。
2. `Embed` / `EmbedBatch` 调用真 client；client 返回错误时降级为关键词/字面量检索，并记录 `WARN` 级告警（含失败原因）。
3. **彻底删除** `seed += int64(c)` 伪向量分支——降级路径必须与伪向量互斥，避免「降级后又静默回退到假向量」。
4. 加单测：同一字符串两种重排 → 向量必须不同；client 不可用时 → 降级且不 panic、有 WARN 日志。

---

## 2. 问题二：GA fitness 落死区（evidence 自举死锁 + fitness 硬编码）

> ✅ **状态：已由用户于 2026-08-02 修复（未提交 git）**。新增 `internal/evolution/genome/fitness.go`（`avgFitnessValue` 从 evidence payload 读真实 `Value`），5 个 genome 的 `Fitness()` 全部改读真实证据；Flight/Memory/AKF 生产者接好（源名 memory/knowledge/recovery/workflow/scheduler 与 genome 查询对齐），发射真实成败值(0/1)。`go build` / `go test ./internal/evolution/genome/...` 通过。下方根因分析保留作历史记录。

- **严重度**：High（核心卖点「自我进化 / 自愈」实际空转，且无任何「我没做事」的日志）

### 2.1 evidence 自举死锁（真根因）
- 5 个 genome 查 `Source: "memory"/"knowledge"/"recovery"/"workflow"/"scheduler"`（`internal/evolution/provide_new_evolution.go:78-130`）。
- 全仓库 evidence 唯一外部生产者是 `internal/ares_arena/service.go:159`，写 `Source="chaos"`，且**仅 action 失败时**写 → 与 genome 查询的 Source **交集为空**。
- 5 个 genome 的 `Append` 全位于 `if len(evs)==0 { return 0.5 }` 早退之后 → 自己产生不了第一条 evidence → 永久空。
- **后果链**：5 genome 恒 0.5 → 均值 0.5 → `genome_wiring_run.go:429 fitness*=100` → 50 → `coordinator.go:402-410` 落 `[30,60)` → `DecisionDelay` → `coordinator.go:372 maxProposalRetries=3` 后**静默丢弃**。GA 一个补丁都应用不了。

### 2.2 fitness 是「配置的函数」而非「表现的函数」（第二层病）
- 即使打通自举，fitness 仍来自硬编码：`memory`(MaxHistory/MaxSessions)、`knowledge`(MaxResults)、`recovery`(strategy 硬编码 0.8/0.7/0.4)、`workflow/scheduler`(取 evidence 均值 = 上次自己写的 fitness，**数学不动点**)。
- evidence **内容一字节未参与计算**，只当 `len==0` 的开关。修自举只会让 GA 收敛到硬编码偏好。

### 2.3 解决方案：三阶段，顺序不可颠倒（用户决策：GA 是核心卖点，必须真做）
- **阶段 A — fitness 改真**：`Fitness()` 改从 `Store.Aggregate(ctx, filter, AggregateFn)`（`internal/evidence/evidence.go:74-85` 已提供，当前是死代码）聚合 payload 算真实指标（如 recovery = 真实恢复成功率、memory = 真实命中率）。停写自指。
- **阶段 B — 接通证据生产者**：把设计里 6 个 Kind 生产者中未接的 3 个接上——Flight Recorder → `KindExecutionTrace`、Memory Distillation → `KindKnowledge`、AKF → `KindInsight`。这样 genome 消费外部模块的真实表现，而非自产自销。
- **阶段 C — 验证后开自动应用**：先离线跑区分度（确认 fitness 能区分好/坏配置），再放开 `ApplyFitnessThreshold=60` 的自动应用。

> ⚠️ **上线顺序安全约束**：只接通 evidence 生产者（阶段 B）而不改 fitness 计算（阶段 A）是**危险**的——fitness 会脱离 0.5 并可能越过 `ApplyFitnessThreshold=60` 触发自动应用，而此时 fitness 仍是硬编码偏好 → 从「不做事」变成「做错事」。**两阶段必须同时上，或阶段 B 期间先关掉自动应用。**

---

## 3. 关联问题：Evidence 总线仅接 2/6 生产者

> ✅ **状态：已由用户于 2026-08-02 修复（未提交 git）**。现 Chaos / Flight(`ares_flight/collector.go`) / Memory(`retriever_wiring.go:99` 源 `memory`) / AKF(`runtime.go:85` 源 `akf` 发射 Insight + `runtime.go:88` 源 `knowledge` 发射 Fitness) 均已接线，GA 不再自反馈空转。

- **位置**：`internal/evidence/evidence.go:26-32`（设计 5 个 Kind → 6 类生产者，还含 Critique）
- **现象**：Chaos(`ares_arena/service.go`) 真接；**Flight / Memory / AKF 三个生产者现已接线**（详见上方状态）。
- **解决方案**：见 2.3 阶段 B。遥测源已存在，无需从零造：`internal/ares_observability/metrics.go`（`RecordLLMCall`/`RecordToolCall`/`RecordAgentStepDuration`/`RecordAgentError`，均带 `duration`+`hasError`）、Flight Recorder 在录 trace、distillation 在跑、AKG 写回路已通。

---

## 4. 方向决策记录（用户，2026-08-02）

- **GA**：核心卖点，必须真做（不是「看起来在进化」）。
- **Embedding**：接真客户端，但不接受硬依赖——服务不可用时降级为关键词检索并告警，**绝不用伪向量顶替**。

---

## 5. 落地顺序建议

1. **（仅剩）修 Embedding 伪向量**（`provider/vector/provider.go:239`，独立、风险低、收益明确，且能顺带消除一个系统性碰撞隐患）。GA 三阶段已由用户完成，无需再排期。
2. GA 修复后建议补「GA 静默丢弃」可观测性日志，避免未来再次无感空转；并在阶段 C 前离线验证 fitness 区分度。

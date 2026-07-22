# 进化系统 Code Review（2026-07-22，第 11 轮之后）

> 范围：`internal/evolution`（patch/diff/genome）、`internal/ares_bootstrap`（接线）、`internal/ares_memory`、`internal/knowledge/runtime`、`cmd/ares/serve.go`。
> 方法：独立实证复核（grep/read/build），不轻信修复报告字面。本轮**只读不改**。
> 前置：第 11 轮已把 `memory_differ.go` 三处 `Target:"memory.config"` 改为 `"memory"`，memory 路由闭合。

## 0. 结论速览
- **功能闭环：确认达成（6 维 + runtime GA 全真实作用 live agent）。**
- **但仍存在 1 个脆弱闭环 + 3 类代码质量问题**，其中 1 个是潜在回归炸弹（knowledge 的 live-swap 实际是静默 no-op），2 个是 rollback/健壮性 bug。

---

## 1. 闭环实证（已确认）

| 维度 | 证据 | 状态 |
|---|---|---|
| runtime GA | `serve.go:180` `NewStrategySource(StrategyStore)` → agent 经 `agents.StrategySource` 消费 | ✅ |
| workflow | `liveDAGPatchExecutor.Apply` 直接 mutate `mgr.GetAgentDAG(id)`（`serve.go:564-572`）；fallback 路由（:283） | ✅ |
| scheduler | `serve.go:632` 写 `dag.SchedulerType`；`mutable_dag.go:345` `GetExecutionOrder` 读取 | ✅ |
| recovery | `UpdateLiveDAG:322` `recoveryExec.SetDAG(liveDAG)`；`recovery_patcher.go:26` 换引用 | ✅ |
| knowledge | `bootstrap.go:199` `knowRt:=BuildKnowledgeRuntime()` 同时喂 `ProvideNewEvolution` 与 `serve.go:144` AKF 工具（**同一实例**） | ✅ |
| memory | `bootstrap.go:188` `liveMemoryStore=comp.Memory`（live）；经 `MemoryPatchExecutor` 写同一实例 | ✅ |

memory 是真闭环的关键验证：`comp.Memory` 经类型断言传给 `ProvideNewEvolution`（`bootstrap.go:187-202`），agent 持有的 `memMgr` 也是同一个 `comp.Memory`（`serve.go:114`）。第 11 轮的 Target 修复使 memory patch 命中 `"memory"` 键（而非跌 fallback），**真实生效**。

---

## 2. 严重问题（P0 / P1）

### 🔴 P1 — `UpdateLiveKnowledgeRuntime` 是静默 no-op（潜在回归炸弹）
`cmd/ares/serve.go:308` 调 `UpdateLiveKnowledgeRuntime(comp.KnowledgeRuntime)`，意图"把隔离 runtime 换成 agent live runtime"。但实现（`provide_new_evolution.go:251-266`）是：
```go
liveExec := knowledgeruntime.NewKnowledgePatchExecutor(rt)   // Name()=="knowledge"
c.PatchReg.RegisterComponent(liveExec)                        // "knowledge" 键已被 bootstrap 占用
c.PatchReg.Register("knowledge.planner.max_results", liveExec) // 子键也已被占用
...                                                          // 全部 _ = 忽略错误
```
- `Register` 对已存在键返回 error 且不覆盖（`patch.go:200-202`），所有调用被 `_ =` 吞掉。
- 当前之所以"闭环"，**不是因为 swap 生效**，而是因为 `bootstrap.go:199` 已经把同一个 `knowRt` 同时喂给 executor 和 agent AKF 工具——闭环靠 bootstrap 同实例共享，而非 swap。
- **风险**：若未来有人把 `ProvideNewEvolution` 的 `rt` 改成 `nil`/占位（历史上 recovery 就走过这条路），期望 `UpdateLiveKnowledgeRuntime` 来注入 live runtime，则子键 `knowledge.planner.*` 仍指向 `noopKnowledgeExecutor`（bootstrap 注册），knowledge patch 静默打到 no-op → **空转回归**，且无任何报错。

**建议**：knowledge 应仿 recovery 的 `SetDAG` 模式——给 `KnowledgePatchExecutor` 加 `SetRuntime(rt)`，`UpdateLiveKnowledgeRuntime` 改为调用它（不再 RegisterComponent），并删除误导性的"live 注入"注释。或新增 `Registry.Replace/Upsert` 真正覆盖注册。

### 🟠 P1 — `MemoryPatchExecutor` 的 rollback 无效（部分损坏风险）
`memory_patcher.go:104-144` 的 `Apply`：**先改 config，后校验**（如 `PatchChangeBudget` 先写 `MaxDistilledTasks` 于 :123，再 `fmtDuration` 校验 `session_ttl` 于 :125-130）。若 `session_ttl` 非法 → 返回 error，触发 `patch.go:234-245` 的 rollback。
但 rollback patch 的 `Value` 是 `MemoryConfig` 结构体（:150），而 `Apply` 只认 `map[string]any`（:107/121/136）。回放 rollback 时类型断言失败 → **静默 no-op → 已写入的 `MaxDistilledTasks` 不被回滚**。
- 当前 differ 只发 `max_distilled_tasks`（不发 session_ttl，`memory_differ.go:58-60`），所以线上不触发；但属**潜在数据损坏 bug**，任意直接 `Apply` 含 TTL 的 budget patch 即中招。

**建议**：校验先于写入；或 rollback 用与正向相同的 `map[string]any` 格式。

---

## 3. 健壮性问题（P2）

### 🟡 P2 — `Register` 错误被全局 `_ =` 吞掉
`provide_new_evolution.go:192-230,260-264,315-316`、 `serve.go:281-282` 全部 `_ = patchReg.Register(...)`。这掩盖了"键冲突/注册失效"，是 recovery（旧）与 knowledge（现）两处"接上却空转"的共同根因。
**建议**：至少 `log.Warn` 注册错误；长期引入 `Upsert` 语义。

### 🟡 P2 — fallback 单 patch 丢弃 rollback（`patch.go:229`）
`Apply` 走 fallback 分支时 `rollback, err := r.fallback.Apply(...); ... _ = rollback`——rollback 被丢弃。
`ApplySet`（:269）会捕获 fallback rollback，但**单 `Apply` 不会**。若 Coordinator 对 workflow 结构 patch 走单 `Apply`，失败回滚时该 patch 无回滚保护。
**建议**：单 `Apply` 与 `ApplySet` 的 rollback 语义保持一致（返回/记录 fallback rollback）。

### 🟡 P2 — `MemoryPatchExecutor.Apply` 对非法 `Value` 静默 no-op
`memory_patcher.go:107/121/136` `if v, ok := p.Value.(map[string]any); ok`——若 differ 发错类型，patch "成功"但**零修改零报错**。加上 `h>0` 守卫（:108）使合法的 `MaxHistory=0` 被静默忽略。
**建议**：非法 Value 应返回 error，而非静默成功。

### 🟡 P2 — `PlannerGenome` 是冗余死代码（已知，无动作）
`provide_new_evolution.go:120-128` 注册 `PlannerGenome` 但无对应 differ（`diffReg` 未注册 `NewPlannerDiffer`），`generateDiffPatches` 对其 `continue`（`genome_wiring_system.go` 按 name 查 differ，查不到跳过）。planner 维度已由 `KnowledgeDiffer`（`knowledge.planner.*`）覆盖。保留无碍，但注释已说明"dead code for patch pipeline"，建议要么接 differ 要么删除以免误导。

---

## 4. 测试缺口（根因级）

- **`internal/evolution/diff/diff_test.go` 无 `MemoryDiffer` 测试**（仅有 workflow/knowledge/scheduler/recovery）。Target 错配（第 10 轮）正是因无测试断言 `Target=="memory"` 而漏过 10 轮。
- **无路由集成测试**：没有"MemoryDiffer 产 patch → PatchReg.Apply → MemoryPatchExecutor → live config 改变"的端到端断言。`Register` 不覆盖 + 键冲突类 bug 也靠集成测试才能抓到。

**建议（最小高价值）**：
1. `TestMemoryDiffer_Diff`：断言产出的 3 个 patch `Target=="memory"`、`Type` 正确。
2. `TestMemoryPatchRouting`：构造 `PatchReg`，注册 `MemoryPatchExecutor(liveStore)`，`Apply` 一个 `Target:"memory"` 的 `PatchChangePlanner`，断言 `liveStore.GetConfig().MaxHistory` 变化。

---

## 5. 改动清单（供核对）
仅第 11 轮由我方修改：
- `internal/evolution/diff/memory_differ.go`：`:48/63/77` `Target:"memory.config"` → `"memory"`。已 `go vet` 通过（`./internal/evolution/diff/... ./internal/ares_bootstrap/... ./internal/ares_memory/...`）。
其余均为既有代码，本报告未改动。

## 6. 优先级排序
1. **P1** knowledge live-swap 实为 no-op（改 `SetRuntime` 模式，消除"靠 bootstrap 同实例才闭环"的脆弱性）
2. **P1** memory rollback 无效（校验前置 / rollback 格式对齐）
3. **P2** 注册错误可见性 + fallback 单 patch rollback
4. **P2** 补 MemoryDiffer + 路由集成测试

# Code Review — Commit `0f9aa9d` "fix: resolve 21 AKF bugs"

- **Reviewer**: Senior Developer (高级开发工程师)
- **Target**: `0f9aa9d` — 18 files changed, +458 / -90
- **Scope**: 审查该 commit 是否真的修好了之前 `AKF_BUGS_REPORT.md` 里列的 21 条问题，并排查是否引入新缺陷、测试是否假绿。
- ** verdict**: ❌ **不建议合入（as-is）**。修好了一部分真实问题，但**引入了一个 release-blocking 的数据竞争**，且至少 3 个根因问题只动了表象没动根。非 `-race` 测试套件是"假绿"。

---

## 0. 一句话结论

> 这个 commit 把"死代码接活"和"明显崩溃"修掉了一部分，但**把 `KnowledgePipeline` 接入了并发加载路径却没有保护它内部的共享 map**，于是 `go test -race` 直接挂。B10 / B16 / B17 三处只是换了皮（降分、换排序算法、加了个错误的 offset guard），根因原封不动。

---

## 1. 🔴 Critical — 本次 commit 引入的新缺陷

### B22 · `KnowledgePipeline.resolvedObjects` 数据竞争（新引入）
- **位置**:
  - 写：`internal/knowledge/pipeline.go:161` → `p.resolvedObjects[obj.ID] = obj`
  - 并发触发：`internal/knowledge/runtime/runtime.go:156` / `:180`（`loadAndProcess` 起多个 goroutine 并发调 `r.pipeline.Process`）
- **根因**：`resolvedObjects` 是 `KnowledgePipeline` 的**结构体字段**（`pipeline.go:81`），被所有 `Process` 调用共享。`runtime.go` 的 `mu` 只保护了 `objects` map（`:186`），**完全没有保护 pipeline 内部的 `resolvedObjects`**。多个 provider 并发跑时，对同一个 map 同时读写 → data race。
- **实测证据**（`go test -race ./internal/knowledge/runtime/...`）：

  ```
  WARNING: DATA RACE
  Write at 0x00c000124030 by goroutine 19:
    .../knowledge.(*KnowledgePipeline).Process()
        pipeline.go:161 +0x8e8
    .../knowledge/runtime.(*KnowledgeRuntime).loadAndProcess.func1()
        runtime.go:180 +0x278
  Previous write at 0x00c000124030 by goroutine 20:
    .../knowledge.(*KnowledgePipeline).Process()
        pipeline.go:161 +0x8e8
    .../knowledge/runtime.(*KnowledgeRuntime).loadAndProcess.func1()
        runtime.go:180 +0x278
  --- FAIL: TestKnowledgeRuntimeFullPipeline (race detected) ---
  ```

- **为什么"假绿"**：常规 `go test`（不带 `-race`）通过；CI 若无 `-race` 则永远发现不了。线上表现为偶发的图结构错乱 / 崩溃，极难复现。
- **修复建议（任选其一，推荐 b）**：
  - **(a)** 给 `KnowledgePipeline` 加一把 `sync.Mutex`，所有对 `resolvedObjects` 的读写都加锁（简单但每次 Process 都争锁，吞吐下降）。
  - **(b)【推荐】** 把"实体解析候选累积"移出并发热路径：各 goroutine 只做 Normalize/Summarize，把对象并发收进 `objects` map；**等 `wg.Wait()` 之后，在主 goroutine 里顺序跑 Resolve 阶段**（实体解析本来就是跨对象操作，顺序跑没损失，还更正确）。这样 `pipeline.Process` 可拆成 `ProcessFast`（并发安全）和 `ResolveAll`（顺序）。
  - **(c)** 每个 provider goroutine 拿一个独立的 `KnowledgePipeline` 实例（最省改动但候选池被割裂，解析质量下降）。
- **验收**：`go test -race ./internal/knowledge/...` 全绿；并把它加进 CI。

---

## 2. 🟠 High — 根因未修（只动了表象）

### B10 · MemoryProvider 仍恒被选中（阈值没动）
- **位置**：`internal/knowledge/planner/default.go:137` → `providers := d.registry.Select(intent, 0.1)`
- **现状**：commit 把 `MemoryProvider.IntentMatch` 的最低分从更高值降到了 **0.3**（`provider/memory/provider.go:54`，code/architecture 分支返回 0.3）。但选择阈值是 **0.1**，于是 `0.3 > 0.1` 永远成立 → **MemoryProvider 对任何 query 都恒选中**，包括它最不擅长的 code/architecture 查询。
- **根因**：修错了变量——该动的是阈值（抬高到比如 0.5），却只降了分数。结果：memory 对象继续污染所有查询，反而更糟（低相关 memory 噪声被引入 code 检索）。
- **修复**：把 `Select(intent, 0.1)` 的阈值提到合理值（如 `0.5`），或由 `Select` 的调用方按 intent 类型动态设阈值；并补一条"code 查询不应选中 memory provider"的断言测试。

### B16 · 大图被砍到 1 个节点（下限没动）
- **位置**：`internal/knowledge/runtime/components.go:70-72`
  ```go
  maxNodes := budget.ForGraph / estTokensPerNode
  if maxNodes <= 0 {
      maxNodes = 1   // ← 原封不动
  }
  ```
- **现状**：commit 把冒泡排序换成了 `sort.Slice`（`:90`，纯性能/可读性优化），但 `ForGraph <= 0` 时 `maxNodes` 仍被保底成 `1`。即预算为 0 时，整张图只剩 1 个节点——根因未触。
- **修复**：预算缺失时应给出**有意义的默认上限**（如 `len(graph.Nodes)` 或固定常量如 50/100），而不是 1；或显式要求 `budget.ForGraph` 必填并校验。

### B17 · Offset 分页逻辑本身有 bug（修复即引入缺陷）
- **位置**：`internal/knowledge/store/memory/store.go:108`
  ```go
  if q.Offset > 0 && q.Offset < len(result) {
      result = result[q.Offset:]
  }
  ```
- **现状**：当 `q.Offset >= len(result)`（请求超出结果集，本应返回空页）时，条件 `q.Offset < len(result)` 为假 → offset **被静默忽略**，函数把**整页数据原样返回**。也就是说"翻到第 3 页但总共只有 1 页"时，你拿到的是第 1 页的数据，而不是空。
- **修复**：
  ```go
  if q.Offset > 0 {
      if q.Offset >= len(result) {
          result = result[:0]   // 越界返回空
      } else {
          result = result[q.Offset:]
      }
  }
  ```
  顺带注意 `result = result[:q.Limit]`（`:104`）后再切片 offset 的顺序是对的，但 SQLite/Postgres store 的 offset 实现需一并核对保持一致。

---

## 3. 🟢 本次 commit 真正修好的部分（肯定的）

| 编号 | 问题 | 修复情况 | 证据 |
|------|------|----------|------|
| B6 | Provider 发 nil 对象 → `objects[obj.ID]` panic | ✅ 已修：`pipeline.go:106` 加了 `if obj == nil` 早返回 | pipeline.go:106 |
| B8 | Pipeline 从未接线（死代码） | ✅ 已接活：`runtime.go:178-184` 真调 `r.pipeline.Process` | runtime.go:180 |
| B1 | Resolver 失效（candidates 传 nil + break） | ✅ 已修：`pipeline.go:132-136` 现在用 `p.resolvedObjects` 作候选池 | pipeline.go:132 |
| B7 | Store.Get 契约不一致 | 🟡 部分：`memory/store.go:52` 现返回 `ErrObjectNotFound`（一致了），但需核对所有调用方是否正确处理 error 而非只判 nil | store.go:52 |
| B3 | MySQL SQL 注入 | ✅（按 diff：对表名/列名做了引号转义） | 见 diff mysql/provider.go |
| B4 | MySQL 无 LIMIT / ID 无命名空间 | ✅（按 diff：加了 LIMIT 与 Namespace 前缀） | 见 diff mysql/provider.go |
| B11 | Compiler 缺 XML/ToolSchema | ✅（按 diff：补了 XML + ToolSchema 两种格式） | 见 diff compiler/compiler.go |
| B21 | MCP 只暴露 2/4 工具 | ✅（按 diff：`query_knowledge` + `distill_memory` 已注册） | 见 diff mcp/mcp.go |
| B5 | ArchitectureLinker 全连接 | ✅（按 diff：缺 tag 时不再无脑加 depends_on） | 见 diff linker/architecture.go |
| B9 | Postgres 空 TimeColumn | ✅（按 diff：TimeColumn 为空时跳过时间排序） | 见 diff postgres/provider.go |

> 注：带"见 diff"的项是我从 commit diff 比对确认、未单独跑该 provider 集成测试（postgres / mysql 无单测文件），建议补集成测试坐实。

---

## 4. 🟡 测试与工程质量观察

- **假绿风险**：常规 `go test ./internal/knowledge/...` 全过，但 **`-race` 下 runtime 包 FAIL**（见 B22）。CI 必须加 `-race`。
- **测试覆盖缺口**：
  - `provider/memory/provider.go` **无测试文件** —— 偏偏是 B10 的当事方。
  - `provider/postgres/provider.go` **无测试文件** —— B9 修复无单测守护。
  - `mysql/provider_test.go`(+143)、`planner_test.go`、`code/provider_test.go`、`sqlite/store_test.go` 已加，但需确认它们是**行为断言**而非"跑通不崩"型（后者仍是假绿）。
- **行为断言建议**：
  - 加一条测试：给定 code 类 intent，断言 `registry.Select` 结果**不含** MemoryProvider（守 B10 根因）。
  - 加一条 runtime 并发测试：2+ provider 并发加载，跑 `-race` 必须绿（守 B22）。
  - 加一条 store offset 测试：offset ≥ 总数时应返回空（守 B17）。

---

## 5. 整改清单（按优先级）

| 优先级 | 项 | 动作 |
|--------|----|------|
| P0 | B22 数据竞争 | 阻断合入；按 §1 方案 b 重构并发路径；CI 加 `-race` |
| P0 | B17 offset bug | 按 §2 改 store.go:108，补越界单测 |
| P1 | B10 阈值 | `Select(intent, 0.1)` → 抬高阈值或动态阈值；补断言测试 |
| P1 | B16 下限 | `maxNodes=1` → 有意义默认值；补预算缺失单测 |
| P2 | B7 调用方 | 核对所有 `Store.Get` 调用方正确处理返回的 error |
| P2 | 测试缺口 | 补 memory/postgres provider 单测；把行为断言做实 |

---

## 6. 总结

`0f9aa9d` 是个"方向对、执行糙"的提交：把流水线接活、把明显崩溃堵上，值得肯定；但它**在修 B8 时把共享状态暴露进了并发路径，制造了一个 race-condition 炸弹**，同时 B10/B16/B17 是"换个写法、根因还在"的典型。建议打回，先把 B22 和 B17 这两个确定性缺陷修掉、CI 挂上 `-race`，再回头用根因法处理 B10/B16。

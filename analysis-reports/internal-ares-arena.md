# 模块分析报告：`internal/ares_arena`（竞技场 / 回归测试）

> 分析范围：`internal/ares_arena/` 全部非测试文件
> `regression.go` 为最近有大量未提交改动（+80/-10）的文件，重点审查。

---

## BUG（高置信度）

### 1. `runStrategy` 并发评分时 `len(scores) == 0` 检查永远不触发，且取消时返回不完整数据
- **文件**：`regression.go`，381-445 行
- **位置**：`runStrategy` 并行评分路径（381-445 行）
- **说明**：
  - `scores := make([]float64, n)`（381 行），而 `n > 0` 已在上方 363-365 行保证，因此 442-444 行的 `if len(scores) == 0 { return nil, ErrEmptyScores }` **永远不可能为 true**，是死代码。
  - 更关键：当 `runCtx` 被取消时（407-409 行），goroutine 直接 `return` 而不写入对应槽位，`scores` 中该位置保持默认值 `0.0`。函数最终（433-445 行）返回的 `scores` 是**被 0 填充的不完整结果**，而不是报错。调用方会误把未运行的 run 当作"得分为 0"。正确做法应统计成功写入的槽位数量，若 `< n` 则返回错误。
- **状态**：✅ 已核实修复（2026-08-14）——当前实现统计 `completed` 槽位数，`if completed != n { return nil, fmt.Errorf("arena: incomplete score set: scored %d/%d runs", ...) }`（526-528 行），取消/出错时返回错误而非 0 填充；`len(scores)==0` 死检查已移除。

### 2. `executeToolCalls` 中拒绝的工具调用被计入消息但仍可能重复执行
- **文件**：`regression.go`（此为 ares_arena 内非本文件的逻辑，见下）
- **说明**：此条属于 `agentloop` 模块，已在 agentloop 报告中分析。**（此处略去。）**

### 3. `computeWinRate` 使用 `>=` 将平局算作"新策略获胜"
- **文件**：`regression.go`，502-518 行
- **说明**：`if newScores[i] >= oldScores[i] { wins++ }`。注释声明"fraction where new >= old"，即平局计入新策略获胜。这在 A/B 测试中通常应使用 `>`（平局不计入任何一方）以避免高估新策略，属逻辑选择问题。**（标注：设计权衡，若意图为严格胜率应改 `>`。）**

---

## LOGIC（逻辑问题）

### 4. `buildResult` 中 `Samples` 字段使用配置值而非实际得分长度
- **文件**：`regression.go`，455-458 行
- **说明****：`samples := cfg.BaselineRuns; if cfg.CompareRuns < samples { samples = cfg.CompareRuns }`。在**自适应模式**（`runAdaptive`）下，最终 `oldScores[:n]` / `newScores[:n]` 可能因提前停止被裁剪得比配置的 `BaselineRuns`/`CompareRuns` 更短，但 `Samples` 仍报告配置值，与实际的 `len(oldScores)`/`len(newScores)` 不符，导致返回结果自相矛盾。
- **状态**：✅ 已核实修复（2026-08-14）——`buildResult` 现用 `samples := len(oldScores); if len(newScores) < samples { samples = len(newScores) }`（543-546 行），反映实际评分样本数，注释说明自适应模式裁剪。

### 5. `MinWinRate` 配置项被校验、设置默认值，但从未在逻辑中使用
- **文件**：`regression.go`，60 行（字段）、181-184 行（默认值）、702-704 行（校验）
- **说明**：`MinWinRate` 在整个文件中只被赋值和校验，`buildResult`（449-473 行）从未根据它判断"新策略是否优于旧策略"。这是一个**死配置**——用户设置 `MinWinRate` 不会对结果产生任何影响。要么应将其纳入 `buildResult` 的判定逻辑，要么应删除该字段。
- **状态**：✅ 已核实修复（2026-08-14）——`buildResult` 现设 `NewBetter: winRate >= cfg.MinWinRate`（560 行），`MinWinRate` 已纳入判定（进化门可用），死配置复活。

### 6. `runAdaptive` 中提前停止阈值 `p > 0.5` 的"无望"判定过于激进
- **文件**：`regression.go`，337 行
- **说明**：`if pVal < cfg.Confidence || pVal > 0.5 { break }`。当 p 值刚超过 0.5 时即判定"无望"并停止，样本量很小时 p 值波动大，可能在统计功效不足时就过早停止，产生错误结论。属统计方法学问题，需结合实际样本量权衡。

### 7. `runStrategy` 中 `return nil, err` 的 `err` 可能是 nil
- **文件**：`regression.go`，392-395 行
- **说明**：`if err := runCtx.Err(); err != nil { return nil, err }` 正确。但在 393-394 行该判断本身没问题。真正需注意的是 436-438 行 `if err := ctx.Err(); err != nil { return nil, err }`——若 ctx 未取消，`err` 为 nil，不会返回。逻辑正确，仅作确认。**（无问题。）**

---

## 其他：并行评分正确性

### 8. `runStrategy` 并发写入 `scores` 有锁保护，但 `errOnce`/`runErr` 的返回时序
- **文件**：`regression.go`，381-445 行
- **说明**：`mu.Lock()` 保护 `scores[i] = score`（428-430 行），无数据竞争。但 `runErr` 由 `errOnce.Do` 设置并 `runCancel()`（422-425 行），随后 `wg.Wait()`（433 行）等待所有 goroutine 结束。若某 goroutine 因 `runCtx.Err()` 提前 return（407-409 行），该槽位为 0，与第 1 条问题相同——**不完整数据被当作 0 分返回**。此条与 #1 是同一根因。

---

## 统计实现备注

### 9. `approximatePValue` / `regularizedBeta` 实现质量
- **文件**：`regression.go`，574-660 行
- **说明**：使用 Lentz 连分数实现正则化不完全 beta 函数，公式与 Abramowitz & Stegun 一致，`df > 30` 用正态近似。实现较严谨。唯一潜在风险是 `front := math.Exp(...) / a`（614 行）当 `a = df/2` 较小、x 接近 1 时可能下溢，但此处有 `x > (a+1)/(a+b+2)` 的对称分支保护，风险可控。**（无实质 bug。）**

---

## 结论

`internal/ares_arena`（尤其 `regression.go`）主要问题：

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 高 | `runStrategy` 381-445 | 取消/出错时返回被 0 填充的不完整 scores；`len(scores)==0` 检查是死代码 |
| 中 | `buildResult` 455-458 | `Samples` 报告配置值而非实际得分长度（自适应模式下不一致） |
| 中 | `MinWinRate` 字段 | 被设置但从未参与判定，属死配置 |
| 低 | `computeWinRate` 513 | 平局计入新策略获胜（`>=`），可能高估新策略 |
| 低 | `runAdaptive` 337 | `p > 0.5` 即提前停止，样本少时可能过早下结论 |

**特别注意**：`regression.go` 是本仓库中唯一有未提交改动的文件，其中的自适应模式（`runAdaptive`）、beta 函数重写等改动引入了上述 #4、#5、#6 问题，建议在提交前复查 `MinWinRate` 的未使用问题。

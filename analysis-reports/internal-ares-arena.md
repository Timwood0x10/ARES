# 模块分析报告：`internal/ares_arena`（竞技场 / 回归测试）

> 分析范围：`internal/ares_arena/` 全部非测试文件
> `regression.go` 为最近有大量未提交改动（+80/-10）的文件，重点审查。

---

## BUG（高置信度）

### 2. `executeToolCalls` 中拒绝的工具调用被计入消息但仍可能重复执行
- **文件**：`regression.go`（此为 ares_arena 内非本文件的逻辑，见下）
- **说明**：此条属于 `agentloop` 模块，已在 agentloop 报告中分析。**（此处略去。）**

### 3. `computeWinRate` 使用 `>=` 将平局算作"新策略获胜"
- **文件**：`regression.go`，502-518 行
- **说明**：`if newScores[i] >= oldScores[i] { wins++ }`。注释声明"fraction where new >= old"，即平局计入新策略获胜。这在 A/B 测试中通常应使用 `>`（平局不计入任何一方）以避免高估新策略，属逻辑选择问题。**（标注：设计权衡，若意图为严格胜率应改 `>`。）**

---

## LOGIC（逻辑问题）

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

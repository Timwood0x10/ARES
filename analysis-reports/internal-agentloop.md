# 模块分析报告：`internal/agentloop`（核心 Agent 循环引擎）

> 分析范围：`internal/agentloop/engine.go`（核心 ReAct 工具调用循环）、`doc.go`
> 本模块是 Agent 执行的调度核心，负责 LLM 调用、工具执行、事件发射与记忆持久化。

---

## BUG（潜在正确性问题）

### 1. 达到 `maxIter` 上限时未发射 `TaskCompleted` 事件
- **文件**：`engine.go`
- **位置**：`Run()` 方法，`for iter := 0; iter < maxIter; iter++` 循环结束后（约 263-271 行）
- **说明**：当循环达到迭代上限（`maxIter`）而没有得到最终答案时，只返回 `Result{Output: "max iterations reached"}`，**没有**调用 `emitTaskCompleted`。而正常完成路径（243-247 行）会发射 `TaskCompleted` 事件。这意味着在超时/超迭代情况下，`TaskCompleted` 事件永远不会发出，下游的蒸馏（distill）流程拿不到"任务完成"信号，可能造成任务结果丢失。
- **状态**：✅ 已核实修复（2026-08-14）——`maxIterationsReachedMsg`/`maxTokensReachedMsg` 两条超限路径均调用 `emitTaskCompleted`（engine.go 354、279 行），报告条目过时。

### 2. `FriendlyErr` 中 hint 表每次调用都重建
- **文件**：`engine.go`，`FriendlyErr()`（490-502 行）
- **说明**：每次错误产生都重建 `hints map`。功能上无错误，但属于轻微性能/风格问题，非真正的 bug。**（标注：低优先级）**

---

## LOGIC（逻辑问题）

### 3. `req.MaxIter` 为 0 时使用默认值，但语义上"0 表示无限"无法表达
- **文件**：`engine.go`，211-214 行
- **说明**：`maxIter <= 0` 回退到 `DefaultMaxIterations = 10`。若调用方希望"完全禁用迭代上限"（无限循环），无法通过 `MaxIter=0` 表达，会被强制限制为 10 次。这是设计取舍，但注释（"`<=0` falls back to DefaultMaxIterations"）与"0 表示默认"的常见约定一致，此处仅为提示。

### 4. 记忆持久化仅保存 `resp.Content`，丢失工具调用信息
- **文件**：`engine.go`，238-240 行
- **说明**：`e.Memory.AddMessage(ctx, req.SessionID, roleAssistant, resp.Content)` 只保存 `Content`，而助手消息中的 `ToolCalls` 没有被持久化。后续从记忆中重建上下文时，会丢失"助手曾经调用过哪些工具"的轨迹，可能影响多轮对话的上下文完整性。**（取决于记忆消费方是否依赖 ToolCalls 字段，属潜在不一致。）**
- **状态**：✅ 已修复（2026-08-14）——新增 `structuredMemorySink` 可选接口 + `toMemToolCalls` 转换；assistant 消息带 ToolCalls 时经 `AddStructuredMessage` 持久化（含 Time/ToolCalls），不支持结构化消息的 mock 回退 `AddMessage`（content-only）。

---

## DEAD_CODE（死代码）

### 5. 模块级 `log` 变量的唯一用途
- **文件**：`engine.go`，20 行
- **说明**：`log = logger.Module("agentloop")` 仅用于 `emitToolEvent` / `emitTaskCompleted` 中的 `log.Warn(...)`（429、459 行），用途正常，非死代码。**（此处确认无死代码。）**

---

## 其他观察（不构成 bug）

- `parseArgs`（475-484 行）对非法 JSON 静默返回 nil，与 SDK 行为一致，可接受。
- `toolNameInSet`（396-403 行）线性扫描去重，量小可接受。
- `Run` 中对 `req.Messages` 和 `req.Tools` 的拷贝（202-209 行）正确避免了调用方切片被污染，实现良好。

---

## 结论

`internal/agentloop` 整体实现质量较高，主要关注点为：

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 中 | `Run()` 循环末尾 | 达到迭代上限时未发射 `TaskCompleted`，下游蒸馏信号缺失 |
| 低 | 238-240 行 | 记忆持久化丢失 ToolCalls 信息 |
| 低 | `FriendlyErr` | hint map 每次重建（风格） |

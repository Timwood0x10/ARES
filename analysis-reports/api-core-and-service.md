# 模块分析报告：`api/core` 与 `api/service`

> 分析范围：`api/core/`（28 个文件）、`api/service/`（32 个文件）

---

## api/service/agent

### 1. `service.go` `CreateAgent` 丢弃 `config.Config`（数据丢失）
- **位置**：`agent/service.go` 47-63 行
- **说明**：`*core.AgentConfig` 上的 `config.Config`（`map[string]interface{}`）被**完全忽略**——从不转发给 `s.inner.CreateAgent(ctx, config.ID, config.Name, config.Type)`，也从不填充到返回的 `core.Agent`。调用方提供的配置被静默丢弃。`GetAgent`/`UpdateAgent`/`ListAgents` 同样不填充返回的 `core.Agent.Config` 字段。

### 2. `service.go` `ListAgents` 仅设 `Page` 时返回错误页
- **位置**：`agent/service.go` 159-162 行
- **说明**：当 `filter.Pagination` 只设 `Page`（`Page>0` 但 `PageSize==0`），需要 `Page>0 && PageSize>0` 的分支被跳过，回退到 `offset=0`、`limit=20`。请求第 2 页（未显式给 page size）会静默返回第 1 页内容。

### 3. `service.go` `GetTaskResult` 返回共享可变指针
- **位置**：`agent/service.go` 247-255 行
- **说明**：直接返回缓存的 `*core.TaskResult` 指针（无拷贝）。调用方修改返回值会修改共享缓存值，与并发读取/`GetTaskResult` 竞争。低优先级但真实共享状态暴露。

---

## api/service/eval

### 4. `service.go` `ExactMatch.Evaluate` 丢弃 `TestCase.Timeout`
- **位置**：`eval/service.go` 23-31 行
- **说明**：公开的 `TestCase.Timeout`（`time.Duration`）从不拷贝到内部 `internal.TestCase`（其有 `Timeout` 字段）。委托给内部评估器时配置的逐测试超时被静默丢弃，超时永不生效。

---

## api/service/llm

### 5. `service.go` `toInternal` fallback 转换丢字段
- **位置**：`llm/service.go` 36-41 行
- **说明**：转换 `Config.Fallbacks` 时只拷贝 `Provider/Model/BaseURL/APIKey/Timeout/MaxTokens`，`Temperature`、`TopP`、`FrequencyPenalty`、`PresencePenalty`、`MaxPromptLength` 被静默丢弃。fallback LLM 用默认（未设置）的采样/限制运行，与主配置不一致。

---

## api/service/workflow

### 6. `service.go` `ExecuteStream` 忽略配置的超时（BUG）
- **位置**：`workflow/service.go` 285-303 行
- **说明**：与 `Execute`（119-128 行）不同，流式路径从不应用 `req.Timeout` 或 `s.config.RequestTimeout`。通过 `ExecuteStream` 执行的工作流忽略所有配置的超时，可无限运行。

### 7. `service.go` `executeStreamWithRunner` 未 await errgroup，执行错误丢失
- **位置**：`workflow/service.go` 188-194 行
- **说明**：goroutine 返回 `execErr`（`runner.ExecuteBound` 的错误），但 `group.Wait()` **从不调用**，执行错误被整体丢弃。调用方只收到事件通道；若 runner 失败但没发 `WorkflowEventFailed`，失败原因丢失。

### 8. `service.go` 未完成步骤的时长计算为无效值
- **位置**：`workflow/service.go` 212、251 行
- **说明**：`ns.FinishedAt.Sub(ns.StartedAt)` 用于每一步。若步骤未完成（工作流失败/取消时步骤仍 pending/running），`FinishedAt` 是零值 `time.Time`，产生极大负值或巨大 duration。无零值 guard。

### 9. `service.go` `mapEngineStatus` 废弃但仅测试用
- **位置**：`workflow/service.go` 405-435 行
- **说明**：标注 `Deprecated`，仅 `service_test.go` 引用，生产执行路径全用 `mapRunnerStatus`。

---

## api/service/events

### 10. `service.go` `Subscribe` 转发 goroutine 可能泄漏
- **位置**：`events/service.go` 163-173 行
- **说明**：转发 goroutine 阻塞在 `ch <-`（缓冲 1）直到 `ctx.Done()`。若消费者停止排空且 `ctx` 不取消、内部 channel 不关闭，goroutine 泄漏。`select` 上的 `ctx.Done()` 缓解但消费侧无超时/所有权强制。

### 11. `errors.go` `ErrInvalidEvent` / `NewErrInvalidEvent` 死代码
- **位置**：`events/errors.go` 7、10 行
- **说明**：哨兵错误及其构造器无任何外部引用。

---

## api/core（死代码）

### 12. `factory.go` 4 个默认构造器仅测试用
- **位置**：`core/factory.go` 6、18、26、35 行
- **说明**：`DefaultEvolutionConfig`、`DefaultArenaConfig`、`DefaultDreamCycleConfig`、`DefaultRuntimeConfig` 仅 `core_test.go` 引用。

### 13. `types.go` 方法仅测试用
- **位置**：`core/types.go` 73、87、98、110 行
- **说明**：`NewRequestContext`、`WithUserID`、`WithTraceID`、`WithMetadata` 仅 `types_test.go` 引用。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `workflow/service.go` 285 | ExecuteStream 忽略超时，可无限运行 |
| **高** | `workflow/service.go` 188 | errgroup 未 await，执行错误丢失 |
| 中 | `agent/service.go` 47 | CreateAgent 丢弃 config.Config |
| 中 | `agent/service.go` 159 | 仅设 Page 返回错误页 |
| 中 | `eval/service.go` 23 | 丢弃 TestCase.Timeout |
| 中 | `llm/service.go` 36 | fallback 丢采样/限制字段 |
| 低 | 多处 | 共享指针、时长无效、事件 goroutine 泄漏风险、死代码 |

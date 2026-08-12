# 模块分析报告：`internal/monitoring`（监控）

> 分析范围：`internal/monitoring/`（60 个 Go 文件），含 dag/、tabs/、data/、eventutil/ 等子包

---

## BUG（高置信度）

### 1. `eventutil.go` `ExtractAgentID` 潜在 nil 解引用
- **位置**：`eventutil/eventutil.go` 163 行
- **说明**：`ExtractAgentID` 在 `ExtractString(evt, "agent_id")` 返回 `""` 后 `return evt.StreamID`。`ExtractString` 对 **nil** `evt` 返回 `""`（有 guard），于是回退 `return evt.StreamID` 会解引用 nil 指针而 panic。当前调用方都恰好在 nil 上做了 guard，但这是公共 helper 的潜在 nil-deref bug。

---

## LOGIC（逻辑问题）

### 2. `dag/engine.go` `handleTaskCreated` 直接创建 Running 状态，绕过校验
- **位置**：`dag/engine.go` 379 行
- **说明**：task "created" 事件直接用 `Status: StatusRunning` 创建节点，绕过 `validateAndTransition`，从不使用 `StatusPending`。与 `handleAgentStarted`（正确从 `StatusPending` 开始）不一致。"created" 任务不应以 running 开始。

### 3. `tabs/workflow_tab.go` `handleTaskCreated` 同样直接 Running
- **位置**：`tabs/workflow_tab.go` 94 行
- **说明**：与 #2 相同的不一致，task "created" 事件直接设 `WorkflowExecution.Status = dag.StatusRunning`，应为 pending。

### 4. `data/agent_tracker.go` `handleLLMCall` 硬编码 USD
- **位置**：`data/agent_tracker.go` 196 行
- **说明**：`Currency: "USD"` 硬编码，从不读事件 payload 的 `currency` 字段。与 `cost_bar.go`（正确追踪货币且只对同币种求和）不一致。非 USD 事件的成本在此被错误记为 USD，产生与 `CostBar` 不同的成本数字。

### 5. `dag/engine.go` `handleAgentStarted` failed→running 转换被吞
- **位置**：`dag/engine.go` 321 行
- **说明**：当节点已存在（如 agent 之前转为 `failed`），转换到 `StatusRunning` 会经 `validTransitions` 校验，而它没有 `StatusFailed` 的条目。状态更新被静默跳过（转换错误被吞），失败的 agent 再发 `agent.started` 事件不会被反映为运行中。

### 6. `http_api.go` `handleSubscribe` 忽略首次快照写错误
- **位置**：`http_api.go` 409 行
- **说明**：`s.writeSSESnapshot(...)` 的返回值被忽略。若首次快照写失败（客户端已断开），循环仍进 ticker 而不立即返回，浪费周期直至下次失败。

---

## DEAD_CODE

### 7. `detail_panel.go` `HandleEvent` 是空操作
- **位置**：`detail_panel.go` 138-159 行
- **说明**：`DetailPanel.HandleEvent` 读取 viewed agent、提取事件 agent ID、比较，然后**什么都不做**（注释描述的本应实现的 UI 刷新从未实现）。`SetViewedAgent`（78-82）仅测试引用。

### 8. `plugin.go` `WithCostAlertThreshold` 是空操作
- **位置**：`plugin.go` 120-122 行
- **说明**：函数体为空，返回空闭包。仅测试调用。死/桩配置。

### 9. `plugin.go` `Events`/`CostAlerts`/`Interactions`/`MCPToolCalls` 桩
- **位置**：`plugin.go` 306-313、334-337、392-394、430-434 行
- **说明**：总是返回 `ErrNotConfigured`，从未接真实存储。任何 ConsoleAPI 消费者调用总是报错。

### 10. `data/cost_aggregator.go` `SetAlert`/`CheckAlerts` 仅测试用
- **位置**：`data/cost_aggregator.go` 100-139 行
- **说明**：成本告警生产无人用。

### 11. `tabs/event_tab.go` `FilterByType`/`FilterByAgent` 仅测试用
- **位置**：`tabs/event_tab.go` 74-97 行
- **说明**：生产未用。

### 12. `publisher.go` 交互引擎从未接线
- **位置**：`publisher.go` 117-121、246-300 行
- **说明**：`plugin.go` `NewConsole` 只传 `WithInterval`/`WithHub`，`WithInteractionEngine` 从不应用，`p.publisher.interEngine` 保持 nil。`executeNodeAction`/`HandleKillAgent`/`HandleResumeAgent`/`HandleRetryAgent` 总是返回 501 "interaction engine not wired"。`BroadcastActionResult`（177）和 `dag/interaction.go` `SetPublisher` 生产也从未连接。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 中 | `eventutil.go` 163 | ExtractAgentID 潜在 nil 解引用 |
| 中 | `dag/engine.go` 379 / `tabs/workflow_tab.go` 94 | task created 直接 Running |
| 中 | `data/agent_tracker.go` 196 | 硬编码 USD，成本归因错误 |
| 低 | `dag/engine.go` 321 | failed→running 转换被吞 |
| 低 | 多处 | 大量死代码（detail_panel、plugin 桩、cost_aggregator、event_tab、publisher） |

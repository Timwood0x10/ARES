# 模块分析报告：`internal/ares_mcp`（MCP 集成）

> 分析范围：`internal/ares_mcp/`（25 个 Go 文件），含 MCP 客户端/服务端/传输

---

## BUG（高置信度）

### 1. `manager.go` 配置变更检测不完整（仅比较 Command/Args）
- **位置**：`manager.go` `hasConfigChanged`
- **说明**：stdio 配置比较只检查 `Command` 和 `Args`，**不比较 `Env` 和 `WorkDir`**。仅修改 Env/WorkDir 不会触发重连，导致配置变更被忽略。

### 2. `server.go` 未使用的 `handlerTimeout` 功能
- **位置**：`server.go`
- **说明**：`WithHandlerTimeout` 定义但整个代码库无调用方（仅定义，无调用点）。死配置选项。

---

## LOGIC（逻辑问题）

### 3. `client.go` `ListTools` 返回的 `&result.Tools[i]` 指针
- **位置**：`client.go` `ListTools`
- **说明**：返回指向局部 `result` 切片的元素指针，该切片作为返回值返回，生命周期有效，但若调用方在别处持有并期望独立拷贝，共享底层数组。低优先级。

### 4. `manager.go` `Version` 字段被填入 "connected" 状态字符串
- **位置**：`manager.go` `ListServers`
- **说明**：`ListServers` 把 `Version` 字段设为 `"connected"`（一个状态字符串），而不是实际的服务版本。字段用途被误用，观察/审计时得到误导性值。

### 5. `server.go` / `schema.go` 属性 `Items` 字段未被处理
- **位置**：`schema.go` `convertProperty`
- **说明**：JSON Schema 转换未处理 `Items`（数组元素 schema），导致数组类工具参数的结构定义丢失。`Items` 字段实际是死的。

### 6. `server.go` 硬编码 30s 超时忽略配置
- **位置**：`factory.go` 105 行
- **说明**：`context.WithTimeout(context.Background(), 30*time.Second)` 硬编码 30 秒，忽略配置项。

---

## DEAD_CODE

### 7. `RegisteredTools` 等导出方法仅在特定入口使用
- **位置**：`server.go`
- **说明**：`RegisteredTools` 由 `cmd/ares/mcp.go` 使用（活跃）。`MCPTName` 仅测试使用（导出 API，边界）。

---

## 传输层备注（Stdio/SSE）

- `transport_stdio.go` `Receive` 中 `Close` 持有 `t.mu` 调 `t.cmd.Wait()`，而 `Receive` 的 goroutine 阻塞在 `t.stdout.Scan()`。`Close` 通过 `closeStdoutPipe()` 解除 Scan 阻塞，避免死锁。经分析两者锁互斥且不交叉，**无死锁**（确认）。
- `transport_sse.go` 默认分支尝试把任意非 endpoint/message 事件按 JSON-RPC 解析，与 message 处理冗余但非 bug。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 中 | `manager.go` hasConfigChanged | 配置变更检测漏 Env/WorkDir |
| 中 | `schema.go` convertProperty | Items（数组 schema）未处理 |
| 低 | `manager.go` ListServers | Version 字段填入 "connected" 状态串 |
| 低 | `factory.go` 105 | 硬编码 30s 超时 |
| 低 | `server.go` | WithHandlerTimeout 无调用方 |

# 模块分析报告：`internal/ares_mcp`（MCP 集成）

> 分析范围：`internal/ares_mcp/`（25 个 Go 文件），含 MCP 客户端/服务端/传输

---

## BUG（高置信度）

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

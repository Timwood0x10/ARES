# 模块分析报告：`internal/ares_bootstrap` 及辅助 ares_* 小模块

> 分析范围：`internal/ares_bootstrap/`（36 个文件）及 `ares_config`、`ares_ctxutil`、`ares_callbacks`、`ares_shutdown`、`ares_ratelimit`、`ares_security`

---

## BUG（高置信度）

---

## LOGIC（逻辑问题）

### 2. `provide_mcp.go` 无服务器路径不启动
- **位置**：`provide_mcp.go` 56-61 行
- **说明**：当 `len(cfg.Servers) == 0` 时构造 manager 但从不调 `Start`，而配置了服务器路径会 `mcpMgr.Start(ctx)`。空 case 的 manager 永不启动，但 Bootstrap 注册了调用 `m` 的 cleanup，启动路径不一致。

---

## DEAD_CODE

### 3. `provide_llm.go` 多个后向兼容包装器生产未用
- **位置**：`provide_llm.go` 50-73 行
- **说明**：`NewCallbackRegistry`、`NewLLMClientWithCallbacks`、`WireTaskExecutorCallbacks`、`WireLeaderAgentCallbacks` 仅自身文件及测试引用，无生产调用方。（`SetupMCP` 由 `cmd/monitor-live/main.go` 使用，是活跃的。）

### 4. `provide_dashboard.go` `_ = hubGrp.Wait()` 丢弃错误
- **位置**：`provide_dashboard.go` 62 行
- **说明**：`hubGrp.Wait()` 在 `hub.Stop()` 后返回 `hubCtx.Err()`（context.Canceled），被 `_ =` 丢弃。经分析是有意的（shutdown 路径），标注确认。

---

## 其它辅助模块备注

- **ares_shutdown**：`CallbackRegistry`/`PhaseExecutor`/`SignalHandler` 大部分是死代码；`Manager` 在 serve.go 中活跃（`RegisterPhase`/`AddCallback`/`StartShutdown`）。`StartShutdown`/`executePhase` 的 WaitGroup 复用潜在问题见 `internal-llm-and-utils.md`。
- **ares_config、ares_ctxutil、ares_callbacks、ares_ratelimit、ares_security**：未发现可验证的高置信 bug（rate limit 的 `Burst` 忽略见 utils 报告）。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 中 | `provide_mcp.go` 56 | 无服务器路径不启动 |
| 低 | `provide_llm.go` 50 | 多个兼容包装器生产未用 |

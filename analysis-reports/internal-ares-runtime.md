# 模块分析报告：`internal/ares_runtime`（运行时 / 插件总线 / 混沌）

> 分析范围：`internal/ares_runtime/`（44 个 Go 文件）

---

## LOGIC（逻辑问题）

---

## DEAD_CODE

### 5. 内置插件/路由器子系统生产未用
- **位置**：`checkpoint.go`、`loop.go`、`observer.go`、`interrupt.go`、`tool.go`、`recovery.go`、`router*.go`
- **说明**：`NewCheckpointPlugin`、`NewArenaPlugin`、`NewObserverPlugin`、`NewInterruptPlugin`、`NewToolPlugin`、`NewLoopPlugin`、`NewBasicRecoveryPlugin` 及所有 router 实现（`NewExpressionRouter`、`NewMemoryRouter`、`NewEvolutionRouter`、`NewFallbackRouter`）在生产无调用方。生产只实例化 `PluginBus`（`cmd/ares/serve.go` 等）但不注册这些内置插件/router，也不调 `BeforeStep`/`AfterStep`；生产用独立的 `internal/monitoring.MonitorPlugin`。这些约 11 个导出构造器是生产死代码。

### 6. `manager_chaos.go` `UnwrapAgent` / `UnwrappableAgent` 生产未用
- **位置**：`manager_chaos.go` 315-339 行
- **说明**：导出但无调用方（内部 `chaosUnwrap` 用于 manager.go，但 `UnwrapAgent` 从不调用）。

---

## 核心（活跃）部分确认

`bus.go`、`manager.go`、`manager_lifecycle.go`、`collector.go`、`runtime.go`、`errors.go`、`events.go`、`constants.go`、`executable.go`、`options.go`、`log.go`、`plugin.go`、`types.go` 构成活跃生产核心（`PluginBus` 作 EventBus、`Manager` 生命周期、`ExecutionCollector` 供 `internal/workflow` 使用）。未发现可验证的真 bug。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 中 | `arena.go` 17/124 | FaultBusStop 可调度但不处理 |
| 中 | `arena.go` 33-65 | ArenaPlugin 不订阅总线（与文档不符） |
| 中 | `outcome_recorder.go` 108 | 分数可超出 [0,1] |
| 低 | `checkpoint.go` 249 | 双锁 TOCTOU |
| 低 | router*.go | 整个内置插件/路由器子系统生产未用 |
| 低 | `manager_chaos.go` 315 | UnwrapAgent 无调用方 |

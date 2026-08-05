# Bug: system_runtime shutdown 忽略调用方上下文超时

- **ID**: BUG-SR-001
- **严重度**: P1 / High
- **状态**: 已修复
- **日期**: 2026-08-04
- **涉及包**: `internal/system_runtime`

## 现象

`Orchestrator.Shutdown(ctx)` 中，`stopComponent` 和 `cleanupComponent` 在调用无 context 参数的
`Waiter.Wait()` 时，只使用硬编码的 `stopTimeout`（30s）进行超时保护，**完全忽略调用方传入的
`ctx` 截止时间**。

因此，当调用方以更短的 deadline（如 5s）调用 Shutdown，而某个组件忽略取消、其 `Wait()` 永久阻塞时，
Shutdown 仍会阻塞满 30s 才返回，违背"所有边界条件必须显式处理（超时）"的规则，可能导致进程退出被
长时间拖住。

## 触发条件

- 任一已启动组件实现 `Waiter`，且其 `Wait()` 不响应根 context 取消（不监听 `ctx.Done()`）；
- 调用方为 `Shutdown` 提供的上下文 deadline 短于 `stopTimeout`（30s）。

## 根因

`orchestrator.go` 的 Wait 分支：

```go
select {
case waitErr := <-waitCh:
    ...
case <-time.After(stopTimeout):
    log.Warn("system_runtime: wait timed out", ...)
}
```

缺少对调用方 `ctx.Done()` 的分支，导致短 deadline 无法被响应。

## 修复

在 `stopComponent` 与 `cleanupComponent` 的 Wait select 中增加 `case <-ctx.Done()` 分支，
使 Shutdown 能尊重调用方的截止时间而提前返回（优雅中止，不产生组件错误）：

```go
select {
case waitErr := <-waitCh:
    ...
case <-time.After(stopTimeout):
    log.Warn("system_runtime: wait timed out", ...)
case <-ctx.Done():
    log.Warn("system_runtime: wait aborted by shutdown context", ...)
}
```

## 回归测试

- 新增 `TestOrchestrator_Shutdown_BlockingWaiterTimesOut`：以 5s deadline 调用 Shutdown，
  配合永久阻塞的 `Wait()`，断言在 7s 内返回（而非 30s）。
- 新增 `TestOrchestrator_Shutdown_AggregatesErrors`：Stop 错误与 errgroup 错误被聚合返回。
- 新增 `TestOrchestrator_Shutdown_IdempotentAndConcurrent`：并发 Shutdown 只执行一次 Stop。
- 新增 `TestOrchestrator_Start_*` 系列：Bind/Start/Ready 失败时的清理与逆序回滚。

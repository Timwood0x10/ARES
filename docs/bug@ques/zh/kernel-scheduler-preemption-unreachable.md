# Bug: 内核调度器的协作抢占分支不可达——drain 的 wg.Wait 使 RUNNING 任务永远跨不过 drain 边界

- **ID**: BUG-KSCHED-001
- **严重度**: P2 / Medium（功能假接线：API 存在、测试存在，但生产路径永不触发）
- **状态**: 已修复
- **日期**: 2026-08-22
- **涉及包**: `internal/kernelscheduler`

## 现象

`Scheduler.PreemptLowerPriority`（P2.2 协作抢占的调度器侧接线）在真实调度循环中
永远不会抢占任何任务。通过 scheduler 全链路验证时：低优先级任务 RUNNING 中，
创建高优先级 READY 任务后，`task.preempted` 事件永远不出现，运行中的 quantum
持续占位直到自然结束。

## 触发条件

任何经由 `Scheduler.Run` 的正常调度流程。无需特殊配置。

## 根因

`drain()` 在尾部对本次派发的所有执行 goroutine 做 `wg.Wait()`：

```go
// drain()
for _, taskID := range tasks { go s.execute(ctx, id) ... }
wg.Wait() // ← drain 阻塞到所有在飞 quantum 结束
```

而 `PreemptLowerPriority` 恰恰只在 `drain()` **入口**被调用：

```go
tasks := s.fabric.ResumableTasks()
s.PreemptLowerPriority(tasks) // 只在这里调用
```

两件事叠加后：**drain 入口时刻，上一个 drain 派发的任务必然已全部终态**
（COMPLETED / FAILED / SUSPENDED——`RunQuantum` 的 done/err/!done 三出口保证）。
`PreemptLowerPriority` 扫描 `RunningTasks()` 时列表恒为空，抢占分支成为死代码。

注释所写的意图是抢占"a task that is RUNNING from a previous drain"——即作者
预期 RUNNING 能跨越 drain 边界存活；`wg.Wait` 的串行化使该前提不成立。

## 修复

`Run` 增加一个受管 watcher goroutine（ctx 退出即止、单次扫描带 recover 边界，
符合 code_rules_v2 §4.1/§4.2）：每个 poll tick 独立执行一次
`PreemptLowerPriority(ResumableTasks())`，不再依赖会阻塞的 drain 主循环。

语义保持"quantum 永不在步内被打断"：抢占只改 durable 状态（RUNNING→READY、
清 lease），旧持有者的迟到完成由 fencing token 拒绝（`ErrNotOwner` /
epoch 不匹配，benign）。下一个可用 drain 重新 Acquire 被抢占的任务。

`drain()` 入口处的原调用保留：它覆盖"无在飞任务时的即时检查"，与 watcher
并发时由 epoch fencing 保证幂等安全。

## 复现与回归测试

`internal/kernelscheduler/scheduler_contract_test.go`:

```
TestPreemptLowerPriorityHandsBackRunningTask
```

门控 executor 阻塞首个 quantum → 创建高优先级任务 → 断言出现
`task.preempted` → 释放门控 → 断言高优完成、低优被重新执行 ≥2 次
（fencing 拒绝旧持有者后重新获取）。修复前该测试失败于"never preempted"。

## 影响

- 修复后 P2.2 协作抢占在生产调度链路真正生效；
- 高优先级任务的等待上限从"当前最长 quantum"降为"一个抢占扫描周期 +
  当前 step 边界"；
- 无行为兼容性风险：此前该路径从不触发，不存在依赖其旧行为（永不抢占）
  的调用方。

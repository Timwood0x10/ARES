# RunQuantum 吞掉 step 错误 → 失败被记成成功

日期：2026-08-24
范围：internal/taskfabric、internal/kernelscheduler

## 症状

内核 quantum 内的每一次任务失败都不可见：

- `Fabric.RunQuantum` 丢弃 `stepErr`，返回 `f.Fail(...)` 的结果——而该结果在
  “重排队到 READY”与“最终 FAILED”两种情况下都是 `nil`。
- 调度器的 `endQuantumOutcome(winner, capability, taskID, err)` 因此看到
  `err == nil`，把失败的 quantum 记为成功：`LoadTracker.End`（`ok++`）与 W4
  归因（`Record(..., true)`）双双失真。
- 失败时 `Scheduler.Scheduled` 照样自增。
- `logFailure` 收到 `nil`，失败根因永远不会出现在任何日志里。

持续失败的 agent 置信度反而向 1.0 爬升（与文档记载的 EndNeutral 设计初衷完全
相反），污染调度打分。

## 修复

```go
if stepErr != nil {
    if failErr := f.Fail(taskID, agentID, epoch); failErr != nil {
        return errors.Join(stepErr, failErr)
    }
    return stepErr // 状态迁移已完成；调用方必须看到失败
}
```

原先断言“RunQuantum 必须吞掉 step 错误”的契约测试已改写为新契约
（错误向上传播且状态迁移完成）。

## 同轮关联修复：panic 泄漏 LoadTracker 槽位

executor 内 panic 会穿透 `RunQuantum` 跳过 `endQuantumOutcome`；
`tracker.load[winner]` 永久 ≥ 1，Score 的 `(1-clamp01(load))` 因子把该 agent
永久清零（不可再调度）。调度器现在用 defer 守卫仅在 panic 路径中性释放槽位。

## 回归测试

- `TestFabricRunQuantumFails` / `TestFabricRunQuantumFailsExhausted`
  （internal/taskfabric/quantum_test.go）：错误传播 + READY/FAILED 状态。
- `TestSchedulerAttributesFailureAsFailure`：归因置信度降为 0、tracker 置信度
  为 0、槽位释放、Scheduled 计数不变。
- `TestSchedulerPanicReleasesLoadSlot`：panic 后 agent load 归零（仍可调度）。

## 后续发现：零置信度导致重试永久搁浅（本修复暴露）

失败被如实记录后，`TestGraphsEndpointNodeFailureReturns422` 挂起：一次失败后
`LoadTracker.Confidence` 归零，`Score = overlap × (1-load) × confidence × boost`
随之为 0——唯一有能力的 executor 永远不会再被调度，有界的重试预算永远花不
出去，协作图节点一直等到超时。旧 bug（失败记成成功、置信度 1.0）恰好把这个
设计缺陷掩盖了。

修复于 `taskfabric.Pick`：增加"最后手段"层——当没有任何能力重叠的候选项得分
为正时，仅在回退得分严格为正的候选中，返回去掉置信度因子后排名最好的那个。健康路径完全不变；0 置信度 agent 按 SetAgentConfidence 契约留在队尾，
而不是永远不可调度。

回归测试：`TestPick_LastResortKeepsAllFailureCandidateReachable`、
`TestPick_HealthyBeatsFailed`、`TestPick_NoOverlapStillNil`。

# 租约续期、Live-DAG 注入、PG 订阅丢事件、归档持久性、复活竞态、僵尸执行器

日期：2026-08-24
范围：taskfabric、kernelscheduler、cmd/ares serve 接线、ares_events（PG store + archive）、ares_runtime

## 1. 无租约续期 → 并发重复执行

`Lease.ExpiresAt` 在 Acquire 时一次性设定，无任何续期机制。超过 TTL 的
quantum 会在原持有者仍在执行时被 `CheckExpiredLeases`（每秒一次 sweep）重排
队，第二个执行器随即并发执行同一任务——fencing 保住了状态正确性，但开销与
副作用双倍。

修复：新增 fencing 的 `Fabric.Renew(id, agentID, epoch, ttl)`；调度器在
quantum 存续期间以 ttl/3（最小 5s）心跳续期，协程由 errgroup 管理，在完成/
ctx 取消/失去所有权时停止。`Scheduler.WithTTL` 暴露配置。测试：
`TestSchedulerRenewsLeaseDuringLongQuantum`（700ms 步长 vs 300ms TTL → 恰好
执行一次）。

关联时钟一致性修复：`Acquire` 改用 FABRIC 时钟（`f.now`）构造租约——过期
判定本就基于 f.now，旧的时间源混用使得 fixture 一旦把 fabric 时钟拨到真实
时间之后，所有新租约"生来即过期"。

## 2. UpdateLiveDAG 在 serve 从未被调用

workflow/recovery 结构补丁永远只改合成三节点 DAG，"提权到 live"不可观测。

修复：`cmd/ares/serve_live_dag.go` 从配置的 peer 群体构建真实拓扑（每 peer
一节点；legacy agents.sub 的 Dependencies 转为 DAG 边），serve 在创建代理后
经 `RegisterAgentDAG("agents", …)` + `NewEvolution.UpdateLiveDAG(dag)` 注入。
测试：`TestBuildLiveAgentDAG_*`、`TestUpdateLiveDAG_WiredFromServeShape`。

## 3a. PG 事件订阅静默跳过时间戳并列事件

轮询游标推进到批次最大 `created_at`，下次查询用严格 `>`——落在 LIMIT 截断
之后的同微秒兄弟事件被永久跳过（PG 时间戳微秒精度，突发批次必然碰撞）。

修复：重叠窗口轮询——查询改为 `>= cursor`，按事件 id 去重（有界已投递集
合）；仅当单次轮询取回行数小于 LIMIT（窗口已被完整消费）才推进游标。

## 3b. 归档 sink 失败永久丢失轮次记录

轮次边界（`lastArchivedVersion`）在持久化写入之前推进；瞬时 sink 错误无回
滚，随后 compaction 裁剪原始事件时，其归档副本从未落盘的记录不可恢复。

修复：仅在 sink 写入成功后提交边界；失败时同窗口同轮次号重试。测试：
`TestArchiveSink_TransientFailureRetriesSameRound`、
`TestArchiveSink_BoundaryStaysZeroAfterFailure`。

## 4a. 停机后的复活竞态（约 1s 窗口）

`NotifyAgentDead` 只在决策时刻检查操作员意图，随后异步调度约 1s 后的恢复。
落在窗口内的 Stop/Pause 会被 clobber：恢复流程在操作员停机约 1s 后装上一个
全新的运行实例。

修复：`managedAgent.operatorIntent` 由 StopAgent/PauseAgent 置位；
RestoreAgent 在安装临界区内复查该标志并中止（期望状态就是"停"）。
ResumeAgent 清除标志使后续死亡可正常复活。测试：
`TestRestoreAgent_AbortsWhenOperatorStoppedAfterScheduling`、
`TestNotifyAgentDead_ThenOperatorStop_RestoreAborted`。

## 4b. 被 kill 的 agent 残留僵尸 executor 注册

`agentfabric.Kill` 只删 fabric 条目；静态调度注册永存——stale-winner 回退
可能在死者注册上执行任务，且每次 spawn_agent 都让注册表无界增长。

修复：`Scheduler.reconcileFabricDeaths()` 每次 drain 对账，fabric 中已消失
的注册即注销；recovery 绑定的替换执行器除外（终态时注销）。测试：
`TestReconcileFabricDeaths_*`。

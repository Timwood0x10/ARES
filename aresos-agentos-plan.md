# ARES Agent OS 完全体改造计划（v3 · 单一调度系统）

> 基准：`aresos-plan.md`（P0-P6 + 附件 D/E）
> 指导原则（用户拍板）：
> 1. **对外接口极度精简**——SDK 只暴露最小必要面。
> 2. **内部只保留一套 agent 调度系统**——废弃 leader-sub，统一到 `agentfabric.Agent`。
> 3. **`agentfabric.Agent` 做成完全体**——融合执行（ExecuteStep）、chaos、GA、memory distill。
> 核对：读 `internal/{taskfabric,agentfabric,agentipc,aresrecovery,ares_memory,agents/sub,agents/leader}`、`sdk`、`cmd/ares`。
> 日期：2026-08-20 | 分支：dev
> 规范：`plan/rules/code_rules_v2.md`（禁裸 goroutine、goroutine 必 recover、修 bug 先写复现测试、不擅自 git commit、破坏性变更走灰度）。

---

## 0. 目标架构（一句话）

```
一个 Agent 实体 = 认知(LLM) + 执行(quantum) + 生命周期(spawn/kill/recover) + 私有 CognitiveState
Kernel = Scheduler(taskfabric) + Lifecycle(agentfabric) + IPC(agentipc)
横切能力 = Chaos / GA / Memory-Distill，全部挂在同一个 Agent 实体 + EventStore 上
对外 = 极简 SDK（NewRuntime → Submit(task) → 结果），不暴露 leader/sub/kernel 细节
```

废弃：`internal/agents/leader`（TaskPlanner/TaskDispatcher/Team）、`internal/agents/sub` 作为“独立第二套 agent”的定位。执行能力（`ExecuteStep` + tool loop）**下沉**到统一 Agent。

---

## 1. 现状：三套东西没合并

| 关注点 | 现在在哪 | 问题 |
|---|---|---|
| 执行体 | `sub.Agent.ExecuteStep`（配置写死，进 scheduler `executors` map）| 与生命周期体割裂 |
| 生命周期体 | `agentfabric.Agent`（Spawn/Kill/Recover/CognitiveState）| 进不了 scheduler（phantom）|
| 调度入口 | `leader.Agent.Process → TaskPlanner → TaskDispatcher` | 第二套调度，需废弃 |
| Chaos | `aresrecovery.Chaos`（已 target agentfabric）| 已对齐 fabric，但没跟执行体联动 |
| GA | `aresrecovery.EvolutionAdapter/PopulationAdapter`（已 target agentfabric）| 只 spawn/retire 空壳 agent |
| Memory Distill | `ares_memory.Distiller.SubscribeAndDistill`（订阅 EventStore）| 与 agent 生命周期无关联 |

**根因**：`agentfabric.Agent` 是空壳（只有状态没有执行体），所以 chaos/GA 操作的都是不能干活的 agent，执行真正发生在另一套 `sub.Agent` 上。**把执行能力注入 `agentfabric.Agent`，三套自然合并。**

现状基线（现在能过）：
```
go test -race ./internal/taskfabric ./internal/agentfabric ./internal/agentipc ./internal/aresrecovery ./cmd/ares  # 全绿
go run ./examples/aresos-demo                                                                                        # 7 步（确定性模拟）
```

---

## 2. 改造总纲：8 个阶段

```
A 统一 Agent 实体（把执行能力注入 agentfabric.Agent）      ← 地基
B 单一调度回路（scheduler 只认统一 Agent）
C 废弃 leader-sub（删第二套调度）
D Agent 自主 spawn / 拆分（syscall）
E 融合 Chaos（死亡→恢复走真实执行体）
F 融合 GA（进化真实 agent + 反馈调度）
G 融合 Memory Distill（挂到 agent 生命周期）
H 极简 SDK 对外面 + 真实 runtime 大闭环验收
```

依赖：A 是所有的地基；B/C 紧随；D/E/F/G 可在 B 之后并行推进；H 收口。

---

## 3. 逐阶段修复计划（每步：做什么 + 改哪 + 验收）

> 通用质量门（每步收尾）：`go build ./...`、`gofmt -l .` 空、`go vet ./...`、相关包 `go test -race` 全绿。每步独立 commit、可回滚。

---

### 阶段 A — 统一 Agent 实体（地基）

**A1. 把执行能力注入 `agentfabric.Agent`**

做什么：
1. 在 `agentfabric` 定义执行契约（接口在消费方，`code_rules_v2 §5.2`）：
   ```go
   // agentfabric/executor.go
   type Cognition interface {
       ExecuteStep(ctx, *Task) (*StepOutcome, error)  // 一个 quantum
   }
   ```
2. `Agent` 结构增加 `cognition Cognition` 字段：Agent 从“状态载体”升级为“可执行认知进程”。
3. `SpawnSpec` 增加 `Capabilities → Cognition` 的构造方式：通过 `CognitionFactory`（给定 capabilities 产出绑定 LLM/tools 的执行体）。
4. 把 `sub` 包的 tool-loop 执行逻辑（`chatStep`/`decodeChatStepState`/`renderPrompt`）**下沉/移植**为 `agentfabric` 的默认 Cognition 实现（不是新写，是搬运 + 去掉 leader 依赖）。

改哪：`internal/agentfabric/{agent.go,lifecycle.go,executor.go(新)}`、移植 `internal/agents/sub/executor.go` 的 tool loop。

验收：
- `fabric.Spawn(spec)` 出的 Agent 直接可 `ExecuteStep`（新测试：spawn → 喂 task → 跑一个 quantum → 有结果/checkpoint）。
- `StepOutcome` 语义（Done/Checkpoint/Result）与原 `sub.StepOutcome` 一致（迁移测试全绿）。

**A2. checkpoint 协议固化（顺带修 D6）**

做什么：
1. 统一 checkpoint schema（复用现有 `stepSchemaVersion` 模式），Task checkpoint 与 Agent CognitiveState 用带版本的结构，去掉长期 `any`。
2. 明确三层数据边界：Task Shared（taskfabric）/ Agent Private（`agentfabric.privateContext`）/ IPC（agentipc）。

验收：schema 带 version，旧版本解码拒绝/迁移有测试；三层隔离测试全绿。

---

### 阶段 B — 单一调度回路

**B1. scheduler 候选来自 agentfabric 动态群体**

做什么：
1. `kernelScheduler.executors` 从“静态 `map[string]sub.Agent`”改为“查询 `agentfabric.Fabric` 的活跃 Agent”。
2. 候选 = 所有 `StateIdle` 且 capability 匹配的 Agent；选择只看 capability/load/confidence/priority。
3. `Schedule → Acquire → RunQuantum` 直接调 `agentfabric.Agent.ExecuteStep`（A1 注入的执行体）。
4. Spawn/Kill 事件即时反映到候选集（agent 一 spawn 就能被调度，一 kill 就移除）。

改哪：`cmd/ares/scheduler.go`、`cmd/ares/kernel.go`。

验收：
- 新测试：`fabric.Spawn` 后该 agent 立即出现在候选并被选中执行；`Kill` 后不再被选。
- grep 确认 scheduler 不再引用 `sub.Agent` / `AgentTypeLeader`。

---

### 阶段 C — 废弃 leader-sub（删第二套调度）

**C1. 移除 leader 编排入口**

做什么：
1. 删除/legacy 化 `leader.Agent.Process`、`TaskPlanner`、`TaskDispatcher`、`NewTeam`。任务入口改为“提交 Task 到 Kernel”，由统一 Agent 群体调度。
2. `cmd/ares/agents.go` 的 `createLeaderAgent`/`createSubAgents` 替换为 `createAgents`（一组平等 capability agent 注册进 `agentfabric.Fabric`）。
3. `ares.yaml` 配置从 `leader/sub` 改为平铺 `agents: [{id, capabilities, priority}]`。
4. 破坏性变更走灰度：保留 `kernel.policy=legacy` 一个版本作为回滚开关，默认新路径。

改哪：`cmd/ares/agents.go`、`internal/agents/leader/*`、`ares.yaml`、相关配置结构。

验收：
- 关闭 leader（或删除后），提交用户任务仍能完成（新 E2E）。
- grep 确认生产路径无 `TaskPlanner.Plan` / `TaskDispatcher.Dispatch` 调用。
- 旧 leader 包若保留则打 `// Deprecated:` 且不在默认路径。

---

### 阶段 D — Agent 自主 spawn / 拆分

**D1. Kernel syscall：spawn_agent / create_task**

做什么：
1. 给统一 Agent 的 Cognition 暴露受 Kernel 校验的 syscall（tool 形式）：内部调 `agentfabric.Spawn`（走 A1 factory）+ `taskfabric.Create`，做 capability/quota 校验。
2. 子任务进 scheduler（ReadyTasks），不经任何 dispatcher。
3. 父 Agent 经 IPC（`Request`/`Delegate`）收子结果并 synthesis。

改哪：`internal/agentfabric`（syscall 接口）、`internal/agentipc`（协作）、tool 注册。

验收：
- 单测：Agent 调 spawn syscall → fabric 多一个可执行 agent + Task Fabric 多一个 READY task + provenance link。
- E2E（fake LLM）：单 Agent 判断任务大 → 自主 spawn B/C → 经 scheduler 执行 → IPC 回传 → 父 synthesis（无预拆 TaskPlanner）。

---

### 阶段 E — 融合 Chaos（死亡→恢复走真实执行体，修 D1）

**E1. Chaos/Recovery 作用于统一 Agent**

做什么：
1. `aresrecovery.Recovery.RecoverTaskCheckpoint` 的 replacement 走 A1 factory，产出**可执行**替代 Agent，注册进调度候选（消除 phantom）。
2. checkpoint 从 `CognitiveState` 真正流入替代 Agent 的下一个 quantum。
3. `Chaos.InjectFailure` 杀的就是执行体；`VerifyRecovery` 验证新执行体续跑。

改哪：`internal/aresrecovery/{recovery.go,chaos.go}`、`cmd/ares/kernel.go: runKernelRecoveryLoop`（删 phantom 分支）。

验收：
- `-race` E2E：真实执行体跑到一半 → chaos kill → lease 过期 → 新执行体 acquire → 从 checkpoint 续跑（断言带旧轮次对话，非重头跑）→ COMPLETED。

---

### 阶段 F — 融合 GA（进化真实 agent + 反馈调度，修 D7）

**F1. GA 作用于统一 Agent + 反馈进调度**

做什么：
1. `EvolutionAdapter/PopulationAdapter` spawn 出的是 A1 的可执行 Agent（不再空壳）。
2. 采集调度结果（成功率/失败率）按 capability/agent 归因，反馈进 scheduler 的 confidence/capability scoring。
3. GA 可建议 spawn 特定 capability 的 reviewer agent。

改哪：`internal/aresrecovery/{chaos.go,evolution_population.go}`、`cmd/ares/scheduler.go`（scoring 接反馈）。

验收：
- 测试：注入一组结果 → GA 更新策略 → 下一轮 scheduler 选择/spawn 行为可观测变化。
- GA spawn 的 agent 能被真实调度执行（非 phantom）。

---

### 阶段 G — 融合 Memory Distill（挂到 agent 生命周期）

**G1. Distill 绑定 Agent 事件**

做什么：
1. `Distiller.SubscribeAndDistill` 订阅统一 Agent 的 task 完成事件（已有 EventStore 通道），distill 结果回写为 Agent 可复用的经验/CognitiveState 先验。
2. Agent 恢复/spawn 时可加载相关 distilled experience 作为初始 context（closing §11 feedback loop）。

改哪：`internal/ares_memory/distillation/*`、`agentfabric` spawn 时注入经验先验。

验收：
- 测试：Agent 完成任务 → distill 异步产出经验 → 新 spawn 的同 capability agent 能读到该经验先验（channel/WaitGroup 同步，禁 `time.Sleep`）。

---

### 阶段 H — 极简 SDK + 真实大闭环验收

**H1. 对外接口收敛（原则 1）**

做什么：
1. SDK 只保留最小面，示意：
   ```go
   rt := sdk.NewRuntime(opts...)          // 装配 Kernel（scheduler+lifecycle+ipc+chaos+GA+distill）
   defer rt.Close()
   rt.RegisterAgent(id, capabilities...)  // 注册平等 agent（或全自动）
   res, err := rt.Submit(ctx, task)       // 提交任务，Kernel 自行调度/拆分/恢复
   ```
2. 隐藏 leader/sub/kernel/fabric 细节，不在公共 API 暴露。
3. `NewTeam` 等 leader 概念标 Deprecated 或删除。

改哪：`sdk/sdk.go`、`sdk/team.go`、`sdk/options.go`。

验收：
- SDK 公共符号面显著缩小（对比前后导出符号数）；示例 `go run` 通过。
- 无公共 API 泄漏 leader/sub/kernel 类型。

**H2. 真实 runtime 大闭环 E2E（修 D4，总验收）**

做什么：
1. 新增 runtime E2E（fake LLM），真实经过：`Submit → Spawn → Schedule → Acquire → RunQuantum → Yield → chaos kill → lease expiry → recovery → 新执行体续跑 → Peer IPC → synthesis`。
2. synthesis 结果由子 Agent 真实输出组装，禁止写死。
3. 覆盖附件 D Case 1-4 + 附件 E 大闭环，全走真实调度。
4. 追加 “Leader OFF” 断言：全程无 leader 参与。

验收：
- E2E `-race` 通过；改子 Agent 输出会改变最终结果；事件流含 `task.created/acquired/yielded/completed` 与 `agent.spawned/killed/recovered`。

---

## 4. 缺陷 → 阶段对照

| 缺陷 | 严重度 | 修复阶段 |
|---|---|---|
| 执行体与生命周期体割裂（根因）| 致命 | A1 |
| D1 恢复未接真实 executor | 致命 | E1（依赖 A1）|
| D2 Agent 不能自主 spawn | 高 | D1 |
| D3 未验证 Leader OFF / 两套调度 | 高 | B1 + C1 |
| D4 大闭环是模拟 | 高 | H2 |
| D5 EventStore best-effort | 中 | A2 附带 + 独立收紧 |
| D6 checkpoint 无 schema | 中 | A2 |
| D7 GA 无反馈闭环 + 进化空壳 agent | 中 | F1 |
| distill 未挂 agent 生命周期 | 中 | G1 |
| 对外接口臃肿（leader/sub 泄漏）| 中 | H1 |

---

## 5. 执行顺序与里程碑

```
里程碑 1（地基）：A1 + A2
  → 统一 Agent 可执行 + checkpoint schema 固化
里程碑 2（单一调度）：B1 + C1
  → scheduler 只认统一 Agent，leader-sub 退出默认路径
里程碑 3（自主 + 恢复）：D1 + E1
  → Agent 自主 spawn，死亡后真实续跑
里程碑 4（横切融合）：F1 + G1
  → GA/Chaos/Distill 全挂统一 Agent
里程碑 5（收口）：H1 + H2
  → 极简 SDK + 真实大闭环验收
```

**关键路径是 A1**：执行能力注入 `agentfabric.Agent` 之后，B/C/D/E/F/G 全都从“操作空壳”变成“操作真实执行体”，三套系统自然合并成一套。建议第一个迭代只做 A1+A2，拿到“spawn 出的 agent 能直接跑一个 quantum”的测试后再推进。

---

## 6. 风险与回滚

| 阶段 | 风险 | 回滚 |
|---|---|---|
| A1 | 执行逻辑下沉可能引入行为差异 | 迁移测试对齐原 `sub` 行为；分 commit |
| C1 | 删 leader-sub 是破坏性变更 | `kernel.policy=legacy` 灰度开关保留一个版本 |
| E1/F1 | 恢复/GA 接真实执行体改变运行时行为 | feature flag，先影子验证 |
| H1 | SDK 收窄破坏外部调用方 | 标 Deprecated 过渡，不一次性删 |

一句话：**先把执行能力注入 `agentfabric.Agent`（A1），让它成为唯一的可执行认知进程；然后 scheduler 只认它、删掉 leader-sub、chaos/GA/distill 全挂上来，最后用极简 SDK 收口。这是从“两套 agent + 一个空壳”到“单一完全体 Agent OS”的最短路径。**

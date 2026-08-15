# 模块分析报告：`internal/evolution`（进化协调 / GA 管线）

> 分析范围：`internal/evolution/`（44 个 Go 文件），含 coordinator/、deployment/、genome/、patch/、diff/ 子包

---

## BUG（高置信度）

### 2. `candidate_pipeline.go` `Release` 时 patch 被应用/部署两次
- **位置**：`candidate_pipeline.go` 202-216 行
- **说明**：
  ```go
  p.coordinator.Submit(...)
  p.coordinator.Evaluate(ctx)              // ① 决策 Apply 时已应用 patch（经 deployer 或 registry）
  decision := p.lastDecision(rp.ID)
  switch decision.Decision {
  case coordinator.DecisionApply:
      released, applyErr := p.applyAndPromote(ctx, rp, c)   // ② 再次应用/部署
  ```
  `Evaluate` 在决策为 Apply 时已应用 patch（经 `patchReg.Apply` 或 deployer）。`applyAndPromote` 再调 `p.registry.Apply`（或 `p.deployer.Deploy` 全 canary 管线）第二次。启用 deployer 时同一 patch 的 staging→evaluate→live canary 全序列执行两次；禁用时 `ProfileExecutor.Apply` 第二次因碰巧见相同指令而 no-op。**冗余的双重应用/部署。**

---

## LOGIC（逻辑问题）

### 5. `patch/patch.go` `ApplySet` fallback 回滚可能遗漏
- **位置**：`patch/patch.go` 329-348 行
- **说明**：fallback 应用的 patch 后续触发回滚时，只经 `r.executors[ap.rollback.Target]` 尝试；fallback 起源的回滚目标 key 在 `r.executors` 中无条目（单 patch `Apply` 265-285 会经 `r.fallback` 重路由，但 `ApplySet` 不会）。回滚循环中的错误也被丢弃（339、357、372、387 行的 `_, _ = rbEx.Apply(...)`）。

### 6. `scheduler_genome.go` 候选池文档/行为不符
- **位置**：`scheduler_genome.go` 25-27、152-157 行
- **说明**：文档声称池含 DefaultScheduler、PriorityScheduler、ShortJobScheduler、RoundRobinScheduler、WeightedFairScheduler，但 `defaultSchedulerCandidates()` 只注册 `DefaultScheduler` 和 `RoundRobinScheduler`。文档高估实际搜索空间。

### 7. `coordinator/coordinator.go` `ApplyEmergency` 绕过 deployer
- **位置**：`coordinator/coordinator.go` 232-256 行
- **说明**：即使安装了启用的 `PatchDeployer`，`ApplyEmergency` 总是经 `ec.patchReg.Apply`（绕过决策过程）。文档注明"bypassing the decision process"，但应急自愈 patch 跳过安全晋升管线，值得注意的不对称。

---

## 其它 / 低优先级

### 8. `workflow_genome.go` `Crossover` 忽略取消上下文
- **位置**：`workflow_genome.go` 156/160 行
- **说明**：`ReplaceNode`/`AddNode` 用 `context.Background()` 而非传入的 `ctx`，取消的 crossover 不中止。

### 9. `candidate.go` `generateID` 提交时被覆盖
- **位置**：`candidate.go` 93、360 行
- **说明**：`NewCandidate` 设 `ID: generateID()`，但 `CandidateStore.Submit` 无条件重写 `c.ID = fmt.Sprintf("cand-%d", nextID)`。纳秒时间 ID 对已存候选冗余/丢弃（非 store 路径仍用）。

---

## 确认的非问题（供参考）

- `ga_generator.go` `children[0]` 安全：`Mutator.Mutate` n=1 成功时恰好返回一个 child。
- `NotifySelfHealingAttempt` 重试预算正确。
- `Registry.Apply` idempotency map 无自身 mutex，但当前所有调用路径经 coordinator 锁串行化，无确认竞争。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `workflow_genome.go` 298-379 | DAG 变异绕过边 map，拓扑变更无效；merge 自环 |
| **高** | `candidate_pipeline.go` 202 | patch 应用/部署两次 |
| 中 | `deployment.go` 165 | Deploy 伪造无关 PatchID |
| 中 | `patch.go` 329 | ApplySet fallback 回滚可能遗漏 |
| 中 | `diff.go` 106 | DiffAll 吞 differ 错误 |
| 低 | `coordinator.go` 232 | ApplyEmergency 绕过 deployer |

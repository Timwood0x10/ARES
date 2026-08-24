# 部署流水线：staging 污染 live 状态；Coordinator 历史失真

日期：2026-08-24
范围：internal/ares_bootstrap、internal/evolution/deployment、patch.Registry

## 症状

`evolution.deployment.enabled=true` 时，staging→live 安全门形同虚设：

1. `deploymentStagingRuntime` 与 `deploymentLiveRuntime` 共享同一个
   `comp.NewEvolution.PatchReg`。staging 的 `Apply` 直接对 live 注册表执行
   `reg.Apply`——随后被"拒绝"的补丁早已改写了内存配置、知识运行时与 DAG
   恢复策略。
2. staging 的"回滚"把同一个补丁再应用一遍（`return &p` 自己当回滚）——对幂等
   补丁是 no-op，其余情况是双重应用。
3. 对带 ID 的补丁，staging 应用污染了共享幂等表（`applied[patch.ID]=true`），
   导致后续 live 提权被静默跳过——实际上什么都没提升。
4. `deploymentAdapter.Deploy` 丢弃了 `rec.Status`：管道拒绝不是 error，
   Coordinator 便记录 `PatchResult{Error: nil}`——面向运维的决策历史把被拒绝
   的补丁记成了成功。

## 修复

- `patch.Registry.CanApply(target)`：只读预检。
- staging Apply = 仅预检（不改任何状态）；Rollback = 记账 no-op。未知 target
  仍在 staging 阶段失败（保持原拒绝类别）。
- `deploymentAdapter.Deploy` 对一切非 promoted 结果返回 error，让 Coordinator
  历史如实反映现实。

## 回归测试

- `TestDeploymentStaging_DoesNotMutateLiveRegistry`
  （internal/ares_bootstrap/deployment_wiring_test.go）：真实
  MemoryPatchExecutor；staging Apply 后 live MaxHistory 不变；孤儿 target 被拒。

## 同轮关联的闭环修复

- `recovery.strategy` 从未注册为 patch target，而 `RecoveryDiffer` 和 LLM
  adapter 都会产出该 target → 所有恢复策略补丁报 "no executor registered"。
  已在 `ProvideNewEvolution` 与 `UpdateLiveDAG` 两处随其他三个 recovery 键一并
  注册。
- `cmd/ares serve` 传入 nil StrategySource：GA 部署到
  `NewEvolution.StrategyStore` 的策略没有任何消费方。serve 现在将
  `ares_bootstrap.NewStrategySource(...)` 注入每个 agent executor。
- Bootstrap 组装了带共享 M3/M4 可观测性适配器的 dashboard 服务器，但任何地方
  都没调用过 `Start`：相关端点（/evolution/trajectory、/evolution/feedback、
  /observability/spans）服务于一台永远无法到达的服务器。serve 现在在
  cfg.Dashboard.Addr 上启动它，并在优雅停机时停止。

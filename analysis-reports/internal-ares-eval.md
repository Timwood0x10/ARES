# 模块分析报告：`internal/ares_eval`（评估系统）

> 分析范围：`internal/ares_eval/`（30 个 Go 文件），含 service/ 子包

---

## BUG（高置信度）

---

## LOGIC（逻辑问题）

### 3. `service/service.go` 生产评估 API 无法真正运行
- **位置**：`internal/api_impl/service.go:358`（接线）
- **说明**：`NewService` 在接线时**没有** `WithAgentExecutor`，`s.agentExecutor` 保持 nil，每个 `runSingleConfig` 都返回 `singleErrorResult`（"no agent executor configured"）。生产评估 API 已接线但实际无法运行评估——总是记录错误结果。**（标注：文档化的行为，但等效于死的端点。）**
- **状态**：⚠️ 接线决策（2026-08-14）——`WithAgentExecutor` option 存在（service.go:26-29），`runSingleConfig` 无 executor 时显式返回错误（注释"Without one, evaluation cannot run"）；api_impl 接线（`evalapi.NewService(evalRepo)`）未注入 executor 属文档化行为，注入需调用方（sdk 层）提供，非本次修复范围，标注留痕。

---

## DEAD_CODE

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `report.go` 100-113 | 全 0 指标最小值误报 1.00 |
| 中 | `service/service.go` 329 | 软失败原因不持久化 |
| 中 | `api_impl/service.go:358` | 生产评估 API 无 executor，永远失败 |
| 中 | `agent_runner.go` 41 | RunSuite 单错中止整套 |
| 低 | `service/repository.go` 150 | ErrNoRows 分支不可达 |

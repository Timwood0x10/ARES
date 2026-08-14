# 模块分析报告：`internal/ares_eval`（评估系统）

> 分析范围：`internal/ares_eval/`（30 个 Go 文件），含 service/ 子包

---

## BUG（高置信度）

### 1. `report.go` `GenerateMarkdown` 最小值误报为 1.00
- **位置**：`report.go` 100-113 行
- **说明**：`minVal` 初始化为 `1.0`。若某指标所有值均为 `0.0`（如全部 exact-match 失败），循环从不更新 `minVal`，报告 Min 列错误显示 `1.00` 而非 `0.00`。
- **状态**：✅ 已核实修复（2026-08-14）——`minVal := values[0]`（从实际值初始化，非 1.0 哨兵），无样本时输出零值行（注释明确说明 1.0 哨兵问题已处理）；报告条目过时。

---

## LOGIC（逻辑问题）

### 2. `service/service.go` 软失败的原因未持久化
- **位置**：`service/service.go` 329-336 行
- **说明**：对软失败（`tr.Error != ""` 但非硬错误），status 置为 `"fail"` 但 `errMsg` 保持 nil，失败原因从不持久化。只有硬错误（timeout/cancelled 等，经 `isHardError`）会存消息。
- **状态**：✅ 已核实修复（2026-08-14）——软/硬失败均 `errMsg = &tr.Error`（注释明确"Previously soft failures (status "fail") left errMsg nil, so the cause was dropped"）；报告条目过时。

### 3. `service/service.go` 生产评估 API 无法真正运行
- **位置**：`internal/api_impl/service.go:358`（接线）
- **说明**：`NewService` 在接线时**没有** `WithAgentExecutor`，`s.agentExecutor` 保持 nil，每个 `runSingleConfig` 都返回 `singleErrorResult`（"no agent executor configured"）。生产评估 API 已接线但实际无法运行评估——总是记录错误结果。**（标注：文档化的行为，但等效于死的端点。）**
- **状态**：⚠️ 接线决策（2026-08-14）——`WithAgentExecutor` option 存在（service.go:26-29），`runSingleConfig` 无 executor 时显式返回错误（注释"Without one, evaluation cannot run"）；api_impl 接线（`evalapi.NewService(evalRepo)`）未注入 executor 属文档化行为，注入需调用方（sdk 层）提供，非本次修复范围，标注留痕。

### 4. `agent_runner.go` `RunSuite` 单个用例出错即中止整套
- **位置**：`agent_runner.go` 41-53 行
- **说明**：任一测试用例出错即 `return nil, err`，丢弃前面已跑和后续待跑的用例结果。与 `ConcurrentRunner.RunSuite`（记录每个测试错误并返回部分结果）不一致。
- **状态**：✅ 已修复（2026-08-14）——`RunSuite` 记录每个失败并继续执行剩余用例，返回部分结果 + `errors.Join` 汇总错误，与 `ConcurrentRunner.RunSuite` 一致，build/test 通过。

### 5. `service/repository.go` 不可达的 `ErrNoRows` 分支
- **位置**：`service/repository.go` 150-153 行
- **说明**：`QueryContext` 的 SELECT 无行时返回空结果集而非 `ErrNoRows`，`if err == sql.ErrNoRows` 分支不可达。死分支。
- **状态**：✅ 已核实修复（2026-08-14）——注释明确"QueryContext returns an empty result set (not sql.ErrNoRows)... The previous `err == sql.ErrNoRows` branch was unreachable"，空集由 `rows.Next()` 循环处理；报告条目过时。

### 6. `comparison.go` 汇总假设所有 config 跑相同用例数
- **位置**：`comparison.go` 410 行
- **说明**：`totalTests` 从 `len(results[0].Results)` 推导，假设所有 config 跑同样数量的测试。且平均/方差只统计成功（无错误）的 config，而 `TotalConfigs` 计所有 config，当 config 失败或数量不同时汇总不一致。
- **状态**：⚠️ 已核实（2026-08-14）——仓库内已无 `comparison.go` 文件（报告路径指向的文件不存在）；当前 `report.go` 的 `GenerateMarkdown` 用 `totalTests := len(results)`（单套结果的直接统计），不涉及跨 config 假设。原 comparison 汇总逻辑已不在此路径，条目过时，无需变更。

---

## DEAD_CODE

### 7. `report.go` `ReportGenerator` / `RunEvaluation` 生产不可达
- **位置**：`report.go` 22-208 行
- **说明**：`GenerateMarkdown`/`GenerateJSON`/`SaveReport` 和独立 `RunEvaluation` 仅测试/文档示例使用。生产 `evalapi` service 不使用它们。
- **状态**：⚠️ 保留为库 API（2026-08-14）——`RunEvaluation` 为独立的库级入口（生产走 `evalapi` service 的 runner 路径），这些导出函数属能力储备/文档示例 API，保留不删（与 ares_runtime 能力储备同模式）。

### 9. `loader.go` 路径校验只认 POSIX 绝对路径
- **位置**：`loader.go` 31-51 行
- **说明**：`validateSuitePath` 的绝对路径/`..` 穿越检测只检查 `strings.HasPrefix(cleaned, "/")`。在 darwin（文档目标）没问题，但 Windows 绝对路径（`C:\...`）不被识别为绝对/穿越保护。
- **状态**：✅ 已修复（2026-08-14）——`isAbsolute` 现同时识别 POSIX（`/abs`）与 Windows 驱动器路径（`X:/`、`X:\`），Windows 绝对路径不再绕过敏感目录检查，build/test 通过。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `report.go` 100-113 | 全 0 指标最小值误报 1.00 |
| 中 | `service/service.go` 329 | 软失败原因不持久化 |
| 中 | `api_impl/service.go:358` | 生产评估 API 无 executor，永远失败 |
| 中 | `agent_runner.go` 41 | RunSuite 单错中止整套 |
| 低 | `service/repository.go` 150 | ErrNoRows 分支不可达 |

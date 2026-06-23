# 混沌工程（Arena）Bug 分析报告（已修复）

> **模块**: `internal/arena`
> **分析时间**: 2026-06-23
> **修复时间**: 2026-06-23
> **分析范围**: Dead Code、Technical Debt、Potential Bugs

---

## 📊 概览

| 类型 | 数量 | 已修复 | 验证结果 |
|------|------|--------|----------|
| Dead Code | 2 | 2（添加废弃警告） | 🟡 向后兼容 |
| Technical Debt | 4 | 2（输入验证+超时） | 🟡 部分修复 |
| Potential Bugs | 6 | 3（实际确认的可修复项） | ✅ 修复 |

---

## 📋 验证结果

在修复前对每个 Bug 报告中的问题进行了代码校验：

| # | 问题 | 严重程度 | 实际存在 | 结论 |
|---|------|----------|----------|------|
| 1 | `calculateAvgRecoveryTime` 方法不存在 | 🔴 高 | ❌ **不存在** | 方法定义在 `survival.go:207`，receiver 为 `*Service`，编译通过 |
| 2 | `Service.actions` 竞态条件 | 🔴 高 | ❌ **不存在** | `emitEvent()` 在 `Unlock()` 之后调用，锁使用正确 |
| 3 | Goroutine 资源泄漏 | 🔴 高 | ✅ **存在** | `context.Background()` 不可取消 |
| 4 | Score 计算使用 nil | 🟠 中 | ❌ **不存在** | `calculateAvgRecoveryTime` 返回 `time.Duration`，nil slice 合法 |
| 5 | `calcAvailability` 边界值 | 🟠 中 | ✅ **存在** | `total==0` 返回 100 不合理 |
| 6 | `LoadScenarioFile` 无限大小 | 🟠 中 | ✅ **存在** | 无文件大小限制 |
| 7 | 死代码: metrics 方法 | 🟡 低 | ✅ **存在** | 仅测试使用 |
| 8 | 死代码: RunScenario | 🟡 低 | ✅ **存在** | 仅测试使用 |
| 9 | 重复统计计算 | 🟡 低 | ❌ **不存在** | 同一方法，同一 receiver |
| 10 | HTTP 输入验证 | 🟠 中 | ✅ **存在** | 仅有空字符串检查 |
| 11 | 超时控制 | 🟠 中 | ✅ **存在** | 同 #3 |

---

## ✅ 已修复问题（6个）

### 1. Goroutine 资源泄漏 (#3)
**文件**: `http.go:handleSurvivalStart`
**修复**: 
- 使用 `r.Context()` 替代 `context.Background()`
- 添加 `context.WithTimeout(ctx, cfg.Duration*2)` 超时控制
- goroutine 在请求取消或超时时自动退出

### 2. `calcAvailability` 边界值 (#5)
**文件**: `score.go:calcAvailability`
**修复**: `total == 0` 时返回 `0` 而非 `100`
- 无故障执行 = 无可用性数据，返回 0 更合理
- 更新了相关测试断言

### 3. `LoadScenarioFile` 无大小限制 (#6)
**文件**: `scenario.go:LoadScenarioFile`
**修复**: 添加 `os.Stat()` 前置检查，限制最大 10MB

### 4. HTTP 输入验证 (#10)
**文件**: `http.go`
**修复**: 
- 新增 `validAgentID()` 辅助函数
- 在所有 `{id}` 路径参数 handler 中增加验证：
  - 空字符串检查
  - 最大长度 256 字符
  - 禁止空白字符
- 覆盖 9 个 handler（killAgent、removeNode、networkPartition、pauseAgent、resumeAgent、slowAgent、toolTimeout、memoryCorrupt、mcpDisconnect、llmFailure）

### 5. 死代码: metrics 废弃方法 (#7)
**文件**: `metrics.go`
**修复**: 为 `RecordRecovery()`、`RecordFailover()`、`RecordConsistency()` 添加 `slog.Warn` 废弃警告日志
- 保留方法以维持测试兼容性
- 运行时触发警告，提示使用 `RecordActionResult`

### 6. 死代码: RunScenario (#8)
**文件**: `scenario.go`
**修复**: 
- 注释更新为 `Deprecated: Use RunScenarioReport instead`
- 函数体首行添加 `slog.Warn("RunScenario is deprecated, use RunScenarioReport instead")`

---

## ⏸️ 确认不存在的问题（5个，无需修复）

| # | 问题 | 原因 |
|---|------|------|
| 1 | 编译错误: 方法不存在 | `calculateAvgRecoveryTime` 定义在 `survival.go:207`，receiver 为 `*Service`，编译正常 |
| 2 | 竞态条件 | `emitEvent()` 在 `s.mu.Unlock()` 之后调用 |
| 4 | Score nil 参数 | `calculateAvgRecoveryTime` 返回 `time.Duration`，非错误；nil slice 合法 |
| 9 | 重复统计计算 | 同一方法 `Service.calculateAvgRecoveryTime` 在 `survival.go` 定义，所有调用方使用相同方法 |
| - | 并发安全注释 | 现有注释已足够清晰 |

---

## 🎯 验证结果

```bash
go test -race ./internal/arena/... -count=1   # ✅ PASS (14.219s)
go vet ./internal/arena/...                    # ✅ PASS
go build ./internal/arena/...                  # ✅ PASS
```

---

## 变更文件清单

### 修改的文件
| 文件 | 变更 |
|------|------|
| `internal/arena/http.go` | 新增 `validAgentID()` + goroutine context 修复 + import |
| `internal/arena/metrics.go` | 添加废弃 `slog.Warn` 到 3 个弃用方法 + import |
| `internal/arena/score.go` | `calcAvailability` total==0 返回 0 |
| `internal/arena/scenario.go` | `RunScenario` 废弃警告 + `LoadScenarioFile` 大小限制 |
| `internal/arena/score_test.go` | 测试断言更新（total==0 期望值） |
| `internal/arena/scenario_test.go` | 测试错误消息更新（stat → read） |

---

*报告生成于 2026-06-23 | 修复于 2026-06-23*
*分析工具: 手动代码审查 + go vet + go test -race*
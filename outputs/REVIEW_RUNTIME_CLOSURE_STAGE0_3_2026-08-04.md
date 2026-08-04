# Runtime Closure 阶段 0–3 提交前审查

> 日期：2026-08-04  
> 审查方式：只读源码核验 + 独立构建/测试/静态检查  
> 结论：**BLOCK — 暂缓提交**

## 一、结论先行

本轮改动中的 F01/F02 配置门控、B01 EventStore 前置接线、Embedding nil-interface-trap 修复方向正确；但它们同时暴露了默认入口兼容回归，且新 `system_runtime` 仍是未接入生产路径、缺少直接单元测试的骨架。

当前至少存在 **2 项默认可达阻断问题、5 项高风险生命周期/契约问题**。`go build ./...` 和全仓测试通过，不能证明 Runtime 闭环已完成，也不能覆盖默认 `ares serve` / `monitor-live` 的 nil 路径。

**建议：先修复本报告 P0/P1，补齐入口和 System Runtime 直接测试，再提交。**

## 二、阻断项汇总

| ID | 严重度 | 位置 | 结论 |
|---|---|---|---|
| R01 | P0 / Critical | `cmd/ares/serve.go:138,208-209` | 默认禁用 Memory/Evolution 时，启动路径会 nil 解引用或无法创建 Leader |
| R02 | P0 / Critical | `cmd/monitor-live/main.go:99-103,145-146` | 默认配置下直接调用 nil Memory，并解引用 nil NewEvolution |
| R03 | P1 / High | `internal/system_runtime/registry.go:161-165` | 未注册依赖被静默视为 external，Required 依赖拼错/缺失仍可启动 |
| R04 | P1 / High | `internal/system_runtime/orchestrator.go:53-59,94-114` | 当前组件 Start 成功、Ready 失败时不会被 rollback，可能泄漏资源 |
| R05 | P1 / High | `internal/system_runtime/orchestrator.go:27-38,131-153` | Shutdown 未先取消根 context，`errgroup.Wait()` 可能永久阻塞 |
| R06 | P1 / High | `api/bootstrap/bootstrap.go:136-140,152-154,245-258,339-340` | EvidenceStore 新契约未统一；Evolution disabled 时仍返回内部 nil 的 RuntimeEvolution wrapper |
| R07 | P1 / High | `internal/ares_bootstrap/bootstrap.go:332-338`; `provide_evolution.go:63` | Legacy Evolution 未完整受 `cfg.Evolution.Enabled` 门控，scheduler 强制启用 |
| R08 | P1 / High | `internal/system_runtime/` | 新控制面未接入 Bootstrap、CLI、API 或 SDK，且没有直接测试 |
| R09 | P2 / Medium | closure 测试文件 | 多项目标缺口只 `t.Logf`，测试 PASS 属于“记录差距”，不是“闭环验证” |
| R10 | P2 / Medium | `Makefile:99-106` | golangci-lint 失败被后续 echo 掩盖，`make lint` 假成功 |
| R11 | P2 / Medium | 中英文文档及进度报告 | 阶段状态、测试含义和 lint 结果表述过强或不实 |

## 三、详细发现

### R01 [已验证] `ares serve` 默认配置不可用 — P0

**证据**

- `cmd/ares/serve.go:138`：`memMgr := comp.Memory`
- `cmd/ares/serve.go:208`：直接读取 `comp.NewEvolution.StrategyStore`
- `cmd/monitor-live/config.yaml:137-144`：Memory 明确默认 `enabled: false`
- 配置未启用 Evolution，`Evolution.Enabled` 零值同样为 false。
- `internal/agents/leader/agent.go:49-50`：`leader.New` 明确拒绝 nil MemoryManager。

**故障路径**

1. F01/F02 使禁用组件不再构造，这是正确门控；
2. 默认 `ares serve` 获得 `comp.Memory=nil`、`comp.NewEvolution=nil`；
3. `comp.NewEvolution.StrategyStore` 首先触发 panic；
4. 即使增加该处 nil guard，Leader 构造仍因 nil MemoryManager 返回错误。

**建议**

明确入口契约后二选一：

- 若 serve 必须依赖 Memory：将 Memory 声明为 serve 的 Required 组件，启动前返回带修复建议的配置错误；
- 若 Memory 可选：提供真正支持 nil/disabled 的 Agent 路径，不要把 nil 传入明确要求非 nil 的构造器。

Evolution disabled 时应传 nil/no-op `StrategySource`，不能解引用 `NewEvolution`。

### R02 [已验证] `monitor-live` 同类默认 panic — P0

`cmd/monitor-live/main.go:99-103` 在未判空时调用 `memMgr.SetEventStore(...)`；`145` 再直接解引用 `comp.NewEvolution.StrategyStore`。其配置文件正好禁用 Memory，因此这是默认可达路径，不是理论边界。

**建议**：与 `ares serve` 使用同一入口适配器和 Required/Optional 判定，避免两套兼容逻辑继续漂移。

### R03 [已验证] 缺失依赖静默通过 — P1

`internal/system_runtime/registry.go:161-165`：

```go
if _, exists := inDegree[d]; !exists {
    // Dependency not registered — treat as external (skip).
    continue
}
```

这会把依赖名拼写错误、漏注册、配置遗漏都当成合法 external dependency。它直接违背“启用组件缺依赖必须 fail-loud”的目标。

**建议**：默认对所有声明依赖要求已注册；如确实需要 external dependency，应使用显式类型/注册 API，而不是自动猜测。

### R04 [已验证] 启动失败 rollback 漏掉当前组件 — P1

`orchestrator.go:53-59` 仅在 `startComponent()` 完整成功后才将组件加入 `started`。而 `startComponent()` 在 `Start()` 成功后才执行 `Ready()`（94-114）。因此 Ready 失败时，当前组件可能已经创建 goroutine/打开资源，却不在 rollback 列表中。

`Start()` 自身返回错误但已产生部分副作用时也有同类问题。

**建议**：按实际完成的生命周期阶段记录组件；进入 Start 后发生任何失败，都应尝试清理当前组件，再逆序清理此前组件。

### R05 [已验证] Shutdown 可能挂死且吞错 — P1

- `NewOrchestrator` 创建 `egCtx` 后直接丢弃（29-32）；
- `Shutdown()` 没有先调用 `o.cancel()`，而是在 Stop 后直接 `o.eg.Wait()`（131-153）；
- 若 `o.Go()` 中任务等待 `RootContext().Done()`，Shutdown 可永久阻塞；
- Stop、Wait、errgroup 错误只记录日志，最终固定返回 nil；
- `Waiter.Wait()` 没有超时保护；
- “Must be idempotent”没有锁或 once 保证。

**建议**：Shutdown 首先取消统一根 context；使用受控超时等待；聚合并返回 Stop/Wait/errgroup 错误；增加并发与幂等测试。

### R06 [已验证] API disabled 语义不一致 — P1

- Arena 已正确使用 `comp.EvidenceStore`（`api/bootstrap/bootstrap.go:119`）；
- Flight 仍仅在 `comp.NewEvolution != nil` 时取 EvidenceStore（136-140），违背 `Components.EvidenceStore`“始终设置”的新契约；
- 无论 Evolution 是否启用，都返回 `&runtimeEvoService{components: comp.NewEvolution}`（152-154）；
- wrapper 非 nil，但方法最终会在 `getEvolutionStatus` 等位置解引用 nil components（如 339-340）。

**建议**：disabled 时返回 nil/显式 Disabled service，或所有方法返回稳定的 disabled error；Flight 直接使用 `comp.EvidenceStore`。

### R07 [已验证] Legacy Evolution 绕过 F02 — P1

`internal/ares_bootstrap/bootstrap.go:332-338` 只检查 EventStore 和 ExpRepo，不检查 `cfg.Evolution.Enabled`；`provide_evolution.go:63` 又强制 `evolution.WithEnabled(true)`。

因此 F02 当前只完整门控 NewEvolution/GA ticker，没有完整门控 Legacy Evolution。

### R08 [已验证] System Runtime 尚未成为生产控制面 — P1

`internal/system_runtime` 已定义接口、状态、Registry、Orchestrator 和 Snapshot，但当前 Bootstrap、`ares serve`、`ares start`、API bootstrap、SDK 均未接入该 Orchestrator。Bootstrap 仍自行构造/启动组件并管理私有 WaitGroup；live DAG 仍在 `mgr.Start(ctx)` 后由 `serve.go:301` 手工补线。

此外 `internal/system_runtime` 当前没有 `_test.go`，全仓测试输出为 `[no test files]`。因此“阶段 1 完成”最多表示骨架代码存在，不能表示系统级控制面已落地或可用。

### R09 [已验证] Closure 测试存在假绿 — P2

- F03 在 `closure_contract_test.go:181-188` 明确用 `t.Logf` 记录 silent degradation；
- F04 在 223-259 明确承认 synthetic DAG 和 post-Start live binding；
- EvidenceStore、KnowledgeRuntime、StrategyStore、PatchRegistry 多个所谓 identity 测试仅记录日志，未验证对象身份；
- `closure_lifecycle_test.go:300` 的 failure injection 仍 Skip；
- 生命周期测试主要覆盖旧 Manager/Bootstrap，不覆盖新 Orchestrator。

所以“20 tests: 19 PASS, 1 SKIP”是测试进程结果，不等于 19 项闭环契约已成立。

### R10 [已验证] `make lint` 假成功 — P2

`Makefile:102-103`：

```make
golangci-lint run --timeout=5m; \
echo "golangci-lint: PASSED"; \
```

linter 非零后 shell 继续执行成功的 echo，target 最终返回 0。本次独立执行实际报告：

```text
internal/ares_bootstrap/bootstrap.go:114:1:
cyclomatic complexity 41 of func `Bootstrap` is high (> 30) (gocyclo)
```

但随后仍打印 `golangci-lint: PASSED` 和 `All lint checks: PASSED`。

### R11 [已验证] 文档和报告超前于实现 — P2

- 中文文档称“阶段 0-3 已实现”；实际阶段 3 只有 B01 部分完成，live DAG 仍 post-Start；
- 文档称“全部 20 个闭环测试通过”，未披露多个 PASS 仅记录已知缺口；
- 英文文档 `docs/en/development/system-runtime.md:132` 将 `LLM` 拼成 `LM`；
- 进度报告声称 `golangci-lint: 0 issues`，与独立结果不符；
- 报告把 System Runtime 包存在描述为阶段 1 完成，但未披露未接生产路径、无直接测试。

## 四、已核实正确或暂未发现问题的部分

以下结论均已直接核对源码：

1. **F01 局部门控正确**：`Memory.Enabled=false` 时 Bootstrap 不再构造 Memory。
2. **F02 的 NewEvolution/GA ticker 门控正确**：disabled 时不创建 NewEvolution，并创建独立 EvidenceStore。
3. **EvidenceStore 字段方向正确**：`Components.EvidenceStore` 在 Evolution disabled 时仍可用。
4. **Embedding nil-interface-trap 修复正确**：仅在 typed pointer 非 nil 时赋给接口；retriever 读取模型名前也判空。
5. **B01 局部接线正确**：Memory 启用时，EventStore 在 Bootstrap 构造期注入，`serve.go` 旧后置调用已删除。
6. **FlightRecorder 单实例接线保持成立**：Bootstrap 创建并启动共享 recorder，写入共享 EvidenceStore。
7. **差异格式正常**：`git diff --check` 通过。
8. 审查期间未修改任何 Go 源码、配置或测试，也未执行 commit。

## 五、独立验证结果

| 检查 | 结果 | 解释 |
|---|---|---|
| `git diff --check` | PASS | 无 whitespace error |
| `go build ./...` | PASS | 编译通过 |
| `go test -count=1 ./...` | PASS | 现有测试通过，但未覆盖默认入口真实启动 |
| closure tests | PASS | 含日志占位与 1 个 Skip，不能等同闭环验收 |
| race closure tests | PASS | 新 `system_runtime` 仍无直接测试 |
| `go vet` | PASS | 相关包通过 |
| `staticcheck ./...` | PASS | 通过 |
| `golangci-lint` | FAIL | `Bootstrap` gocyclo 41 > 30 |
| `make lint` | **假 PASS** | Makefile 吞掉 linter 退出码 |

## 六、建议修复顺序

1. 修复 `ares serve`、`monitor-live` 在 Memory/Evolution disabled 时的入口契约和 nil 路径；补默认配置启动测试。
2. 统一 API bootstrap 的 EvidenceStore 和 RuntimeEvolution disabled 语义。
3. 完整门控 Legacy Evolution。
4. 修复 System Runtime：缺失依赖 fail-loud、当前组件 rollback、Shutdown cancel/超时/错误传播、并发幂等。
5. 为 `internal/system_runtime` 增加直接单元测试：拓扑、缺失依赖、环、Bind/Start/Ready 失败、rollback、Shutdown、Stop/Wait 错误、并发调用。
6. 将 closure 测试的日志占位改为硬断言，或明确标记 Pending/Skip，不再用 PASS 表示未实现目标。
7. 修复 Makefile lint 退出码并处理 gocyclo。
8. 按真实状态更新中英文文档和进度报告。

## 七、提交门槛

修复后至少重新执行并真实通过：

```text
go build ./...
go test -count=1 ./...
go test -race -count=1 ./...
go test -race -tags closure -count=1 ./internal/ares_bootstrap
make lint
```

并增加两类验收：

- 使用默认配置真实启动 `ares serve` / `monitor-live`，证明 disabled 组件不会 panic 或产生伪 Ready；
- 直接执行 `system_runtime.Orchestrator` 的失败注入和有序退出测试。

在上述 P0/P1 清零前，审查意见保持：**BLOCK，暂缓提交。**

# Runtime Closure Progress Report — 2026-08-04

## 实施进度

| 阶段 | 状态 | 关键修复 |
|---|---|---|
| 阶段 0: 基线 | ✅ 完成 | 33 组件清单, 20 闭环测试, nil-interface-trap bug 修复 |
| 阶段 1: System Runtime | ⚠️ 部分完成 | `internal/system_runtime/` 包 + 直接测试；**Bootstrap 已接入 Orchestrator/Registry/Snapshot**（step 11），serve 入口输出启动/关闭快照；仅 CLI/API/SDK 入口尚未接入 |
| 阶段 2: 配置门控 | ✅ 完成 | F01: Memory.Enabled 门控, F02: Evolution.Enabled 门控 |
| 阶段 3: live binding | ✅ 部分 | B01: EventStore 在构造期间装配 (serve.go 旁路已删除)；F04: live DAG 绑定前移至 Start 前（当前无 live DAG 注册，为 no-op + 显式警告，Track C 延后） |
| 阶段 4-7 | 📋 待授权 | 数据反馈环, Tools/MCP, 入口统一, 长稳验收 |

## 已修复的差距

| 编号 | 差距 | 修复 | 验证 |
|---|---|---|---|
| F01 | Memory.Enabled=false 仍构造 Memory | ✅ bootstrap.go: 配置门控 | TestClosure_MemoryDisabled_NotConstructed PASS |
| F02 | Evolution.Enabled=false 仍启动 GA ticker | ✅ bootstrap.go: 配置门控 | TestClosure_EvolutionDisabled_NoGATicker PASS |
| B01 | serve.go 后置 SetEventStore 旁路 | ✅ bootstrap.go: 构造期间装配 | serve.go 旁路已删除 |
| BUG | nil-interface-trap panic | ✅ bootstrap.go + retriever_wiring.go | TestClosure_KnowledgeRetrievalEnabled PASS |
| -- | EvidenceStore 依赖 NewEvolution | ✅ Components.EvidenceStore 始终设置 | api/bootstrap.go 使用 comp.EvidenceStore |

## 代码变更清单

### 新增文件

| 文件 | 说明 |
|---|---|
| `internal/system_runtime/component.go` | 组件生命周期接口 (Component, Binder, Starter, ReadinessChecker, Stopper, Waiter, Resolver, Mode) |
| `internal/system_runtime/state.go` | 状态机类型 (11 个状态, ComponentStatus) |
| `internal/system_runtime/registry.go` | 组件注册表 + 拓扑排序 (Kahn's algorithm) |
| `internal/system_runtime/orchestrator.go` | 生命周期编排器 (Construct → Bind → Start → Ready, 逆序 Stop → Wait, rollback) |
| `internal/system_runtime/snapshot.go` | 状态快照 API (Snapshot, IsReady, JSON) |
| `internal/ares_bootstrap/closure_contract_test.go` | 4 个契约测试 + 2 个辅助测试 |
| `internal/ares_bootstrap/closure_shared_instance_test.go` | 6 个共享实例一致性测试 |
| `internal/ares_bootstrap/closure_lifecycle_test.go` | 8 个生命周期启停断言测试 |
| `outputs/RUNTIME_COMPONENT_INVENTORY_2026-08-04.md` | 33 组件清单 + 依赖 DAG |
| `outputs/RUNTIME_CLOSURE_STAGE0_GAP_REPORT_2026-08-04.md` | 阶段 0 差距报告 |
| `docs/en/development/system-runtime.md` | 英文开发文档 |
| `docs/zh/development/system-runtime.md` | 中文开发文档 |

### 修改文件

| 文件 | 变更 |
|---|---|
| `internal/ares_bootstrap/bootstrap.go` | F01: Memory.Enabled 门控; F02: Evolution.Enabled 门控; B01: EventStore 构造期间装配; EvidenceStore 字段; nil-interface-trap 修复 |
| `internal/ares_bootstrap/retriever_wiring.go` | nil-interface-trap 修复 (akgModelName nil guard) |
| `cmd/ares/serve.go` | B01: 删除 SetEventStore 旁路 |
| `api/bootstrap/bootstrap.go` | 使用 comp.EvidenceStore 替代 comp.NewEvolution.EvidenceStore |
| `internal/ares_bootstrap/bootstrap_test.go` | 测试适配 Memory.Enabled=true |
| `api/integration/e2e_test.go` | 测试适配 Memory.Enabled=true |

## 测试结果

### 闭环测试 (closure build tag)

```
20 tests: 16 PASS, 4 SKIP
- TestClosure_MemoryDisabled_NotConstructed: PASS (F01 fixed)
- TestClosure_EvolutionDisabled_NoGATicker: PASS (F02 fixed)
- TestClosure_KnowledgeRetrievalEnabled: PASS (nil-interface-trap fixed)
- TestClosure_Ready_AllExecutorsBoundToLiveTargets: SKIP (F04 — needs entry-level test)
- TestClosure_Lifecycle_*: 7 PASS, 1 SKIP
- TestSharedInstance_*: 5 PASS, 1 SKIP (PatchRegistry — F04)
```

> 说明：F03（知识检索缺写依赖未 Ready）、F04（live-DAG 绑定）与 PatchRegistry
> 一致性检查已显式 Skip——其硬断言需要注册表报告 Degraded 状态或入口级测试，
> 不再以 PASS + t.Logf 记录差距（R09）。

### 常规测试 (make check)

```
make fmt: OK (gofmt + goimports)
make lint: OK (go vet + staticcheck + golangci-lint: 0 issues)
make test: OK (all packages PASS)
```

## 架构改进

1. **配置即契约**: `memory.enabled` 和 `evolution.enabled` 现在真正控制组件构造
2. **构造无副作用**: EventStore 在构造期间注入 Memory，不再后置旁路
3. **EvidenceStore 始终可用**: 即使 evolution 禁用，flight recorder 仍有 evidence store
4. **System Runtime 控制面**: 新包提供组件注册、依赖拓扑排序、状态机和生命周期编排
5. **nil 安全**: 修复了 nil-interface-trap panic bug

## 待完成（阶段 4-7）

以下阶段需要进一步授权和实施：

- **阶段 4**: 闭合所有真实数据反馈环 (Event → Evidence → GA → Strategy → Agent)
- **阶段 5**: Tools 与 MCP 进入真实依赖图
- **阶段 6**: 统一 serve/start/SDK 入口
- **阶段 7**: 真实部署门禁、故障恢复与长稳验收

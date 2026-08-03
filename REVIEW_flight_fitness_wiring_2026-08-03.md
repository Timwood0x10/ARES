# Review: Flight Fitness 写侧接线（2026-08-03）

> 独立复核。结论先行：**代码改动正确、验证全绿，但"闭环效果"目前仅在 `ares start` 路径实际成立；`ares serve` 路径虽已正确接入 EvidenceStore，却因 flight recorder 从未 `Start()` 而不发射任何 fitness。** 该 Start 缺口为改动前既有问题，本次未引入，但直接影响你描述的"ares serve → Bootstrap 写进 EvidenceStore"是否成立。

---

## 1. 验证结果（独立复跑，全绿）

| 项 | 命令 | 结果 |
|----|------|------|
| build | `go build ./...` | ✅ 0 错误 |
| 测试 | 6 包 `go test`（ares_bootstrap / api/bootstrap / api/service/flight / api/flight / api_impl / ares_flight） | ✅ 全 ok |
| lint | `golangci-lint run`（6 包） | ✅ 0 issues |

验证命令与用户报告一致，结论可信。

---

## 2. 改动正确性确认

逐文件核对（6 文件改动与描述完全吻合）：

- **`provide_evolution.go`**：`ProvideEvolution` 新增 `evStore evidence.Store` 末参，转发进 `FlightRecorderConfig.EvidenceStore`。doc comment 说明用途。✅
- **`bootstrap.go`**：装配顺序重排——`ProvideNewEvolution`（建 `newEvol.EvidenceStore`）提前到 `ProvideEvolution` 之前，`newEvol.EvidenceStore` 传入。`deps.EventStore/ExpRepo` 缺失时整体跳过，安全。✅
- **`api/service/flight/service.go`**：新增 `NewWithEvidenceStore(eventStore, evStore)`，`New` 委托传 nil，**向后兼容**。✅
- **`api/bootstrap/bootstrap.go`**：flight recorder 改用 `NewWithEvidenceStore`，`comp.NewEvolution != nil` 守卫后取 store。✅
- **`internal/api_impl/service.go`**：`s.bootstrap != nil && s.bootstrap.NewEvolution != nil` 双重 nil 守卫后取 `EvidenceStore`（nil 安全）。✅
- **`api/flight/flight.go`**：`Config` 新增 `EvidenceStore` 字段并转发。✅

**数据契约对齐（决定性）**：collector 在 `EvidenceStore != nil` 时建 `workflow`/`recovery`/`scheduler` 三个 evidence collector（`ares_flight/collector.go:52-62`），发射 `KindFitness`，payload key = `value`（成败值 0/1）；genome 端 `recovery_genome.go:117` 等按 `Source="recovery"/"workflow"/"scheduler"` 调 `avgFitnessValue` 读取 **同一** `EvidenceStore` 的 `value`。Source 名三端完全一致，shared store 同一实例。**接线逻辑本身正确。**

---

## 3. 关键发现 🔴（影响"闭环"是否真发生）

**flight recorder 必须 `Start()` 后其 collector 才订阅事件、才发射 fitness。** 全仓库 grep 结果：**只有 `internal/api_impl/service.go:262` 这一处真正 `Start()` 了 flight recorder。**

| 路径 | recorder 创建 | 是否 `Start()` | fitness 是否发射 |
|------|-------------|---------------|----------------|
| `ares start` → `api_impl.StartService`（`cmd/ares/start.go:62`） | ✅ api_impl/service.go:258 | ✅ **已 Start (262)** | ✅ **发射，闭环成立** |
| `ares serve` → `internal/ares_bootstrap.Bootstrap`（`cmd/ares/serve.go:125` → `ProvideEvolution`） | ✅ provide_evolution.go:50 | ❌ **从未 Start** | ❌ 不发射 |
| `api/bootstrap`（`ARES`） | ✅ bootstrap.go:140 | ❌ `ARES.Start()` 只启 Runtime（`bootstrap.go:161`） | ❌ 不发射（且该包仅为文档引用，无 Go 源码 import） |

**含义**：
- 你描述的"生产路径（ares serve → Bootstrap、api_impl.StartService、api/bootstrap）的 flight collector 会把 fitness 写进 EvidenceStore"——**目前只有 `ares start`/`api_impl` 这一条路径成立**。
- `ares serve` 路径里，`ProvideEvolution` 构造的 recorder 被包进 adapter 做数据源、但从未 `Start()`，其 collector 不订阅事件 → 零 fitness 进入 `EvidenceStore` → GA 对 workflow/scheduler/recovery 在该路径下仍是"盲进化"（拿不到真实反馈，退回之前 0.5 默认或历史空态）。
- 此缺口为**改动前既有**（`New` 旧实现同样未 Start），本次改动未引入回归，只是"接线就位但开关未拨"。

**需你确认**：生产实际跑的是 `ares serve` 还是 `ares start`？
- 若 `ares start` → 改动已闭环，可忽略下条。
- 若 `ares serve` → 需在 `Bootstrap` 路径补 `flightRecorder.Start(ctx)`（并让 `comp` 持有该 recorder 引用以便启动/清理），否则你这次接的 EvidenceStore 在生产里是"接好但不通电"。

---

## 4. 其他观察（非阻塞）

- **evidence store 为内存型**（`*evidence.MemoryStore`，`provide_new_evolution.go:34`）。fitness 仅进程内累积，重启即清空——与"进程内进化"设计一致，但意味着 GA 每次冷启动从零学起。符合当前架构，记录以备后续若要做长期学习时需换持久化 store。
- **emit 错误被忽略**：`collector.go:178/187/201` 用 `_ = c.xxxCollector.Emit(...)`。属 best-effort 遥测，可接受；与项目"不静默"原则不冲突（该原则针对配置/部署级故障，非单条事件）。
- **`api/bootstrap` 包实际未被任何 Go 源码 import**（仅 quick-start / api-client-guide 文档引用），属 dead code。本次改动保持其与另两条路径一致是对的，但可后续清理或补测试覆盖。
- 无并发/竞态隐患：store 为已存在的共享实例，多 goroutine 写经 evidence.Store 内部同步；nil 守卫齐全。

---

## 5. 建议

1. **（优先）确认生产入口**：若 `ares serve` 是目标，补 `Bootstrap` 路径的 `flightRecorder.Start(ctx)`，否则闭环在 prod 不成立。
2. **统一入口**：`ares serve`（Bootstrap）与 `ares start`（api_impl）并存且 flight recorder 生命周期不一致，建议明确单一生产入口，或让 `ares serve` 复用 api_impl 的 recorder 启动逻辑。
3. 可选：给 `emit` 失败加计数指标/日志，便于日后排查 fitness 缺失（目前完全静默）。

---

## 6. 结论

| 维度 | 评价 |
|------|------|
| 改动正确性 | ✅ 通过 |
| 数据契约（Source / store / value key） | ✅ 三端对齐 |
| 向后兼容 | ✅ `New` 委托 nil，Config 可选 |
| nil 安全 | ✅ 全调用点守卫 |
| 构建/测试/lint | ✅ 全绿 |
| **生产闭环** | ⚠️ **仅 `ares start` 生效；`ares serve` 因 recorder 未 Start 仍不发射** |

改动本身质量高、可合并；唯一需决断的是"生产到底跑哪个二进制"——这决定本次接线是否真的把 feedback 喂进 GA。

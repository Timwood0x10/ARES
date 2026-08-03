# Review: Flight Fitness 写侧 Start 修复（2026-08-03）

> 复核你针对上轮 review 🔴 发现的修复（ares serve 路径 recorder 未 Start）。结论先行：**主目标达成（ares serve 闭环✅），且首轮 review 抓出的 🔴 真 bug（`FlightRecorder` 字段从未赋值 → `Stop` cleanup 形同虚设）已于 2026-08-03 修复并复跑验证。** 尚有 1 个低危副作用（ares start 路径 fitness 双发，功能无碍）。

---

## 1. 验证结果（独立复跑，全绿）

| 项 | 命令 | 结果 |
|----|------|------|
| build | `go build ./...` | ✅ 无错 |
| 测试 | 7 包 `go test`（ares_bootstrap / api_impl / api/bootstrap / api/service/flight / api/flight / ares_flight / cmd/ares） | ✅ 全 ok |
| lint | `golangci-lint run`（ares_bootstrap / api_impl） | ✅ 0 issues |

与你的报告一致。

---

## 2. 修复正确性确认

- **`provide_evolution.go`**：`EvolutionComponents` 新增 `FlightRecorder *flight.FlightRecorder` 字段（doc 说明用途）✅；recorder 创建后 `flightRecorder.Start(ctx)` best-effort（失败仅 `log.WarnContext`，不使 bootstrap 失败）✅——**这一行让 ares serve 路径 collector 真正订阅事件、发射 fitness，主目标达成**。
- **`bootstrap.go`**：Step 7 后将 `Stop` 注册进 cleanups，且用 `if evol.FlightRecorder != nil` 守卫（避免 nil panic）✅——意图正确。

数据契约未变（Source 名 workflow/recovery/scheduler 一致、共享同 store、value key 一致），闭环逻辑本身正确。

---

## 3. 🔴→✅ 关键 Bug（已修复）：`FlightRecorder` 字段从未赋值 → Stop cleanup 永不注册

**状态（2026-08-03）**：已在 `provide_evolution.go` return 中补 `FlightRecorder: flightRecorder,`（单行），字段不再为 nil，`bootstrap.go:271` 的 `if evol.FlightRecorder != nil` 守卫可命中 → `Stop` 正常注册进 cleanups。复跑 build ✅ / 7 包测试全绿 ✅ / lint 0 issues ✅。

**原始现象**：`provide_evolution.go` 的 return（约 104-110 行）是：

```go
return &EvolutionComponents{
    Adapter:           adapter,
    Scheduler:         scheduler,
    DreamCycle:        dreamCycle,
    FeedbackService:   feedbackSvc,
    EvaluatorRegistry: evalRegistry,
}, nil
```

**没有 `FlightRecorder: flightRecorder,` 这一行。** 全文件 grep `FlightRecorder:` 赋值 → 0 命中。即字段声明了但**永远为 nil**。

**后果链**：
1. `comp.Evolution.FlightRecorder` 恒为 nil；
2. `bootstrap.go:271` 的 `if evol.FlightRecorder != nil` 守卫**永远为假** → `Stop` 从未注册进 cleanups；
3. 进程关闭时 collector goroutine 不被显式停止。

→ 你这次修复的**第二条目标"关闭时停 collector goroutine 防泄漏"并未实际达成**——`Stop` 是死代码。recorder 虽然 `Start` 了（闭环✅），但 `Stop` 不生效（防泄漏❌）。

> 注：若 bootstrap 的 `ctx` 在关闭时被取消，collectLoop 可能因 ctx cancel 自行退出（取决于 ctx 生命周期管理）；但这依赖隐式行为，而非你显式注册的 cleanup。若 ctx 在关闭时仍存活（仅靠进程退出回收），goroutine 会泄漏到进程结束。无论哪种，显式 cleanup 是你明确想要且写出来却没生效的，应当修。

**修复（1 行）**：在 return 中加 `FlightRecorder: flightRecorder,`。

---

## 4. ⚠️ 低危副作用：ares start 路径 fitness 双发

`ares start` → `api_impl.StartService` 在 `service.go:184` 调 `ares_bootstrap.Bootstrap` 内部、**又**在 `service.go:258-262` 自建并 `Start` 一个 recorder。修复前 Bootstrap 的 recorder 未 Start → 仅 api_impl 一个发射；修复后 **两个 recorder 都 Start**，且两者订阅**同一 EventStore**（`eventStore.CompactableEventStore` 经 deps 传入）、写**同一 EvidenceStore**（`newEvol.EvidenceStore` 同一实例）→ **同一事件被两个 collector 各发一次 fitness → 证据量翻倍、多一个 collector goroutine**。

**影响评估**：两个 recorder 发射的值完全相同（成功 1.0 / 失败 0.0），GA 取 `avgFitnessValue` 均值 → **均值在重复下不变，fitness 值不被扭曲**。属冗余/资源浪费，非正确性 bug。若后续 EvidenceStore 有容量/留存上限或两路值可能分化，需收敛为单一 recorder。

---

## 5. 结论

| 维度 | 评价 |
|------|------|
| ares serve 闭环（主目标） | ✅ 达成（Start 生效，fitness 发射） |
| build / test / lint | ✅ 全绿 |
| `Stop` cleanup 泄漏防护（次目标） | ✅ **已达成**（已补 `FlightRecorder` 赋值 → cleanup 注册） |
| ares start 双发 | ⚠️ 低危冗余（均值不变，无正确性影响） |

**建议**：ares start 双发可暂不改（功能无碍），但建议记一笔待后续收敛单 recorder。

---

## 6. 修复状态

2026-08-03 经用户授权，已补 `FlightRecorder: flightRecorder,` 一行（单行、向后兼容、不改其他逻辑），build/test/lint 全绿。原 🔴 bug 关闭。

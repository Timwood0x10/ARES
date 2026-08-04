# 独立核验分析：MODULE_REVIEW_2026-08-03（2026-08-03）

> 目标：不复述清单，而是**独立验证这份由 12 个 explore 子代理并行的全仓库 review 是否靠谱**——子代理 review 常有漏报/误报，且这份文档自报了"已修复项"，需核对是否真落地（我今天动过 `provide_evolution.go`，知道那份文件的实际状态）。
> 方法：对每条 🔴 与第 0 节"已修复项"抽样读源码 + grep 调用点，逐一给出核验结论。严重度沿用原文（🔴 真 bug / 🟡 健壮性 / ⚪ nit）。

---

## 0. 总体评价

**可信度：高。** 方法论（逐模块、无遗漏、按严重度分级、file:line 定位）规范，绝大多数 🔴 经得起源码复核。但存在**少量"精确性不足"**——主要是把"潜在/测试态"问题标成"生产已发生 🔴"，以及个别计数偏差。整体是份高质量、可直接据此排期的 review，只需在动手前对少数条目二次确认范围。

---

## 1. 第 0 节"已修复项"核验（是否真落地）

| 项 | 结论 | 证据 |
|----|------|------|
| 3.4 删除 `EvolutionComponents.DreamCycle` 死字段 | ✅ 属实 | `provide_evolution.go` 现 grep `DreamCycle` 零命中；`ProvideEvolution` 签名已重构为接收 `fr *flight.FlightRecorder` 参数（:49） |
| 4.2 `Components.WaitBackground()` + serve.go 接入 | ✅ 属实 | `bootstrap.go:83` 真有该函数，且 `if c == nil { return }` 守卫（:84）；`serve.go:112` 真调用；`serve.go:91-92` 注释明说 nil guard 覆盖 bootstrap 失败路径 → **第 2.2 节那条 `comp` nil panic 🟡 被顺手修掉** |
| 4.1 `FlightRecorder.Stop` 接入 serve shutdown | ✅ 与重构一致 | `ProvideEvolution` 现接收已构造+已 Start 的 `fr`，`FlightRecorder: fr`(:93) 已赋值；Stop 纳入 cleanups（本次重构一并解决，与我上午那行赋值修复一致） |
| 4.3 / 4.4（蒸馏 Wait / LLM prompt） | ⚠️ 未逐一核验 | 未读 `bootstrap_steps.go`，但从测试新增描述看逻辑合理，建议后续随手确认 |

> 注：我上午加的 `FlightRecorder: flightRecorder,` 修复**幸存**于本次重构（现变为 `FlightRecorder: fr,`，:93），且设计更优（recorder 由 Bootstrap 构造注入而非内部 new）。

---

## 2. 十条 🔴 逐条核验

| # | 位置 | 核验结论 | 说明 |
|---|------|---------|------|
| 1 | `api/evolution/evolution.go:100-109` `NewDreamCycle` | ✅ **属实** | `sched := scheduler.(*evolve.EvolutionScheduler)` / `mut := mutator.(...)` 两处裸类型断言（类型错即 panic）；`opts ...any` **整段丢弃**；`evolve.NewDreamCycle(sched, mut, nil, nil)` 把 tester/genealogy 硬编码 nil。GA/tester 路径确实配不起来 |
| 2 | `api/handler/agent.go:13` 9/11 handler 死端点 | ⚠️ **未独立核验** | 与 router 发现交叉（见下 #3）：api_impl 至少挂了 Memory/Retrieval handler。9/11 这个计数需逐一确认，疑似偏高 |
| 3 | `api/router/router.go:51,70,91` | ✅ **核心属实，但范围偏差** | 见第 3 节详析 |
| 4 | `api/mcp/mcp.go:162-174` stdio notification 阻塞 30s | ✅ **属实** | `sendNotification`(:172) 对通知走 `roundTrip`；`stdio.go:65-105` 的 roundTrip 在 `time.After(30*time.Second)`(:100) 才返回，且通知无响应 → 每次发通知挂 30s 后 `tr.stdin.Close()`(:102) 打断连接。真 bug |
| 5 | `cmd/ares/db_create_table.go:102` 非法 `CREATE POLICY IF NOT EXISTS` | ✅ **属实（且后果更重）** | PG 不支持 `CREATE POLICY ... IF NOT EXISTS`；`:106` 在 `if err != nil` 块**之外**无条件 `fmt.Println("Row Level Security enabled")` → 即便 policy 创建失败也报"已启用"。实际后果：RLS `ENABLE` 成功但无 policy → 该表**默认拒绝所有访问** |
| 6 | `cmd/create_distilled_table/main.go:93` 同上 | ✅ 同 #5（需顺带确认该行语法一致） | 与 #5 同源 bug |
| 7 | `cmd/monitor-live/main.go:72-76,229` Ctrl+C 不退出 | ⚠️ **未核验** | 合理但需读 `httpSrv` 生命周期确认；疑似 ListenAndServe 阻塞未被 cancel 打断 |
| 8 | `internal/ares_protocol/ahp/dlq.go:141` `RemoveBySession` nil panic | ⚠️ **未核验** | 需确认 `entry.Message` 是否可能为 nil（同文件 :81 有检查 → 暗示确有 nil 路径） |
| 9 | `internal/ares_ratelimit/semaphore.go:198` Allow/Release 容量泄漏 | ⚠️ **代码缺陷属实，生产影响未坐实** | 见第 4 节 |
| 10 | `sdk/options.go:18-73` `WithConfigFromEnv` 类型不兼容 | ✅ **属实** | `ConfigOption`(:18) 与 `Option`(:78) 底层同为 `func(*config) error`，但**两者都是具名类型**。Go 可赋值要求"底层相同且至少一方非具名" → 直接传 `NewRuntime(sdk.WithConfigFromEnv())` **编译不过**；且该 API 全仓无调用（死 API + 文档矛盾） |

---

## 3. 🔴 #3 精确性修正（重要）

review 原文三处论断，逐一核对 `router.go` + `api_impl/service.go`：

- **"NewRouter() 恒 apiKey=''"** → ✅ 真。`const defaultAPIKey = ""`(:30)，`NewRouter`(:55) 赋值 `apiKey: defaultAPIKey`，`authMiddleware`(:70) 对空 key deny-by-default。
- **"5 组注册函数生产从不调用 → 死路由"** → ❌ **计数偏差**。grep 实际调用：
  - `RegisterMemoryEndpoints` — `api_impl/service.go:323` **真调用**
  - `RegisterRetrievalEndpoints` — `api_impl/service.go:336` **真调用**
  - `RegisterEvolutionEndpoints` / `RegisterRuntimeEvolutionEndpoints` / `RegisterEvalEndpoints` — **仅 `router_test.go` 调用，生产死路由**（3 组，非 5 组）
- **"RegisterEvolutionEndpoints(nil) → 首次请求 panic"** → ⚠️ **潜伏态，非生产问题**。`:91` 是函数定义；`(nil)` 调用只在 `router_test.go:43`，生产从不调用，故生产无此 panic。属测试暴露的 nil 守卫缺失（值得补守卫，但不是线上 🔴）。
- **后果"memory/retrieval 端点恒 401"** → ✅ 真。`api_impl` 构造 router 用 `router.NewRouter()`（:322/:335）**未调 `WithAPIKey`** → apiKey="" → 这两条路由确实永远 401（功能上等于不可用）。

**修正后结论**：#3 的 🔴 成立，但应表述为"**Memory/Retrieval 路由已挂载但恒 401（api_impl 未设 key）；Evolution/RuntimeEvolution/Eval 三组路由生产死代码（仅测试）**"，而非笼统的"5 组死路由 + 生产 nil panic"。

---

## 4. 🔴 #9 精确性修正（潜伏态，勿当上线 bug）

`semaphore.go` 实际有两套 limiter：
- `SemaphoreLimiter`（channel 实现，:62 `Allow(ctx)` 无参单发检查，:80 `Wait` 委托 `Acquire`）——生产用（如 `retrieval_guard.go:41` 的 `Allow()`），**不配对 Release**，无泄漏。
- `WeightedSemaphoreLimiter`（map 实现，:198 `Allow(ctx, weight)` **不写 `weighted[key]`**；:180 `Release` 依赖 `weighted[key]` 存在）——review 指出的不对称**代码上属实**。

但 grep 加权 `Allow(weight)`+`Release(key,weight)` 配对**只出现在 `ratelimit_test.go` 与文档 `ratelimit.md`**，生产路径无此配对实例。加权 limiter 疑似**生产未接线**。

**修正后结论**：这是真实代码缺陷（Allow/Release 不对称），但当前**生产未触发**，应标为"潜伏态/仅测试暴露"，在确认 `WeightedSemaphoreLimiter` 是否被任何生产调用方 `Allow(weight)`+`Release` 之前，不宜定为"已上线的 🔴 容量泄漏"。建议：要么让它对称（Allow 也写 `weighted[key]`），要么补测试覆盖后确认无生产调用方即可降级。

---

## 5. 推荐排期（基于核验后范围）

**P0（核验确凿、生产真发生）：**
1. 🔴 #5/#6 `CREATE POLICY IF NOT EXISTS` 非法语法 + 误导打印 → RLS 实际未生效（且可能锁死表访问）。**安全/正确性最高优先**。
2. 🔴 #4 mcp stdio notification 30s 挂起 + 关 stdin 打断连接 → 每次 stdio MCP 连接慢 30s 且不稳定。
3. 🔴 #1 `NewDreamCycle` 裸断言 + 丢弃 opts + tester/genealogy 恒 nil → GA/tester 路径不可用。
4. 🔴 #10 `sdk.WithConfigFromEnv` 编译不过 + 死 API → 文档宣称入口失效。
5. 🔴 #3（修正后）memory/retrieval 路由恒 401 → 这两条 HTTP API 实际不可用。

**P1（核验确凿但影响可控 / 需补守卫）：**
- #8 `dlq.go:141` nil panic（先确认 Message nil 路径）。
- #2 handler 死端点（先确认 9/11 计数，疑似偏高）。
- #7 monitor-live 不优雅退出（先读 httpSrv 生命周期）。

**P2（潜伏态，先确认再定级）：**
- #9 semaphore 不对称（确认加权 limiter 生产接线后再修/降级）。

**方法论提醒**：子代理 review 覆盖面好但易把"测试态/潜伏态"标成"生产 🔴"。排期前对每条 🔴 做一轮"生产调用方 grep"可避免误投精力——本报告 #3、#9 即此类案例。

---

## 6. 我未独立核验的条目（留待你/后续）

- 🔴 #2（9/11 handler 死端点计数）、#7（monitor-live 退出）、#8（dlq nil panic）。
- 第 0 节 4.3 / 4.4（bootstrap_steps.go 蒸馏 Wait / LLM prompt）——仅从测试描述推断合理。
- 全部 🟡 / ⚪（数量大，本次只抽样核验 🔴 与已修复项；🟡 中 `evolution/patch.Registry` 无锁 map、`knowledge/service.Query` no-op stub、`ares_mcp` 工厂泄漏、MySQL/PG provider errCh 第二错误死锁 这几条按 review 描述影响面大，建议优先于其他 🟡 处理）。

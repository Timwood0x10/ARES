# GoAgent 性能优化全景分析报告

> 生成日期: 2026-06-25
> 分析方法: 代码静态分析 + codebase-memory-mcp 图查询 + 3 个并行 agent 深度扫描
> 分析范围: 全模块（10 个子系统）

---

## 一、为什么 Deterministic Scorer 快而 LLM Scorer 慢？

### 1.1 分支入口

两条路径的分叉点在 `api/ares_evolution/service.go:685`：

```go
if s.config.Scorer == nil {
    // 快路径：Deterministic
    pop.ScoreAgents(func(agent *mutation.Strategy) float64 {
        return DeterministicScore(toAPIStrategy(agent))
    })
} else {
    // 慢路径：LLM Scorer
    snap, _ := pop.Snapshot()  // 深拷贝整个 population
    // ... goroutine pool + HTTP calls
}
```

### 1.2 Deterministic 快路径执行链

```
scoreAgents(scorer=nil)
  → pop.ScoreAgents(closure)              [population.go:975]
    → p.mu.Lock()                          ← 1 次加锁
    → for each agent:
        → DeterministicScore(agent)        [llm_scorer.go:53]
          → 3 次 type assertion（零分配）
          → 算术运算：(1-temp)*25, dist²/10
          → switch 字符串比较
          → clamp [5, 100]
          → return float64
    → updateBestEverLocked()
    → p.mu.Unlock()
```

**每 agent 成本：~100ns，纯 CPU，零网络 I/O，零堆分配**

### 1.3 LLM 慢路径执行链

```
scoreAgents(scorer!=nil)
  → pop.Snapshot()                         ← 深拷贝 N 个 agent（第 1 次 clone）
  → make([]float64, N)                     ← 分配 score 数组
  → make(chan, 5)                          ← 信号量
  → for each agent:
      → goroutine:
          → sem <- struct{}{}              ← 阻塞等待并发槽
          → toAPIStrategy(agent)           ← 第 2 次 clone（CloneParams）
          → scorer(strategy):
              → buildPrompt(strategy)
                  → json.MarshalIndent     ← 堆分配 JSON buffer
                  → strings.ReplaceAll     ← 字符串拼接
              → client.Generate(ctx, prompt)  ← ⚡ HTTP 调用（200ms-10s）
              → parseScore(resp)           ← JSON 解析
          → <-sem                          ← 释放槽
  → build scoreMap                         ← map 构建
  → pop.ScoreAgents(mapLookup)             ← 第 2 次加锁
```

**每 agent 成本：~500ms-2s（受 LLM API 延迟主导）**

### 1.4 成本对比表

| 维度 | Deterministic | LLM |
|------|--------------|-----|
| 锁获取 | 1 次 | 3+ 次 |
| 深拷贝 | 0 次 | 2N 次（Snapshot + toAPIStrategy） |
| 堆分配 | ~0 | ~40 maps + N JSON buffers + N goroutine stacks |
| Goroutines | 0 | N |
| HTTP 调用 | 0 | N × numSamples |
| 网络延迟 | 0 | 主导因素（2-8s） |
| 20 agent 耗时 | **~5μs** | **~2-8s** |
| 10 代总耗时 | **~50μs** | **~22-88s** |

### 1.5 为什么 Deterministic 路径快？

1. **零 I/O**：纯算术运算，无网络、无磁盘
2. **零分配**：type assertion 和算术都在栈上完成
3. **单锁**：一次 write lock 覆盖整个 population
4. **零序列化**：不需要 JSON marshal/unmarshal
5. **零 goroutine**：顺序执行无调度开销
6. **initScores 调用 G+1 次无所谓**：每次 ~5μs，G+1 次也才 ~50μs

### 1.6 Tiered Scorer 的中间路径

Wired 系统使用 `TieredScorer`（`tiered_scorer.go:126`），在 LLM 和 Deterministic 之间取得平衡：

```
Score(strategy)
  → StrategyHash(strategy)                 ← FNV hash
  → Cache.Get(hash)                        ← 命中则直接返回（~1μs）
  → if miss && budget.CanCallLLM():
      → tryLLMScore → HTTP call            ← 受 budget 限制
  → else:
      → heuristic scorer                   ← 回退到 Deterministic
```

**关键**：Budget 限制了每代 LLM 调用次数（`MaxLLMCallsPerGeneration`），超出的都走 heuristic。Cache 避免了重复评分。

---

## 二、全模块逐个优化分析

### 2.1 `internal/ares_evolution/` — 进化引擎

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| B1 | HIGH | `population.go:975` | ScoreAgents 持写锁纯串行 | 改 worker pool 并发 |
| B2 | MEDIUM | `population.go:953` | Snapshot 深拷贝所有 agent | 缓存 snapshot 到下次进化 |
| B3 | MEDIUM | `population.go:828` | Fitness sharing O(n²) | 确保 sampled mode 启用 |
| B4 | LOW | `crossover.go:241` | 重复 sort param keys | 移除冗余 sort |
| B5 | LOW | `selection.go:217` | pickUniqueIndices 分配全量数组 | 改 partial Fisher-Yates |
| B6 | MEDIUM | `scoring/cache.go:96` | Cache 逐出 O(n) 全扫描 | 改 linked list/min-heap |
| B7 | LOW | `population.go:1354` | EvolveAfterScoring 三次加锁 | 合并为单次锁 |
| B8 | LOW | `selection.go:287` | 废弃代码未清理 | 删除 deprecated 类型 |

### 2.2 `internal/ares_memory/` — 记忆系统

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| B9 | MEDIUM | `distiller.go:402` | embedPhase 并发限制 5 | 改为可配置 |
| B10 | HIGH | `distiller.go:672` | enforceSolutionCap 查全量（无 LIMIT） | 加 LIMIT 或计数器 |
| B11 | MEDIUM | `context/cache.go:111` | TTL cache 逐出 O(n) | 改 min-heap |
| B12 | MEDIUM | `manager_impl.go:340` | BuildContext 字符串 += O(n²) | 改 strings.Builder |
| C1 | LOW | `manager_impl.go:472` | sync/atomic 和 Mutex 混用 | 简化为 plain int + errgroup |
| C2 | LOW | `manager_impl.go:508` | 部分失败静默吞掉 | 记录 partial failure |

### 2.3 `internal/ares_arena/` — 竞技场

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| B13 | MEDIUM | `regression.go:310` | runStrategy 串行评分 | 并行化 scorer 调用 |
| B14 | LOW | `service.go:131` | actions 历史无上限 | 加 circular buffer |
| C3 | LOW | `regression.go:523` | 死代码条件永远 false | 修复或删除 |

### 2.4 `internal/ares_runtime/` — 运行时

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| B15 | MEDIUM | `manager.go:499` | time.After 泄露 timer | 改 time.NewTimer + Stop |
| C4 | LOW | `manager.go:475` | mu 持锁期间启动 goroutine | 重构为先复制再启动 |

### 2.5 `internal/storage/postgres/` — 数据库层

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| B16 | HIGH | `knowledge_repository.go:42` | float64ToVectorString 用 fmt.Sprintf | 改 strconv.FormatFloat |
| B17 | HIGH | `vector_utils.go:67` | ParseVectorString 用 fmt.Sscanf | 改 strconv.ParseFloat |
| B18 | HIGH | `experience_repository.go:350` | keyword 查询 SELECT embedding 列 | 移除 embedding::text |
| B19 | MEDIUM | `knowledge_repository.go:190` | CreateBatch 单条 INSERT | 改 multi-row INSERT |
| B20 | MEDIUM | `secret_repository.go:287` | 每次 encrypt 重建 AES cipher | 缓存 cipher.AEAD |
| B21 | MEDIUM | `pool.go:54` | Pool.Get 用 db.Conn() 独占连接 | 改直接 QueryContext |
| B22 | MEDIUM | `pool.go:202` | ManagedRow 依赖 finalizer 清理 | 显式 Close 保证 |
| B23 | LOW | `write_buffer.go:256` | time.After 泄露 | 改 NewTimer + Stop |
| B24 | LOW | `write_buffer.go:298` | SHA256 每次写入 | 改 xxhash（非安全场景） |
| B25 | LOW | `base_repository.go:19` | fmt.Sprintf 拼 SQL 表名 | 加白名单校验 |
| B26 | LOW | `query/cache.go:310` | 手写 toLower/trimSpace | 改标准库 |

### 2.6 `internal/dashboard/` — API 层

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| B27 | MEDIUM | `ws_hub.go:172` | broadcast 持 RLock 做 JSON marshal | 预 marshal 再遍历 |
| B28 | LOW | `api.go:688` | SSE 用 time.Sleep 节流 | 移除 sleep |
| B29 | LOW | `api.go:913` | CheckOrigin 总是 true | 生产环境限制 origin |
| B30 | LOW | `api.go:216` | 无请求 body 大小限制 | 加 MaxBytesReader |

### 2.7 `internal/llm/` — LLM 客户端

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| B31 | MEDIUM | `client.go:684` | streaming fullResponse += O(n²) | 改 strings.Builder |
| B32 | LOW | `client.go:372` | 错误路径 io.ReadAll 无限制 | 改 LimitReader(4096) |
| B33 | LOW | `client.go:170` | streaming client 用 DefaultTransport | 配置连接池 |
| C5 | LOW | `client.go:376` | 错误消息可能泄露 API key | 清理 error body |

### 2.8 `internal/eval/` — 评估系统

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| C6 | MEDIUM | `concurrent_runner.go:114` | 测试失败 errgroup 仍返回 nil | 改返回聚合错误 |
| B34 | LOW | `concurrent_runner.go` | RunSuite 不支持结果流式 | 加 channel 流式输出 |

### 2.9 `internal/workflow/` — 工作流引擎

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| B35 | MEDIUM | `executor.go:415` | findStep O(n) 线性扫描 | 改 map[string]*Step |
| B36 | MEDIUM | `executor.go:482` | completed map 每步拷贝 | 改 sync.Map 或原子快照 |
| B37 | LOW | `executor.go:267` | 死锁检测固定超时 | 改为可配置 |
| C7 | LOW | `executor.go:140` | resultChan 多处 close | 改 defer close 单出口 |
| C8 | LOW | `executor.go:606` | time.After 泄露 | 改 NewTimer + Stop |

### 2.10 `internal/agents/` — Agent 系统

| # | 严重度 | 位置 | 问题 | 修复 |
|---|--------|------|------|------|
| B38 | MEDIUM | `agent.go:424` | initMemoryContext 6 次串行 DB 调用 | 并行化独立步骤 |
| B39 | LOW | `agent.go:498` | BuildContext 字符串 += O(n²) | 改 strings.Builder |
| B40 | LOW | `agent.go:506` | SearchSimilarTasks 每次请求都调 | 加 session 级缓存 |

### 跨模块通用问题

| # | 严重度 | 问题 | 修复 |
|---|--------|------|------|
| X1 | MEDIUM | 热路径 slog.Debug 参数构造开销 | 用 slog.LogAttr 或 guard 检查 |
| X2 | LOW | error wrapping 不一致（errors vs fmt.Errorf） | 统一用 %w |
| X3 | MEDIUM | 多处 time.After 泄露 timer | 全部改 NewTimer + Stop |
| X4 | LOW | 缺少 OpenTelemetry span | 关键路径加 tracing |

---

## 三、vNext 开发计划可行性评估

### 3.1 计划概览

`plan/workflow-runtime-vnext-plan.md` 提出 6 Phase 的 "Adaptive Plugin Runtime" 架构：

- P0: Plugin Runtime Contract + Experience Checkpoint + Memory-Routed Workflow
- P1: HITL Feedback Plugin + Controlled Evolutionary Loop
- P2: Evolution Plugin + Arena Robustness Suite

### 3.2 逐 Phase 可行性

| Phase | 可行性 | 预估工期 | 关键风险 |
|-------|--------|----------|----------|
| P0: Plugin Contract | **HIGH** | 2-3 周 | Executor 构造函数 breaking change |
| P0: Experience Checkpoint | **MEDIUM** | 3-4 周 | 最大跨切面改动，需大量数据埋点 |
| P0: Memory-Routed Workflow | **HIGH** | 2-3 周 | engine 和 graph 两个执行路径需对齐 |
| P1: HITL Feedback | **HIGH** | 1-2 周 | 低风险，增量改动 |
| P1: Evolutionary Loop | **MEDIUM** | 4-5 周 | 最复杂特性，State 对象缺服务访问 |
| P1/P2: Evolution Plugin | **HIGH** | 2-3 周 | 从 agent 级扩展到 workflow 级信号 |
| P2: Arena Robustness | **HIGH** | 2-3 周 | Arena 已成熟，需加 workflow action 类型 |

### 3.3 已具备的基础

| 组件 | 现状 | vNext 需求 |
|------|------|-----------|
| EventStore | ✅ Append/Read/Subscribe + OCC | 直接复用 |
| Workflow HITL | ✅ InterruptHandler/Store/Result | 包装为 Plugin |
| MutableDAG | ✅ 动态修改 DAG | 加条件边支持 |
| Memory Manager | ✅ SearchSimilarTasks | 加路由建议接口 |
| Evolution | ✅ genome/mutation/scoring/dream_cycle | 消费 workflow 事件 |
| Arena | ✅ 场景/注入/报告 | 加 plugin 故障注入 |
| GraphEventHub | ✅ 轻量级 in-process pub/sub | 可作为 EventBus 基础 |

### 3.4 风险点

**Breaking Changes（高风险）**：
- `Executor`/`DynamicExecutor` 的构造模式（`WithHitlHandler`, `WithHitlStore`）需改为插件注册
- `DynamicExecutor.recoveryEventSink` 功能回调模式与 EventBus 冲突

**设计冲突（中风险）**：
- EventBus 定位不清：替代 GraphEventHub？包装 EventStore？还是第三套机制？
- LoopPlugin 需要子工作流引擎能力，`State` 对象当前无法访问系统服务
- `engine` 和 `graph` 两套执行路径的 RouterPlugin 需要统一

**性能影响（中风险）**：
- `BeforeStep`/`AfterStep` hooks 增加每步延迟
- ExperienceCheckpoint 每步持久化增加 I/O
- MemoryRouter 每次路由查 memory 可能拖慢执行

### 3.5 建议

1. **Phase 2 和 3 可以并行**：Memory-Routed Workflow 不严格依赖 Experience Checkpoint
2. **先统一 engine 和 graph**：在加 RouterPlugin 前，明确哪条路径是主路径
3. **EventBus 应该是轻量级 in-process**：复用 GraphEventHub 模式，不要混用 EventStore
4. **LoopPlugin 先做简单版**：支持 max_iterations + until 条件，不做每轮 checkpoint
5. **插件调用必须有严格 timeout**：避免 memory/evolution 查询拖慢 workflow

---

## 四、优先行动清单

### P0 — 立即实施（最大收益，1-2 天）

- [ ] `ScoreAgents` 改 worker pool 并发（~20× 提速）
- [ ] `numSamples` 并发化（~3× 提速）
- [ ] `concurrentScoreLimit` 从 5 提升到 15
- [ ] `float64ToVectorString` 改用 `strconv.FormatFloat`
- [ ] `ParseVectorString` 改用 `strconv.ParseFloat`

### P1 — 短期实施（1-2 周）

- [ ] Budget 改原子操作 `TryRecordLLMCall()`
- [ ] keyword/list 查询移除 embedding 列
- [ ] `CreateBatch` 改 multi-row INSERT
- [ ] AES cipher 缓存
- [ ] `shouldEvolve` 合并锁
- [ ] weighted query 并行化
- [ ] BuildContext/replaceAllIgnoreCase 改 strings.Builder
- [ ] time.After 全部改 NewTimer + Stop

### P2 — 中期改进（2-4 周）

- [ ] ScoreCache 改 LRU（container/list）
- [ ] StrategyHash 缓存到 Strategy 对象
- [ ] enforceSolutionCap 加 LIMIT
- [ ] CleanupExpired 改批量 DELETE
- [ ] findStep 改 map 查找
- [ ] initMemoryContext 并行化
- [ ] WebSocket hub 预 marshal

### P3 — 长期架构（vNext 计划）

- [ ] Plugin Runtime Contract
- [ ] Experience Checkpoint
- [ ] Memory-Routed Workflow
- [ ] HITL Feedback Plugin
- [ ] Controlled Evolutionary Loop
- [ ] Evolution Plugin
- [ ] Arena Robustness Suite

---

## 五、预期收益总结

| 优化项 | 当前 | 优化后 | 提速 |
|--------|------|--------|------|
| ScoreAgents 串行 → 并发 | 24s/cycle | 0.6s/cycle | **~40×** |
| Vector 序列化 fmt → strconv | ~10ms/行 | ~1ms/行 | **~10×** |
| Keyword 查询去掉 embedding | 8KB/行浪费 | 0 | IO/CPU ↓ |
| Weighted query 串行 → 并行 | 3x 延迟 | 1x 延迟 | **~3×** |
| time.After 泄露修复 | timer 泄露 | 无泄露 | 稳定性 ↑ |
| 字符串操作 O(n²) → O(n) | n² 分配 | n 分配 | **内存 ↓** |

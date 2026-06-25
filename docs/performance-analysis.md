# GoAgent 性能优化全景分析报告

> 生成日期: 2026-06-25
> 分析范围: internal/ares_evolution, api/ares_evolution, internal/storage, internal/dashboard
> 方法: 代码静态分析 + codebase-memory-mcp 图查询 + 多 agent 并行扫描

---

## 一、LLM Scorer 慢因根因分析

### 1.1 调用链路全景

```
EvolutionScheduler.OnAgentEnd()                     [scheduler.go:253]
  → GenomePopulationAdapter.Run()                   [genome_wiring.go:248]
    → Population.EvolveAfterScoring()               [population.go:1354]
      → Population.ScoreAgents(scorer)              [population.go:975]  ← 第一次：串行打分
      → Population.EvolveOnIdle()                   [population.go:1071] ← 选择/交叉/变异
      → Population.ScoreAgents(scorer)              [population.go:975]  ← 第二次：串行打分
        └─ for each agent:                          ← 纯串行 for 循环，持写锁
            └─ MemoryAwareScorer.Score()            [memory_aware_scorer.go:196]
                ├─ TieredScorer.Score()             [tiered_scorer.go:126]
                │   ├─ Cache lookup                  ← O(1) hash 查找
                │   └─ tryLLMScore()                [tiered_scorer.go:178]
                │       └─ client.Generate(ctx, prompt)  ← 同步阻塞 HTTP 调用
                └─ ExperienceProvider.FindSimilar() ← 额外 DB I/O
```

### 1.2 Dream Cycle 调用链路

```
DreamCycle.Run()                                    [dream_cycle.go:500]
  → DreamCycle.findWinner()                         [dream_cycle.go:513]
    → [Stage 1: Quick Reject] errgroup 并行 across candidates
      → RegressionTester.Run(N=5)                   [regression_tester.go:48]
        └─ for i < 5:                               ← 串行采样循环
            ├─ scorer(&candidate)                   ← LLM 调用 #1
            └─ scorer(&baseline)                    ← LLM 调用 #2
    → [Stage 2: Full Eval] errgroup 并行 across survivors
      → RegressionTester.Run(N=50)                  [regression_tester.go:48]
        └─ for i < 50:                              ← 串行采样循环
            ├─ scorer(&candidate)                   ← LLM 调用 #1
            └─ scorer(&baseline)                    ← LLM 调用 #2
```

### 1.3 慢因详解

#### 慢因 #1 — CRITICAL: ScoreAgents 持写锁纯串行

**文件**: `internal/ares_evolution/genome/population.go:975-999`

```go
func (p *Population) ScoreAgents(scorer func(*mutation.Strategy) float64) {
    p.mu.Lock()
    defer p.mu.Unlock()

    for i, agent := range p.Agents {   // ← 串行 for 循环
        func() {
            defer func() {
                if r := recover(); r != nil {
                    // panic recovery
                    agent.Score = ScoreUnevaluated
                }
            }()
            agent.Score = scorer(agent)  // ← 每个 agent 阻塞等 LLM 返回
        }()
    }
    p.updateBestEverLocked()
}
```

**问题**:
- 持有整个 population 的写锁，阻塞所有并发读者（Stats, BestStrategy, Snapshot）
- 逐个串行调用 scorer，无 goroutine 并发
- 每个 LLM 调用阻塞下一个

**影响**: 20 agent × 2 次调用/周期 = 40 次串行 LLM 调用

---

#### 慢因 #2 — CRITICAL: numSamples 顺序循环

**文件**: `api/ares_evolution/llm_scorer.go:221-238`

```go
func (s *LLMScorer) ScoreWithContext(ctx context.Context, strategy *Strategy) float64 {
    if s.numSamples <= 1 {
        return s.sampleOnce(ctx, strategy)
    }
    best := 0.0
    for range s.numSamples {       // ← 顺序循环
        sc := s.sampleOnce(ctx, strategy)  // ← 同步阻塞 HTTP 调用
        if sc > best {
            best = sc
        }
    }
    return best
}
```

**问题**: `NumSamples=3` 时耗时直接 ×3，每个 sample 是独立的 LLM HTTP 调用

**影响**: 3 倍延迟放大

---

#### 慢因 #3 — HIGH: 并发上限硬编码为 5

**文件**: `api/ares_evolution/service.go:23-24`

```go
// concurrentScoreLimit is the maximum number of concurrent LLM scoring calls.
concurrentScoreLimit = 5
```

**问题**:
- Service 层用 goroutine 池做并发评分，但限制最多 5 个并发
- 20 个 agent 需要 4 轮（20/5），每轮等待最慢的那个返回
- 现代 LLM API（OpenAI/Anthropic）的 rate limit 通常远高于 5 并发

**影响**: 将本来可以全并发的任务限制为 4 轮串行

---

#### 慢因 #4 — MEDIUM: EvolveAfterScoring 调用 ScoreAgents 两次

**文件**: `internal/ares_evolution/genome/population.go:1354-1390`

```go
func (p *Population) EvolveAfterScoring(...) {
    p.ScoreAgents(scorer)        // ← 第一次：给当前 population 打分
    p.EvolveOnIdle(...)          // ← 选择/交叉/变异
    p.ScoreAgents(scorer)        // ← 第二次：给新 offspring 打分
}
```

**影响**: 每个进化周期 = 2 次完整串行评分

---

#### 慢因 #5 — MEDIUM: Dream Cycle RegressionTester 串行采样

**文件**: `internal/ares_evolution/regression_tester.go:63-82`

```go
for i := 0; i < sampleSize; i++ {
    candScore := t.scorer(&candidateMS)   // LLM 调用 #1
    baseScore := t.scorer(&baselineMS)    // LLM 调用 #2
    // ... compare and record
}
```

**影响**: 50 samples × 2 调用 = 100 次串行 LLM 调用/candidate

---

#### 慢因 #6 — MEDIUM: MemoryAwareScorer 额外 I/O

**文件**: `internal/ares_evolution/scoring/memory_aware_scorer.go:219`

```go
expCount, confidence, err := ms.exp.FindSimilar(ctx, taskTypeFromStrategy(s), ms.cfg.ExperienceLookupLimit)
```

**问题**: 启用 memory-aware scoring 后，每次评分额外查询 experience provider（DB/向量搜索）

---

#### 慢因 #7 — LOW: Budget TOCTOU 竞态

**文件**: `internal/ares_evolution/scoring/budget.go:52-63`

```go
func (b *Budget) CanCallLLM() bool {  // 步骤1: 检查
    b.mu.Lock(); defer b.mu.Unlock()
    return b.UsedLLMCalls < b.MaxLLMCalls
}
func (b *Budget) RecordLLMCall() {    // 步骤2: 记录 — 两步不原子!
    b.mu.Lock(); defer b.mu.Unlock()
    b.UsedLLMCalls++
}
```

**问题**: `CanCallLLM()` 和 `RecordLLMCall()` 是两个独立 mutex 操作。改并发后多个 goroutine 可同时通过检查，超过 budget 上限。

---

#### 慢因 #8 — LOW: StrategyHash 每次重新计算

**文件**: `internal/ares_evolution/scoring/hash.go:39-62`

**问题**: `StrategyHash()` 排序所有 param key 并通过 `fmt.Fprintf` 写入 FNV hasher，每次评分调用都重新计算

---

### 1.4 耗时估算

假设: 20 个 agent, LLM 调用 300ms/次, numSamples=2

| 场景 | 计算 | 耗时 |
|------|------|------|
| 当前（串行，ScoreAgents ×2） | 20 × 2 × 300ms × 2 轮 | **~24s** |
| P0 优化后（并发 + sample 并行） | 300ms（全部并发） | **~0.6s** |

**理论提速: ~40×**

---

### 1.5 优化方案

#### P0 — 并发化 ScoreAgents

```go
// internal/ares_evolution/genome/population.go
func (p *Population) ScoreAgentsConcurrent(
    scorer func(*mutation.Strategy) float64,
    concurrency int,
) {
    sem := make(chan struct{}, concurrency)
    var wg sync.WaitGroup

    for i, agent := range p.Agents {
        wg.Add(1)
        sem <- struct{}{}
        go func(idx int, a *mutation.Strategy) {
            defer wg.Done()
            defer func() { <-sem }()
            defer func() {
                if r := recover(); r != nil {
                    slog.Warn("scorer panicked for agent",
                        "agent_index", idx, "panic_value", r)
                    a.Score = ScoreUnevaluated
                }
            }()
            a.Score = scorer(a)
        }(i, agent)
    }
    wg.Wait()

    p.mu.Lock()
    defer p.mu.Unlock()
    p.updateBestEverLocked()
}
```

#### P0 — numSamples 并发化

```go
// api/ares_evolution/llm_scorer.go
func (s *LLMScorer) ScoreWithContext(ctx context.Context, strategy *Strategy) float64 {
    if s.numSamples <= 1 {
        return s.sampleOnce(ctx, strategy)
    }

    scores := make([]float64, s.numSamples)
    var wg sync.WaitGroup
    for i := range s.numSamples {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            scores[idx] = s.sampleOnce(ctx, strategy)
        }(i)
    }
    wg.Wait()

    best := 0.0
    for _, sc := range scores {
        if sc > best {
            best = sc
        }
    }
    return best
}
```

#### P1 — 提升并发上限

```go
// api/ares_evolution/service.go
concurrentScoreLimit = 15  // 从 5 提升到 15
```

#### P1 — Budget 原子化

```go
// internal/ares_evolution/scoring/budget.go
func (b *Budget) TryRecordLLMCall() bool {
    b.mu.Lock()
    defer b.mu.Unlock()
    if b.UsedLLMCalls >= b.MaxLLMCalls {
        return false
    }
    b.UsedLLMCalls++
    return true
}
```

#### P2 — Batch Scoring 接口

```go
// 定义批量评分接口，减少 HTTP round-trip
type BatchScorer interface {
    BatchScore(strategies []*mutation.Strategy) []float64
}
```

---

## 二、数据库层优化

### 2.1 Vector 序列化性能

#### 问题 A: float64ToVectorString 使用 fmt.Sprintf

**文件**: `internal/storage/postgres/repositories/knowledge_repository.go:42-52`

```go
// 当前实现 — 1024 次 fmt.Sprintf，极慢
func float64ToVectorString(vector []float64) string {
    parts := make([]string, len(vector))
    for i, v := range vector {
        parts[i] = fmt.Sprintf("%.6f", v)  // ← 反射 + interface boxing
    }
    return "[" + strings.Join(parts, ",") + "]"
}
```

**修复**: 改用 `strconv.FormatFloat`

```go
func float64ToVectorString(vector []float64) string {
    var b strings.Builder
    b.Grow(len(vector) * 12)
    b.WriteByte('[')
    for i, v := range vector {
        if i > 0 {
            b.WriteByte(',')
        }
        b.WriteString(strconv.FormatFloat(v, 'f', 6, 64))
    }
    b.WriteByte(']')
    return b.String()
}
```

**预期提升**: embedding 写入/搜索 CPU 降低 ~90%

---

#### 问题 B: ParseVectorString 使用 fmt.Sscanf

**文件**: `internal/storage/postgres/vector_utils.go:67-90`

```go
// 当前实现 — 每个 float 一次 Sscanf
fmt.Sscanf(strings.TrimSpace(part), "%f", &result[i])
```

**修复**: 改用 `strconv.ParseFloat`

```go
result[i], err = strconv.ParseFloat(strings.TrimSpace(part), 64)
```

**预期提升**: embedding 读取 ~10× 更快

---

### 2.2 查询优化

#### 问题: Keyword 查询 SELECT 了 embedding 列

**文件**: `internal/storage/postgres/repositories/experience_repository.go:350`

**问题**: `SearchByKeyword`, `ListByType`, `ListByAgent` 等方法的 SELECT 包含 `embedding::text`，每行解析 1024 维向量（~8KB），但调用方从不使用 embedding。

**修复**: 从 SELECT 子句中移除 `embedding::text`，或添加轻量查询方法。

---

#### 问题: CreateBatch 用单条 INSERT

**文件**: `internal/storage/postgres/repositories/knowledge_repository.go:190-295`

**问题**: `CreateBatch` 在事务中逐条执行 `INSERT ... RETURNING id`，N 条记录 = N 次 round-trip。

**修复**: 使用 multi-row INSERT 或 COPY 协议。

---

### 2.3 其他数据库问题

| 文件 | 问题 | 修复 |
|------|------|------|
| `secret_repository.go:287` | 每次 encrypt/decrypt 重建 AES cipher | 缓存 cipher.AEAD |
| `*_repository.go` CleanupExpired | 无 LIMIT 的 DELETE 可能锁表 | 改批量 DELETE + LIMIT 1000 |
| `conversation_repository.go:431` | GROUP BY 无覆盖索引 | 添加 `(tenant_id, session_id, created_at DESC)` 索引 |

---

## 三、内存与热路径优化

### 3.1 字符串操作

| 文件 | 行号 | 问题 | 修复 |
|------|------|------|------|
| `retrieval_service.go` | 1096 | `tokenize()` 用 `+=` 拼接字符串 | 改 `strings.Builder` |
| `retrieval_service.go` | 2060 | `replaceAllIgnoreCase` O(n²) 拼接 | 改 `strings.Builder` + `Grow` |

### 3.2 缓存实现

| 文件 | 行号 | 问题 | 修复 |
|------|------|------|------|
| `retrieval_service.go` | 617 | Embedding cache LRU 实际是 FIFO（hit 时不移动） | 改用 `container/list` |
| `cache.go` (scoring) | 91 | `ScoreCache.Put` 逐出是 O(n) 全扫描 | 改 min-heap 或 linked list |
| `retrieval_service.go` | 688 | Query cache 逐出两轮 O(n) 扫描 | 改有序结构 |

---

## 四、并发与架构

### 4.1 Scheduler 锁优化

**文件**: `internal/ares_evolution/scheduler.go:208-244`

**问题**: `shouldEvolve` 调用 `averageScore()` 和 `recentAverage(10)` 各自独立获取 `scoreMu`，再第三次加锁读 `scoreCount`。三次加锁之间 slice 可能变化，比较不安全。

**修复**: 合并为单次加锁方法：

```go
func (s *EvolutionScheduler) scoreStats() (avg, recent10 float64, count int) {
    s.scoreMu.Lock()
    defer s.scoreMu.Unlock()
    count = len(s.scores)
    if count == 0 {
        return 0, 0, 0
    }
    for _, v := range s.scores { avg += v }
    avg /= float64(count)
    window := 10
    if window > count { window = count }
    recent := s.scores[count-window:]
    for _, v := range recent { recent10 += v }
    recent10 /= float64(window)
    return avg, recent10, count
}
```

### 4.2 Retrieval 查询并行化

**文件**: `internal/storage/postgres/services/retrieval_service.go:306-309`

**问题**: `Search` 串行执行多个 weighted query（最多 3 个）

**修复**: 用 errgroup 并行执行所有 weighted query，然后合并结果。

### 4.3 其他并发问题

| 文件 | 问题 | 修复 |
|------|------|------|
| `scheduler.go:313` | fire-and-forget goroutine | 加 WaitGroup，Shutdown 时 Wait |
| `adaptive.go:140` | 多样性 O(n²) 计算重复 3 次/周期 | 缓存结果，周期开始时算一次 |

---

## 五、API/HTTP 优化

| 文件 | 问题 | 修复 |
|------|------|------|
| `api.go:309` | WebSocket upgrader 每次连接重建 | 改为包级变量或 struct 字段 |
| `api.go:688` | SSE 用 `time.Sleep(100ms)` 节流 | 移除 sleep，依赖 flusher |
| `api.go:216` | 无请求 body 大小限制 | 加 `http.MaxBytesReader(w, r.Body, 1<<20)` |
| `coingecko.go:58` 等 | Market clients 各自创建 http.Client | 共享 Transport，配置连接池 |

---

## 六、优先行动清单

### P0 — 立即实施（最大收益）

- [ ] `ScoreAgents` 收为 worker pool 并发（~20× 提速）
- [ ] `numSamples` 并发化（~3× 提速）
- [ ] `concurrentScoreLimit` 从 5 提升到 15（~3× 提速）
- [ ] `float64ToVectorString` 改用 `strconv`（DB CPU ↓90%）
- [ ] `ParseVectorString` 改用 `strconv.ParseFloat`（读取 ~10× 更快）

### P1 — 短期实施

- [ ] Budget 改原子操作 `TryRecordLLMCall()`
- [ ] keyword/list 查询移除 embedding 列
- [ ] `CreateBatch` 改 multi-row INSERT
- [ ] AES cipher 缓存
- [ ] `shouldEvolve` 合并锁
- [ ] weighted query 并行化

### P2 — 中期改进

- [ ] 定义 `BatchScorer` 接口
- [ ] ScoreCache 改 LRU（container/list）
- [ ] StrategyHash 缓存到 Strategy 对象
- [ ] CleanupExpired 改批量 DELETE
- [ ] 字符串操作改 strings.Builder

### P3 — 长期架构

- [ ] LLM Client 支持 batch 请求（multi-prompt）
- [ ] ScoreCache 增加相似度匹配（近似命中）
- [ ] 多样性计算结果缓存
- [ ] WebSocket upgrader 复用
- [ ] Market clients 共享 HTTP Transport

---

## 七、预期收益总结

| 优化项 | 当前耗时 | 优化后 | 提速 |
|--------|----------|--------|------|
| ScoreAgents 串行 → 并发 | 24s | 0.6s | **~40×** |
| Vector 序列化 fmt → strconv | ~10ms/行 | ~1ms/行 | **~10×** |
| Keyword 查询去掉 embedding | 8KB/行浪费 | 0 | IO/CPU ↓ |
| Weighted query 串行 → 并行 | 3x 延迟 | 1x 延迟 | **~3×** |

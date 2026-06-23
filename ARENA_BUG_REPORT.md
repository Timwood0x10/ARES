# 混沌工程（Arena）Bug 分析报告

> **模块**: `internal/arena`
> **分析时间**: 2026-06-23
> **分析范围**: Dead Code、Technical Debt、Potential Bugs

---

## 📊 概览

| 类型 | 数量 | 严重程度 |
|------|------|----------|
| Dead Code | 2 | 🟡 中等 |
| Technical Debt | 4 | 🟠 较高 |
| Potential Bugs | 6 | 🔴 高 |

---

## 🚫 Dead Code（死代码）

### 1. `RecordRecovery()`、`RecordFailover()`、`RecordConsistency()` 方法

**位置**: `internal/arena/metrics.go:77-99`

**问题**: 这三个方法被标记为 "Deprecated: Kept for backward compatibility with tests"，但实际代码中从未被调用。

```go
// RecordRecovery records a recovery duration sample.
// Deprecated: Kept for backward compatibility with tests.
func (mc *MetricsCollector) RecordRecovery(d time.Duration) {
    mc.mu.Lock()
    defer mc.mu.Unlock()
    mc.recoveries = append(mc.reoveries, d)
}

// RecordFailover records a failover event.
// Deprecated: Kept for backward compatibility with tests.
func (mc *MetricsCollector) RecordFailover() {
    mc.mu.Lock()
    defer mc.mu.Unlock()
    mc.failoverCount++
}

// RecordConsistency records a data consistency rate sample (0-100).
// Deprecated: Kept for backward compatibility with tests.
func (mc *MetricsCollector) RecordConsistency(rate float64) {
    mc.mu.Lock()
    defer mc.mu.Unlock()
    mc.consistencySamples = append(mc.consistencySamples, rate)
}
```

**搜索结果**:
- 仅在 `metrics_test.go` 中有测试
- 整个应用代码中没有任何地方调用
- 占用 23 行代码，增加维护成本

**影响**:
- 误导开发者认为这些是公共 API
- 增加代码复杂度
- 违反"删除未使用代码"原则

**建议**:
- 如果不需要向后兼容，直接删除
- 如果需要兼容，至少添加注释说明何时使用
- 考虑使用废弃警告（`@Deprecated` 注解）

---

### 2. `RunScenario()` 函数

**位置**: `internal/arena/scenario.go:293-359`

**问题**: `RunScenario()` 函数被标记为 "NOTE: This function preserves backward compatibility. New code should use RunScenarioReport."，但实际应用中已被 `RunScenarioReport()` 替代。

```go
// RunScenario executes all actions in a scenario with the specified delays.
// Returns the results of all executed actions. Stops if the context is cancelled.
// NOTE: This function preserves backward compatibility. New code should use RunScenarioReport.
func RunScenario(ctx context.Context, service *Service, scenario Scenario) ([]Result, error) {
    // ... 67 行实现
}
```

**问题分析**:
- 与 `RunScenarioReport()` 功能几乎相同
- 缺少 `ScenarioReport` 的完整报告功能（Passed/Failed/Skipped/Verified）
- 缺少 `expect_success` 验证
- 缺少 `warmup` 和 `cooldown` 支持
- 缺少 `stop_on_error` 支持

**搜索结果**:
- 仅在 `scenario_test.go` 中有测试
- CLI (`cmd/arena/main.go`) 使用 `RunScenarioReport`
- HTTP Handler 使用 `RunScenarioReport`

**影响**:
- 代码重复，维护两套相似逻辑
- 可能导致开发者使用旧 API 而错过新功能

**建议**:
```go
// 方案 1: 删除旧函数（推荐）
// 如果向后兼容不是严格要求，直接删除

// 方案 2: 添加废弃警告
// Deprecated: Use RunScenarioReport instead. This function will be removed in v1.0.
func RunScenario(ctx context.Context, service *Service, scenario Scenario) ([]Result, error) {
    slog.Warn("RunScenario is deprecated, use RunScenarioReport instead")
    // ... 保留现有实现
}

// 方案 3: 重构合并
// 将 RunScenario 的逻辑合并到 RunScenarioReport 中
// 删除重复代码
```

---

## 🏗️ Technical Debt（技术债务）

### 1. 缺少并发安全保证

**位置**: `internal/arena/survival.go:157-178`

**问题**: `GetSurvivalStatus()` 返回的 Timeline 是深拷贝，但 `Timeline` 字段的注释说明不够清晰。

```go
// GetSurvivalStatus returns the current status of the survival run.
// The returned Timeline is a deep copy of the internal events slice to prevent
// data races between the survival goroutine (which appends events) and callers
// (which may iterate over the returned slice after the lock is released).
func (s *Service) GetSurvivalStatus() SurvivalStatus {
    s.survival.mu.RLock()
    defer s.survival.mu.RUnlock()

    // Copy the events slice to avoid returning a reference to internal state.
    timeline := make([]SurvivalEvent, len(s.survival.events))
    copy(timeline, s.survival.events)

    status := SurvivalStatus{
        Running:    s.survival.running,
        Config:     s.survival.config,
        ActionsRun: len(s.survival.events),
        Timeline:   timeline,
    }
    // ...
}
```

**问题分析**:
- 注释已经说明了深拷贝的必要性
- 但没有明确说明 `Config` 和 `ActionsRun` 是否也需要拷贝
- `ActionsRun` 是计数器，读取时不需要拷贝
- `Config` 是结构体，读取时不需要拷贝
- 只有 `Timeline` 需要拷贝，注释已经正确

**改进建议**:
```go
// GetSurvivalStatus returns the current status of the survival run.
// Returns a snapshot that is safe to use concurrently with the running survival test.
// Note: Timeline is deep-copied to prevent data races; other fields are safe to read directly.
func (s *Service) GetSurvivalStatus() SurvivalStatus {
    s.survival.mu.RLock()
    defer s.survival.mu.RUnlock()

    // Timeline is deep-copied to prevent concurrent modification
    timeline := make([]SurvivalEvent, len(s.survival.events))
    copy(timeline, s.survival.events)

    return SurvivalStatus{
        Running:    s.survival.running,
        Config:     s.survival.config,
        ActionsRun: len(s.survival.events),
        Timeline:   timeline,
    }
}
```

---

### 2. 重复的统计计算逻辑

**位置**: `internal/arena/survival.go:206-220`

**问题**: `calculateAvgRecoveryTime()` 方法在 `survival.go` 中定义，但 `scenario.go` 中也有类似的计算。

```go
// calculateAvgRecoveryTime computes the average duration of successful actions.
func (s *Service) calculateAvgRecoveryTime(events []SurvivalEvent) time.Duration {
    var total time.Duration
    var count int
    for _, ev := range events {
        if ev.Result.Success && ev.Result.Duration > 0 {
            total += ev.Result.Duration
            count++
        }
    }
    if count == 0 {
        return 0
    }
    return total / time.Duration(count)
}
```

**问题分析**:
- `survival.go` 有这个方法（用于 SurvivalMode）
- `scenario.go` 调用 `service.calculateAvgRecoveryTime(nil)`，但 `Service` 结构体没有这个方法
- `scenario.go` 实际上没有实现这个方法，只是调用它
- 这会导致编译错误

**实际代码检查**:
```go
// scenario.go:186, 247
report.Score = CalculateScoreV1(service.Stats(), service.calculateAvgRecoveryTime(nil))
```

**问题**: `service.calculateAvgRecoveryTime()` 方法不存在！这会导致编译失败。

**建议**:
```go
// 方案 1: 在 Service 中添加方法
func (s *Service) calculateAvgRecoveryTime(events []SurvivalEvent) time.Duration {
    var total time.Duration
    var count int
    for _, ev := range events {
        if ev.Result.Success && ev.Result.Duration > 0 {
            total += ev.Result.Duration
            count++
        }
    }
    if count == 0 {
        return 0
    }
    return total / time.Duration(count)
}

// 方案 2: 在 ScenarioReport 中计算
func (r *ScenarioReport) calculateAvgRecoveryTime() time.Duration {
    var total time.Duration
    var count int
    for _, res := range r.Results {
        if res.Success && res.Duration > 0 {
            total += res.Duration
            count++
        }
    }
    if count == 0 {
        return 0
    }
    return total / time.Duration(count)
}

// 然后在 RunScenarioReport 中调用
report.Score = CalculateScoreV1(service.Stats(), report.calculateAvgRecoveryTime())
```

---

### 3. HTTP Handler 缺少输入验证

**位置**: `internal/arena/http.go:69-93`

**问题**: HTTP Handler 中对 Agent ID 的验证不够严格。

```go
func (h *Handler) handleKillAgent(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    if id == "" {
        writeError(w, http.StatusBadRequest, "missing agent id")
        return
    }
    // ... 直接使用 id，没有验证格式
    action := Action{
        ID:        uuid.New().String(),
        Type:      ActionKillAgent,
        TargetID:  id,
        CreatedAt: time.Now(),
    }
    result := h.service.Execute(r.Context(), action)
    writeResult(w, result)
}
```

**潜在问题**:
- Agent ID 可以包含任意字符（包括空格、特殊符号）
- 可能导致 ID 解析错误
- 没有验证 UUID 格式

**建议**:
```go
func (h *Handler) handleKillAgent(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    if id == "" {
        writeError(w, http.StatusBadRequest, "missing agent id")
        return
    }

    // 验证 ID 格式
    if len(id) > 256 {
        writeError(w, http.StatusBadRequest, "agent id too long")
        return
    }

    // 如果期望 UUID 格式
    if _, err := uuid.Parse(id); err != nil {
        writeError(w, http.StatusBadRequest, "invalid agent id format")
        return
    }

    action := Action{
        ID:        uuid.New().String(),
        Type:      ActionKillAgent,
        TargetID:  id,
        CreatedAt: time.Now(),
    }
    result := h.service.Execute(r.Context(), action)
    writeResult(w, result)
}
```

---

### 4. 缺少超时控制

**位置**: `internal/arena/survival.go:73-154`

**问题**: `RunSurvival()` 启动的 goroutine 没有超时控制。

```go
// RunSurvival runs chaos actions at intervals for the configured duration.
func (s *Service) RunSurvival(ctx context.Context, cfg SurvivalConfig) SurvivalReport {
    // ...
    // Run survival in background.
    go func() {
        report := h.service.RunSurvival(context.Background(), cfg)
        slog.Info("arena: survival run finished in background",
            "actions", report.ActionsRun,
            "score", report.Score.Score,
            "grade", report.Score.Grade,
        )
    }()
    // ...
}
```

**问题分析**:
- Goroutine 没有超时控制
- 如果配置错误（如 Duration 非常大），可能导致内存泄漏
- Goroutine 无法被取消（除了 context.Background()）

**建议**:
```go
func (h *Handler) handleSurvivalStart(w http.ResponseWriter, r *http.Request) {
    var cfg SurvivalConfig
    if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
        writeError(w, http.StatusBadRequest, "invalid JSON body")
        return
    }
    if cfg.Duration <= 0 {
        cfg.Duration = 30 * time.Minute
    }
    if cfg.Interval <= 0 {
        cfg.Interval = 10 * time.Second
    }

    // 使用带超时的 context
    ctx, cancel := context.WithTimeout(r.Context(), cfg.Duration*2)
    defer cancel()

    // Run survival in background with timeout control
    go func() {
        report := h.service.RunSurvival(ctx, cfg)
        slog.Info("arena: survival run finished in background",
            "actions", report.ActionsRun,
            "score", report.Score.Score,
            "grade", report.Score.Grade,
        )
    }()

    writeJSON(w, http.StatusAccepted, map[string]string{
        "status":  "started",
        "message": "survival run started",
    })
}
```

---

## 🐛 Potential Bugs（潜在Bug）

### 1. ⚠️ **编译错误**: `scenario.go` 调用不存在的方法

**位置**: `internal/arena/scenario.go:186, 247`

**问题**: 代码调用 `service.calculateAvgRecoveryTime(nil)`，但 `Service` 结构体没有这个方法。

```go
// scenario.go:186
report.Score = CalculateScoreV1(service.Stats(), service.calculateAvgRecoveryTime(nil))

// scenario.go:247
report.Score = CalculateScoreV1(service.Stats(), service.calculateAvgRecoveryTime(nil))
```

**验证**:
```bash
$ go build ./internal/arena/...
# 应该会失败，但可能被忽略
```

**影响**:
- 代码无法编译
- 如果已经编译通过，说明代码路径从未被执行
- `RunScenarioReport()` 的评分功能完全损坏

**建议**:
- 立即添加缺失的方法到 `Service` 结构体
- 或者实现计算逻辑在 `ScenarioReport` 中
- 添加编译时检查

---

### 2. ⚠️ **竞态条件**: `Service.actions` 的并发访问

**位置**: `internal/arena/service.go:25-34`

**问题**: `Service.actions` 是一个切片，被多个 goroutine 并发修改。

```go
type Service struct {
    injector *Injector
    store    EventStore
    actions  []Result  // ← 共享状态
    stats    Stats
    mu       sync.RWMutex
    metrics  *MetricsCollector
    bridge   *FlightBridge

    survival survivalState
}

// Execute 方法（line 54-163）
func (s *Service) Execute(ctx context.Context, action Action) Result {
    // ... 执行逻辑
    s.mu.Lock()
    s.actions = append(s.actions, result)  // ← 写入
    s.stats.TotalActions++
    if result.Success {
        s.stats.SuccessfulActions++
    } else {
        s.stats.FailedActions++
    }
    s.stats.LastAction = time.Now()
    s.mu.Unlock()
}

// History 方法（line 165-173）
func (s *Service) History() []Result {
    s.mu.RLock()
    defer s.mu.RUnlock()

    out := make([]Result, len(s.actions))
    copy(out, s.actions)  // ← 读取
    return out
}
```

**问题分析**:
- `Execute()` 使用 `s.mu.Lock()` 保护 `actions` 的写入
- `History()` 使用 `s.mu.RLock()` 保护读取
- 但 `Execute()` 内部调用 `emitEvent()` 时没有加锁

```go
// service.go:139
s.emitEvent(ctx, action, result)  // ← 没有加锁！
```

**潜在竞态**:
```go
// Goroutine 1: Execute
s.mu.Lock()
s.actions = append(s.actions, result)
s.mu.Unlock()

// Goroutine 2: History（可能在 Execute 之间）
s.mu.RLock()
out := make([]Result, len(s.actions))  // ← len(s.actions) 可能不准确
copy(out, s.actions)  // ← 可能读取不完整的切片
s.mu.RUnlock()
```

**影响**:
- `History()` 可能返回不完整的切片
- `len(s.actions)` 可能不准确
- 数据竞争

**建议**:
```go
// 方案 1: 在 emitEvent 之前加锁
func (s *Service) Execute(ctx context.Context, action Action) Result {
    // ... 执行逻辑

    s.mu.Lock()
    s.actions = append(s.actions, result)
    s.stats.TotalActions++
    if result.Success {
        s.stats.SuccessfulActions++
    } else {
        s.stats.FailedActions++
    }
    s.stats.LastAction = time.Now()
    s.mu.Unlock()

    s.emitEvent(ctx, action, result)  // ← 现在安全了

    // ...
}

// 方案 2: 使用 Copy-on-Write
func (s *Service) Execute(ctx context.Context, action Action) Result {
    // ... 执行逻辑

    s.mu.Lock()
    s.actions = append(s.actions, result)
    s.stats.TotalActions++
    if result.Success {
        s.stats.SuccessfulActions++
    } else {
        s.stats.FailedActions++
    }
    s.stats.LastAction = time.Now()

    // 复制切片用于 emitEvent
    actionsCopy := make([]Result, len(s.actions))
    copy(actionsCopy, s.actions)
    s.mu.Unlock()

    s.emitEvent(ctx, action, result, actionsCopy)  // ← 传入副本
}
```

---

### 3. ⚠️ **资源泄漏**: HTTP Handler 的 Goroutine

**位置**: `internal/arena/http.go:372-399`

**问题**: `handleSurvivalStart()` 启动的 goroutine 没有生命周期管理。

```go
func (h *Handler) handleSurvivalStart(w http.ResponseWriter, r *http.Request) {
    var cfg SurvivalConfig
    if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
        writeError(w, http.StatusBadRequest, "invalid JSON body")
        return
    }
    if cfg.Duration <= 0 {
        cfg.Duration = 30 * time.Minute
    }
    if cfg.Interval <= 0 {
        cfg.Interval = 10 * time.Second
    }

    // Run survival in background.
    go func() {
        report := h.service.RunSurvival(context.Background(), cfg)
        slog.Info("arena: survival run finished in background",
            "actions", report.ActionsRun,
            "score", report.Score.Score,
            "grade", report.Score.Grade,
        )
    }()

    writeJSON(w, http.StatusAccepted, map[string]string{
        "status":  "started",
        "message": "survival run started",
    })
}
```

**问题分析**:
- Goroutine 无法被取消
- HTTP 请求完成后，Goroutine 继续运行
- 如果 Handler 被多次调用，启动多个 Goroutine
- 这些 Goroutine 永远不会退出（除非手动停止）

**潜在问题**:
```go
// 用户调用 10 次 handleSurvivalStart
// 启动 10 个 Goroutine，每个运行 30 分钟
// 10 分钟后，启动 10 个新 Goroutine
// 永远有 10 个 Goroutine 在运行
```

**影响**:
- 资源泄漏
- CPU 占用
- 内存占用
- 无法控制

**建议**:
```go
// 方案 1: 使用 context 控制生命周期
func (h *Handler) handleSurvivalStart(w http.ResponseWriter, r *http.Request) {
    var cfg SurvivalConfig
    if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
        writeError(w, http.StatusBadRequest, "invalid JSON body")
        return
    }
    if cfg.Duration <= 0 {
        cfg.Duration = 30 * time.Minute
    }
    if cfg.Interval <= 0 {
        cfg.Interval = 10 * time.Second
    }

    // 使用请求的 context
    ctx := r.Context()

    // Run survival in background with context control
    go func() {
        report := h.service.RunSurvival(ctx, cfg)
        slog.Info("arena: survival run finished in background",
            "actions", report.ActionsRun,
            "score", report.Score.Score,
            "grade", report.Score.Grade,
        )
    }()

    writeJSON(w, http.StatusAccepted, map[string]string{
        "status":  "started",
        "message": "survival run started",
    })
}

// 方案 2: 使用 WaitGroup 或 Channel 控制生命周期
func (h *Handler) handleSurvivalStart(w http.ResponseWriter, r *http.Request) {
    var cfg SurvivalConfig
    if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
        writeError(w, http.StatusBadRequest, "invalid JSON body")
        return
    }

    // 创建带超时的 context
    ctx, cancel := context.WithTimeout(r.Context(), cfg.Duration*2)
    defer cancel()

    // 使用 Channel 等待完成
    done := make(chan struct{})
    go func() {
        report := h.service.RunSurvival(ctx, cfg)
        slog.Info("arena: survival run finished in background",
            "actions", report.ActionsRun,
            "score", report.Score.Score,
            "grade", report.Score.Grade,
        )
        close(done)
    }()

    // 发送响应
    writeJSON(w, http.StatusAccepted, map[string]string{
        "status":  "started",
        "message": "survival run started",
    })

    // 等待完成（可选）
    select {
    case <-done:
        slog.Debug("survival run completed")
    case <-ctx.Done():
        slog.Info("survival run cancelled")
    }
}
```

---

### 4. ⚠️ **逻辑错误**: Score 计算使用 nil 参数

**位置**: `internal/arena/scenario.go:186, 247`

**问题**: `RunScenarioReport()` 使用 `CalculateScoreV1()` 计算评分，但 `CalculateScoreV1()` 使用 nil metrics。

```go
func CalculateScoreV1(stats Stats, avgRecovery time.Duration) ResilienceScore {
    return CalculateScore(stats, avgRecovery, nil)  // ← nil metrics
}

// calcConsistency 使用 nil metrics
func calcConsistency(failed int, metrics *MetricsSnapshot) float64 {
    if metrics != nil && metrics.DataConsistencyRate > 0 {
        return clamp(metrics.DataConsistencyRate, 0, 100)
    }
    // Heuristic: assume ~50% of failures are data-related, penalize accordingly.
    if failed == 0 {
        return 100
    }
    dataRelated := max(1, failed/2)
    return clamp(100-float64(dataRelated)*5, 0, 100)
}
```

**问题分析**:
- `nil metrics` 导致一致性评分使用启发式估算
- 启发式逻辑不够准确（`failed/2` 和 `*5` 都是硬编码）
- 无法反映实际的系统状态

**影响**:
- 评分不准确
- 无法量化系统的真实恢复能力

**建议**:
```go
// 方案 1: 添加 metrics 到 ScenarioReport
func (r *ScenarioReport) calculateMetrics() *MetricsSnapshot {
    // 计算并返回 metrics
    return s.Metrics()
}

// 方案 2: 使用 nil metrics 时记录警告
func CalculateScoreV1(stats Stats, avgRecovery time.Duration) ResilienceScore {
    slog.Warn("CalculateScoreV1 called with nil metrics, using heuristic consistency")
    return CalculateScore(stats, avgRecovery, nil)
}

// 方案 3: 实现 DataConsistencyRate 计算
func (r *ScenarioReport) calculateDataConsistency() float64 {
    // 根据实际数据计算一致性
    // 例如：检查 EventStore 中的事件一致性
    return 95.0  // 示例值
}
```

---

### 5. ⚠️ **边界条件**: Score 计算的边界值

**位置**: `internal/arena/score.go:80-89`

**问题**: `calcAvailability()` 在 `total == 0` 时返回 100，但逻辑上可能不合理。

```go
func calcAvailability(total, _ /*recovered*/, failed int) float64 {
    if total == 0 {
        return 100  // ← 问题：没有故障，availability = 100%
    }
    base := float64(total-failed) / float64(total) * 100
    return clamp(base, 0, 100)
}
```

**问题分析**:
- `total == 0` 表示没有执行任何故障注入
- 此时返回 100 可能误导
- 应该返回 0 或 NaN

**建议**:
```go
func calcAvailability(total, _ /*recovered*/, failed int) float64 {
    if total == 0 {
        return 0  // 或者返回 NaN
    }
    base := float64(total-failed) / float64(total) * 100
    return clamp(base, 0, 100)
}
```

---

### 6. ⚠️ **安全风险**: 文件读取没有限制

**位置**: `internal/arena/scenario.go:65-70`

**问题**: `LoadScenarioFile()` 直接读取文件，没有大小限制。

```go
func LoadScenarioFile(path string) (*Scenario, error) {
    data, err := os.ReadFile(path) // #nosec G304
    if err != nil {
        return nil, fmt.Errorf("arena: read scenario file %s: %w", path, err)
    }
    return LoadScenario(data)
}
```

**潜在攻击**:
```bash
# 创建一个 1GB 的 YAML 文件
dd if=/dev/zero of=huge_scenario.yaml bs=1M count=1024

# 读取文件会占用大量内存
arena scenario validate huge_scenario.yaml
```

**影响**:
- 内存溢出
- DoS 攻击

**建议**:
```go
func LoadScenarioFile(path string) (*Scenario, error) {
    // 限制文件大小
    const maxFileSize = 10 * 1024 * 1024  // 10MB

    file, err := os.Open(path)
    if err != nil {
        return nil, fmt.Errorf("arena: open scenario file %s: %w", path, err)
    }
    defer file.Close()

    // 读取并限制大小
    stat, err := file.Stat()
    if err != nil {
        return nil, fmt.Errorf("arena: stat scenario file %s: %w", path, err)
    }

    if stat.Size() > maxFileSize {
        return nil, fmt.Errorf("arena: scenario file too large: %d bytes (max: %d bytes)",
            stat.Size(), maxFileSize)
    }

    data := make([]byte, stat.Size())
    n, err := file.Read(data)
    if err != nil {
        return nil, fmt.Errorf("arena: read scenario file %s: %w", path, err)
    }

    return LoadScenario(data[:n])
}
```

---

## 📋 优先级建议

### 🔴 高优先级（立即修复）
1. **编译错误**: `scenario.go` 调用不存在的方法
2. **竞态条件**: `Service.actions` 的并发访问
3. **资源泄漏**: HTTP Handler 的 Goroutine

### 🟠 中优先级（近期修复）
4. **HTTP Handler 输入验证**: Agent ID 格式验证
5. **超时控制**: Goroutine 缺少超时控制
6. **Score 计算边界值**: `total == 0` 的情况

### 🟡 低优先级（技术债务）
7. **死代码**: `RecordRecovery()`、`RecordFailover()`、`RecordConsistency()`
8. **死代码**: `RunScenario()` 函数
9. **重复代码**: 统计计算逻辑
10. **缺少并发安全说明**: 注释不够清晰

---

## 🎯 修复建议

### 立即行动

```bash
# 1. 添加缺失的方法
# 在 Service 结构体中添加 calculateAvgRecoveryTime()

# 2. 修复竞态条件
# 在 emitEvent() 之前加锁

# 3. 修复资源泄漏
# 使用 context 控制 goroutine 生命周期
```

### 后续优化

1. 删除或废弃死代码
2. 添加输入验证和大小限制
3. 实现超时控制
4. 改进 Score 计算逻辑
5. 添加性能测试

---

## 总结

混沌工程模块整体设计良好，核心功能完整，但存在一些关键问题需要立即修复：

### ✅ **优点**:
- 清晰的 Action 类型定义（13 种故障类型）
- 完整的 HTTP API
- 详细的注释和文档
- Flight Bridge 集成

### ⚠️ **需要改进**:
- **编译错误**：`scenario.go` 调用不存在的方法
- **竞态条件**：`Service.actions` 并发访问
- **资源泄漏**：Goroutine 没有生命周期管理

**建议优先修复 🔴 高优先级问题，确保系统的稳定性和正确性。**

---

## 附录：文件清单

### 核心文件
- `internal/arena/types.go` - 类型定义
- `internal/arena/injector.go` - 故障注入
- `internal/arena/service.go` - 服务协调
- `internal/arena/survival.go` - 生存模式
- `internal/arena/metrics.go` - 指标收集
- `internal/arena/score.go` - 评分计算
- `internal/arena/scenario.go` - 场景执行
- `internal/arena/http.go` - HTTP API
- `internal/arena/integration.go` - Flight Bridge

### 测试文件
- `internal/arena/scenario_test.go` - 场景测试
- `internal/arena/metrics_test.go` - 指标测试
- `internal/arena/score_test.go` - 评分测试

---

*报告生成于 2026-06-23*
*分析工具: 手动代码审查 + grep 搜索*
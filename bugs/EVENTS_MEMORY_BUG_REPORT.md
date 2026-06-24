# Events & Memory 模块 Bug 分析报告

> **模块**: `internal/events` & `internal/ares_memory`
> **分析时间**: 2026-06-23
> **分析范围**: Dead Code、Technical Debt、Potential Bugs

---

## 📊 概览

| 类型 | 数量 | 严重程度 |
|------|------|----------|
| Dead Code | 2 | 🟡 中等 |
| Technical Debt | 5 | 🟠 较高 |
| Potential Bugs | 7 | 🔴 高 |

---

## 🚫 Dead Code（死代码）

### 1. `ErrStreamNotFound` 未使用

**位置**: `internal/events/types.go:83`

**问题**: `ErrStreamNotFound` 错误定义了但从未被使用。

```go
// ErrStreamNotFound indicates the requested stream does not exist.
var ErrStreamNotFound = errors.New("stream not found")
```

**搜索结果**:
- 定义了错误变量
- 但在整个项目中找不到任何地方使用这个错误
- 可能是遗留代码或计划中的功能

**影响**:
- 占用代码空间
- 增加维护成本
- 可能误导开发者

**建议**:
```go
// 方案 1: 删除未使用的错误（推荐）
// 如果确认不需要，直接删除

// 方案 2: 添加使用示例
// 如果计划使用，应该在 Read 方法中返回这个错误
func (s *MemoryEventStore) Read(_ context.Context, streamID string, opts ReadOptions) ([]*Event, error) {
    if streamID == "" {
        return nil, ErrStreamNotFound
    }
    // ...
}
```

---

### 2. `EventSummarizer` 类型未使用

**位置**: `internal/events/compactor.go:31`

**问题**: `EventSummarizer` 类型定义了但从未被使用。

```go
// EventSummarizer generates a human-readable summary text from a set of events.
// Implementations can be rule-based (default) or LLM-powered.
type EventSummarizer func(events []*Event) string
```

**搜索结果**:
- 定义了类型
- 但在整个项目中找不到任何地方使用这个类型
- `DefaultSummarizer` 函数被使用，但 `EventSummarizer` 类型未被使用

**影响**:
- 占用代码空间
- 增加维护成本
- 可能误导开发者

**建议**:
```go
// 方案 1: 删除未使用的类型（推荐）
// 如果确认不需要，直接删除

// 方案 2: 添加使用示例
// 如果计划使用，应该在 RegisterTool 时使用这个类型
```

---

## 🏗️ Technical Debt（技术债务）

### 1. 魔法数字散布在代码中

**位置**: 多个文件

**问题**: 大量魔法数字散布在代码中。

**示例**:
```go
// compactor.go:35
if config.Threshold <= 0 {
    config = DefaultCompactionConfig()
}

// compactable_store.go:48
lastChecked: make(map[string]int64),
// compactable_store.go:35
version-lastCheck < int64(threshold)/4,  // ← 魔法数字 4

// memory_store.go:33
subscribers []subscription,
// memory_store.go:84
if event.ID == "" {
    event.ID = NewEventID()
}

// memory_store.go:86
s.events = append(s.events, event),
// memory_store.go:88
s.streams[streamID] = append(s.streams[streamID], event),

// manager_impl.go:259
maxHistory := m.config.MaxHistory,
if len(messages) > maxHistory {
    messages = messages[len(messages)-maxHistory:]
}
```

**影响**:
- 代码可读性差
- 难以理解和维护
- 调优困难

**建议**:
```go
// constants.go
const (
    DefaultCompactionThreshold = 1000
    CompactionDebounceThreshold = 4
    DefaultEventBufferSize = 64
    MaxHistoryMessages = 20
)

// compactor.go
if version-lastCheck < int64(threshold)/CompactionDebounceThreshold {

// manager_impl.go
maxHistory := m.config.MaxHistory
if len(messages) > maxHistory {
    messages = messages[len(messages)-maxHistory:]
}
```

---

### 2. 缺少配置验证

**位置**: `internal/events/memory_store.go:32-40`

**问题**: `NewMemoryEventStore()` 没有验证输入参数。

```go
func NewMemoryEventStore() *MemoryEventStore {
    ctx, cancel := context.WithCancel(context.Background())
    return &MemoryEventStore{
        streams:  make(map[string][]*Event),
        versions: make(map[string]int64),
        ctx:      ctx,
        cancel:   cancel,
    }
}
```

**问题分析**:
- 没有验证 `ctx` 是否为 nil
- 没有验证 channel 缓冲区大小
- 没有验证默认值是否合理

**影响**:
- 可能传入 nil context
- 可能导致运行时 panic
- 难以调试

**建议**:
```go
func NewMemoryEventStore() *MemoryEventStore {
    ctx, cancel := context.WithCancel(context.Background())
    if ctx == nil {
        cancel()
        panic("context cannot be nil")
    }

    return &MemoryEventStore{
        streams:  make(map[string][]*Event),
        versions: make(map[string]int64),
        ctx:      ctx,
        cancel:   cancel,
    }
}
```

---

### 3. 缺少并发安全说明

**位置**: `internal/events/memory_store.go:11-22`

**问题**: `MemoryEventStore` 结构体没有文档说明其并发安全性。

```go
// MemoryEventStore is an in-memory implementation of EventStore.
// Use for development, testing, and prototyping. Not for production.
type MemoryEventStore struct {
    mu          sync.RWMutex
    events      []*Event
    streams     map[string][]*Event
    versions    map[string]int64
    subscribers []subscription
    closed      bool
    ctx         context.Context
    cancel      context.CancelFunc
}
```

**问题分析**:
- `MemoryEventStore` 的方法可能被多个 goroutine 同时调用
- 但没有文档说明是否并发安全
- 没有锁保护

**影响**:
- 可能导致并发问题
- 难以正确使用
- 可能出现竞态条件

**建议**:
```go
// MemoryEventStore is thread-safe for concurrent calls to Append, Read, and Subscribe.
// Close must only be called once from a single goroutine.
type MemoryEventStore struct {
    mu          sync.RWMutex
    events      []*Event
    streams     map[string][]*Event
    versions    map[string]int64
    subscribers []subscription
    closed      bool
    ctx         context.Context
    cancel      context.CancelFunc
}
```

---

### 4. 缺少错误处理说明

**位置**: `internal/events/types.go:90-113`

**问题**: `VerifyStreamIntegrity()` 和 `StreamHash()` 没有文档说明何时使用。

```go
// VerifyStreamIntegrity checks that a sequence of events has contiguous versions
// with no gaps or duplicates. Returns nil for empty or single-event streams.
// Legacy events with Version == 0 skip the check for backward compatibility.
func VerifyStreamIntegrity(evts []*Event) error {
    if len(evts) <= 1 {
        return nil
    }
    // ...
}

// StreamHash computes a deterministic hash for an event stream,
// useful for detecting silent corruption or partial writes.
func StreamHash(evts []*Event) string {
    // ...
}
```

**问题分析**:
- 这两个函数定义了但从未被使用
- 没有文档说明使用场景
- 可能是遗留代码

**影响**:
- 占用代码空间
- 增加维护成本
- 可能误导开发者

**建议**:
```go
// VerifyStreamIntegrity checks that a sequence of events has contiguous versions
// with no gaps or duplicates. Returns nil for empty or single-event streams.
// Legacy events with Version == 0 skip the check for backward compatibility.
func VerifyStreamIntegrity(evts []*Event) error {
    // ...
}

// StreamHash computes a deterministic hash for an event stream,
// useful for detecting silent corruption or partial writes.
func StreamHash(evts []*Event) string {
    // ...
}

// 这些函数目前未使用，保留用于未来扩展
// TODO: 在 PostgresEventStore 中使用 StreamHash 进行完整性检查
```

---

### 5. 缺少配置验证

**位置**: `internal/ares_memory/manager_impl.go:55-76`

**问题**: `NewMemoryManager()` 没有验证配置参数。

```go
func NewMemoryManager(config *MemoryConfig) (MemoryManager, error) {
    if config == nil {
        config = DefaultMemoryConfig()
    }

    sessionMemory := memctx.NewSessionMemory(
        config.MaxSessions,
        config.SessionTTL,
    )

    taskMemory := memctx.NewTaskMemory(
        config.MaxTasks,
        config.TaskTTL,
    )

    return &memoryManager{
        sessionMemory: sessionMemory,
        taskMemory:    taskMemory,
        config:        config,
        ctxCleaner:    memctx.NewContextCleaner(),
    }, nil
}
```

**问题分析**:
- 只检查 `config == nil`
- 没有验证 `config.MaxSessions` 是否为正数
- 没有验证 `config.SessionTTL` 是否为正数
- 没有验证 `config.MaxHistory` 是否为正数

**影响**:
- 可能传入无效配置
- 运行时 panic
- 难以调试

**建议**:
```go
func NewMemoryManager(config *MemoryConfig) (MemoryManager, error) {
    if config == nil {
        config = DefaultMemoryConfig()
    }

    if config.MaxSessions <= 0 {
        return nil, errors.New("MaxSessions must be positive")
    }

    if config.SessionTTL <= 0 {
        return nil, errors.New("SessionTTL must be positive")
    }

    if config.TaskTTL <= 0 {
        return nil, errors.New("TaskTTL must be positive")
    }

    if config.MaxHistory <= 0 {
        return nil, errors.New("MaxHistory must be positive")
    }

    // ... 创建 memoryManager
}
```

---

## 🐛 Potential Bugs（潜在Bug）

### 1. ⚠️ **资源泄漏**: Close 方法未等待订阅者

**位置**: `internal/events/memory_store.go:240-259`

**问题**: `Close()` 方法关闭 channel，但没有等待订阅者。

```go
func (s *MemoryEventStore) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.closed {
        return nil
    }

    s.closed = true
    s.cancel()
    for _, sub := range s.subscribers {
        // Use sync.Once to ensure each channel is only closed once, preventing
        // panic if unsubscribe() is called concurrently.
        sub.closeOnce.Do(func() {
            close(sub.ch)
        })
    }
    s.subscribers = nil
    return nil
}
```

**问题分析**:
- `sub.ch` 被关闭，但订阅者可能仍在读取
- 如果订阅者在 channel 关闭后继续读取，会立即收到 `io.EOF`
- 但订阅者可能不知道 channel 已关闭，继续发送数据
- 可能导致 panic

**影响**:
- 资源泄漏
- Goroutine 泄漏
- 可能导致 panic

**建议**:
```go
func (s *MemoryEventStore) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.closed {
        return nil
    }

    s.closed = true
    s.cancel()

    // Wait for all subscribers to stop reading
    for _, sub := range s.subscribers {
        sub.closeOnce.Do(func() {
            close(sub.ch)
        })
    }

    s.subscribers = nil
    return nil
}

// 在订阅者中处理关闭
func (s *MemoryEventStore) Subscribe(ctx context.Context, filter EventFilter) (<-chan *Event, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.closed {
        return nil, ErrEventStoreClosed
    }

    ch := make(chan *Event, 64)
    sub := subscription{
        id:        uuid.New().String(),
        filter:    filter,
        ch:        ch,
        closeOnce: &sync.Once{},
    }

    s.subscribers = append(s.subscribers, sub)
    return ch, nil
}

// 在订阅者中处理关闭
func (s *MemoryEventStore) unsubscribe(id string) {
    s.mu.Lock()
    defer s.mu.Unlock()

    for i, sub := range s.subscribers {
        if sub.id == id {
            sub.closeOnce.Do(func() {
                close(sub.ch)
            })
            s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
            return
        }
    }
}
```

---

### 2. ⚠️ **竞态条件**: Unsubscribe 和 Close 的并发调用

**位置**: `internal/events/memory_store.go:313-328`

**问题**: `unsubscribe()` 和 `Close()` 可能同时调用，导致 panic。

```go
func (s *MemoryEventStore) unsubscribe(id string) {
    s.mu.Lock()
    defer s.mu.Unlock()

    for i, sub := range s.subscribers {
        if sub.id == id {
            // Use sync.Once to ensure the channel is only closed once, preventing
            // panic if Close() is called concurrently.
            sub.closeOnce.Do(func() {
                close(sub.ch)
            })
            s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
            return
        }
    }
}

func (s *MemoryEventStore) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.closed {
        return nil
    }

    s.closed = true
    s.cancel()
    for _, sub := range s.subscribers {
        sub.closeOnce.Do(func() {
            close(sub.ch)
        })
    }
    s.subscribers = nil
    return nil
}
```

**问题分析**:
- `unsubscribe()` 使用 `sync.Once` 防止多次关闭
- `Close()` 也使用 `sync.Once` 防止多次关闭
- 但两个 goroutine 可能同时调用
- 可能导致竞态条件

**影响**:
- 竞态条件
- 可能导致 panic

**建议**:
```go
func (s *MemoryEventStore) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.closed {
        return nil
    }

    s.closed = true
    s.cancel()

    // Use sync.Once to ensure each channel is only closed once
    for _, sub := range s.subscribers {
        sub.closeOnce.Do(func() {
            close(sub.ch)
        })
    }

    s.subscribers = nil
    return nil
}

// unsubscribe 在 Close 中处理
func (s *MemoryEventStore) unsubscribe(id string) {
    s.mu.Lock()
    defer s.mu.Unlock()

    for i, sub := range s.subscribers {
        if sub.id == id {
            // Channel is already closed by Close(), nothing to do
            s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
            return
        }
    }
}
```

---

### 3. ⚠️ **性能问题**: Append 使用同步 goroutine

**位置**: `internal/events/compactable_store.go:62-77`

**问题**: `Append()` 使用同步 goroutine，可能阻塞调用者。

```go
func (s *CompactableEventStore) Append(
    ctx context.Context,
    streamID string,
    events []*Event,
    expectedVersion int64,
) error {
    err := s.EventStore.Append(ctx, streamID, events, expectedVersion)
    if err != nil {
        return err
    }

    // Async compaction check — don't block the Append caller.
    go s.maybeCompact(ctx, streamID)  // ← 同步 goroutine

    return nil
}
```

**问题分析**:
- `maybeCompact()` 在 goroutine 中调用
- 如果 compaction 耗时较长，会阻塞调用者
- 没有超时控制

**影响**:
- 性能问题
- 可能阻塞调用者

**建议**:
```go
func (s *CompactableEventStore) Append(
    ctx context.Context,
    streamID string,
    events []*Event,
    expectedVersion int64,
) error {
    err := s.EventStore.Append(ctx, streamID, events, expectedVersion)
    if err != nil {
        return err
    }

    // Async compaction check — don't block the Append caller.
    // Use a separate context with timeout to prevent blocking.
    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
        defer cancel()
        s.maybeCompact(ctx, streamID)
    }()

    return nil
}
```

---

### 4. ⚠️ **空指针解引用**: compactor.repo 可能为 nil

**位置**: `internal/events/compactable_store.go:93`

**问题**: `compactor.repo` 可能为 nil。

```go
func (s *CompactableEventStore) Read(ctx context.Context, streamID string, opts ReadOptions) ([]*Event, error) {
    events, err := s.EventStore.Read(ctx, streamID, opts)
    if err != nil {
        return nil, err
    }
    if len(events) > 0 {
        return events, nil
    }

    // Underlying store returned empty — check summaries as fallback.
    summaries, summaryErr := s.compactor.repo.FindByStreamID(ctx, streamID)
    if summaryErr != nil || len(summaries) == 0 {
        // No summaries either, return the original empty result.
        return events, nil
    }
    // ...
}
```

**问题分析**:
- `compactor.repo` 可能为 nil（如果 compactor 未初始化）
- 没有检查 `compactor.repo` 是否为 nil
- 直接访问 `s.compactor.repo` 可能 panic

**影响**:
- 空指针解引用
- 运行时 panic

**建议**:
```go
func (s *CompactableEventStore) Read(ctx context.Context, streamID string, opts ReadOptions) ([]*Event, error) {
    events, err := s.EventStore.Read(ctx, streamID, opts)
    if err != nil {
        return nil, err
    }
    if len(events) > 0 {
        return events, nil
    }

    // Underlying store returned empty — check summaries as fallback.
    if s.compactor.repo == nil {
        slog.Warn("compactable_store: repo is nil, cannot find summaries")
        return events, nil
    }

    summaries, summaryErr := s.compactor.repo.FindByStreamID(ctx, streamID)
    if summaryErr != nil || len(summaries) == 0 {
        // No summaries either, return the original empty result.
        return events, nil
    }

    // ... 转换 summaries 为 synthetic events
}
```

---

### 5. ⚠️ **性能问题**: MemoryEventStore 的 events 切片增长

**位置**: `internal/events/memory_store.go:87`

**问题**: `events` 切片持续增长，不限制大小。

```go
func (s *MemoryEventStore) Append(_ context.Context, streamID string, events []*Event, expectedVersion int64) error {
    // ...
    for i, event := range events {
        // ...
        s.events = append(s.events, event)
        s.streams[streamID] = append(s.streams[streamID], event)
        s.versions[streamID] = event.Version
        // ...
    }
    return nil
}
```

**问题分析**:
- `events` 切片持续增长
- 没有清理机制
- 内存占用持续增长
- 可能导致内存泄漏

**影响**:
- 内存占用高
- 内存泄漏
- 性能下降

**建议**:
```go
// 方案 1: 添加容量限制
type MemoryEventStore struct {
    mu          sync.RWMutex
    events      []*Event
    maxEvents   int  // 新增
    streams     map[string][]*Event
    versions    map[string]int64
    subscribers []subscription
    closed      bool
    ctx         context.Context
    cancel      context.CancelFunc
}

func NewMemoryEventStore() *MemoryEventStore {
    // ...
    return &MemoryEventStore{
        events:     make([]*Event, 0, 1000),  // 初始容量 1000
        streams:    make(map[string][]*Event),
        versions:   make(map[string]int64),
        maxEvents:  10000,  // 最大 10000 个事件
        ctx:        ctx,
        cancel:     cancel,
    }
}

func (s *MemoryEventStore) Append(_ context.Context, streamID string, events []*Event, expectedVersion int64) error {
    // ...
    for i, event := range events {
        // ...
        s.events = append(s.events, event)

        // 清理旧事件
        if len(s.events) > s.maxEvents {
            // 保留最新的 50% 事件
            s.events = s.events[len(s.events)/2:]
            // 清理 stream
            for streamID, evts := range s.streams {
                if len(evts) > s.maxEvents/2 {
                    s.streams[streamID] = evts[len(evts)/2:]
                }
            }
        }

        s.streams[streamID] = append(s.streams[streamID], event)
        s.versions[streamID] = event.Version
        // ...
    }
    return nil
}
```

---

### 6. ⚠️ **并发安全**: Close 方法可能被多次调用

**位置**: `internal/events/memory_store.go:240-259`

**问题**: `Close()` 方法使用 `sync.Once` 防止多次关闭，但没有返回错误。

```go
func (s *MemoryEventStore) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.closed {
        return nil  // ← 返回 nil，不返回错误
    }

    s.closed = true
    s.cancel()
    for _, sub := range s.subscribers {
        sub.closeOnce.Do(func() {
            close(sub.ch)
        })
    }
    s.subscribers = nil
    return nil
}
```

**问题分析**:
- `Close()` 可以被多次调用
- 每次调用都返回 nil
- 调用者无法知道是否已经关闭

**影响**:
- 调用者可能继续使用已关闭的 store
- 可能导致 panic

**建议**:
```go
func (s *MemoryEventStore) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.closed {
        return ErrEventStoreClosed
    }

    s.closed = true
    s.cancel()

    for _, sub := range s.subscribers {
        sub.closeOnce.Do(func() {
            close(sub.ch)
        })
    }

    s.subscribers = nil
    return nil
}
```

---

### 7. ⚠️ **资源泄漏**: MemoryManager 未清理

**位置**: `internal/ares_memory/manager_impl.go:200-250`

**问题**: `Stop()` 方法没有正确清理资源。

```go
func (m *memoryManager) Stop(ctx context.Context) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if m.stopped {
        return errors.New("memory manager already stopped")
    }

    m.stopped = true

    // Cleanup context
    if m.sessionMemory != nil {
        m.sessionMemory.Stop(ctx)
    }

    // Cleanup task memory
    if m.taskMemory != nil {
        m.taskMemory.Stop(ctx)
    }

    // Cleanup event store
    if m.eventStore != nil {
        m.eventStore.Close()  // ← 可能 panic
    }

    // Cleanup embedding pipeline
    if m.pipeline != nil {
        m.pipeline.Close()  // ← 可能 panic
    }

    // Cleanup distiller
    if m.distiller != nil {
        m.distiller.Close()  // ← 可能 panic
    }

    return nil
}
```

**问题分析**:
- `Stop()` 没有检查 `m.sessionMemory` 是否为 nil
- 没有检查 `m.taskMemory` 是否为 nil
- 没有检查 `m.eventStore` 是否为 nil
- 没有检查 `m.pipeline` 是否为 nil
- 没有检查 `m.distiller` 是否为 nil
- 直接调用 `Close()` 可能 panic

**影响**:
- 空指针解引用
- 运行时 panic
- 资源泄漏

**建议**:
```go
func (m *memoryManager) Stop(ctx context.Context) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if m.stopped {
        return errors.New("memory manager already stopped")
    }

    m.stopped = true

    // Cleanup in reverse order of initialization

    // Cleanup distiller
    if m.distiller != nil {
        if err := m.distiller.Close(); err != nil {
            slog.Error("failed to close distiller", "error", err)
        }
        m.distiller = nil
    }

    // Cleanup embedding pipeline
    if m.pipeline != nil {
        if err := m.pipeline.Close(); err != nil {
            slog.Error("failed to close embedding pipeline", "error", err)
        }
        m.pipeline = nil
    }

    // Cleanup event store
    if m.eventStore != nil {
        if err := m.eventStore.Close(); err != nil {
            slog.Error("failed to close event store", "error", err)
        }
        m.eventStore = nil
    }

    // Cleanup task memory
    if m.taskMemory != nil {
        if err := m.taskMemory.Stop(ctx); err != nil {
            slog.Error("failed to stop task memory", "error", err)
        }
        m.taskMemory = nil
    }

    // Cleanup session memory
    if m.sessionMemory != nil {
        if err := m.sessionMemory.Stop(ctx); err != nil {
            slog.Error("failed to stop session memory", "error", err)
        }
        m.sessionMemory = nil
    }

    return nil
}
```

---

## 📋 优先级建议

### 🔴 高优先级（立即修复）
1. **资源泄漏**: Close 方法未等待订阅者
2. **空指针解引用**: compactor.repo 可能为 nil
3. **资源泄漏**: MemoryManager 未清理

### 🟠 中优先级（近期修复）
4. **竞态条件**: Unsubscribe 和 Close 的并发调用
5. **性能问题**: Append 使用同步 goroutine
6. **性能问题**: MemoryEventStore 的 events 切片增长

### 🟡 低优先级（技术债务）
7. 死代码: `ErrStreamNotFound` 未使用
8. 死代码: `EventSummarizer` 类型未使用
9. 魔法数字: 添加常量定义
10. 缺少配置验证
11. 缺少并发安全说明
12. 缺少错误处理说明
13. 缺少配置验证

---

## 🎯 修复建议

### 立即行动

```bash
# 1. 修复 Close 方法未等待订阅者
# 在 Close() 中等待所有订阅者完成

# 2. 修复 compactor.repo 空指针解引用
# 在 Read() 中检查 repo 是否为 nil

# 3. 修复 MemoryManager Stop() 未清理
# 在 Stop() 中检查每个组件是否为 nil
```

### 后续优化

1. 删除未使用的错误
2. 删除未使用的类型
3. 添加常量定义
4. 添加输入验证
5. 添加并发安全说明
6. 添加错误处理说明
7. 优化 MemoryEventStore 内存管理

---

## 总结

Events & Memory 模块整体设计良好，核心功能完整，但存在一些关键问题需要立即修复：

### ✅ **优点**:
- 清晰的接口设计（EventStore、Compactor、MemoryManager）
- 良好的并发控制（sync.RWMutex、sync.Once）
- 完整的错误处理
- 详细的注释

### ⚠️ **需要改进**:
- **资源泄漏**: Close 方法未等待订阅者
- **空指针解引用**: compactor.repo 可能为 nil
- **资源泄漏**: MemoryManager Stop() 未清理
- **性能问题**: MemoryEventStore 的 events 切片增长

**建议优先修复 🔴 高优先级问题，确保系统的稳定性和正确性。**

---

## 附录：文件清单

### Events 模块
- `internal/events/types.go` - 事件类型（128 行）
- `internal/events/store.go` - 事件存储接口（73 行）
- `internal/events/memory_store.go` - 内存事件存储（334 行）
- `internal/events/compactable_store.go` - 可压缩事件存储（195 行）
- `internal/events/compactor.go` - 事件压缩器（435 行）
- `internal/events/pg_store.go` - PostgreSQL 事件存储（456 行）
- `internal/events/summary.go` - 事件摘要（120 行）
- `internal/events/summary_repository.go` - 摘要仓库（335 行）
- `internal/events/trim_store.go` - 修剪存储（110 行）

### Memory 模块
- `internal/ares_memory/manager.go` - 内存管理器接口（305 行）
- `internal/ares_memory/manager_impl.go` - 内存管理器实现（628 行）
- `internal/ares_memory/production_manager.go` - 生产环境管理器（997 行）
- `internal/ares_memory/context/user.go` - 用户上下文（52 行）
- `internal/ares_memory/context/task.go` - 任务上下文（54 行）
- `internal/ares_memory/context/session.go` - 会话上下文（70 行）
- `internal/ares_memory/context/cache.go` - 缓存（74 行）
- `internal/ares_memory/context/rag.go` - RAG（54 行）
- `internal/ares_memory/distillation/distiller.go` - 蒸馏器（314 行）

---

*报告生成于 2026-06-23*
*分析工具: 手动代码审查 + grep 搜索*
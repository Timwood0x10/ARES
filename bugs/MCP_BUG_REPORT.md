# MCP 模块 Bug 分析报告

> **模块**: `internal/mcp` (MCP - Model Context Protocol)
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

### 1. `ErrDuplicateRegistration` 未使用

**位置**: `internal/mcp/server.go:87`

**问题**: `ErrDuplicateRegistration` 错误定义了但从未被使用。

```go
// ErrDuplicateRegistration indicates a tool/resource/prompt with the same name already exists.
var ErrDuplicateRegistration = fmt.Errorf("duplicate registration")
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
// 如果计划使用，应该在注册工具时返回这个错误
func (s *MCPServer) RegisterTool(name string, handler ToolHandler, schema json.RawMessage, description string) error {
    if _, exists := s.tools[name]; exists {
        return ErrDuplicateRegistration
    }
    // ...
}
```

---

### 2. `ErrEmptyName` 未使用

**位置**: `internal/mcp/server.go:90`

**问题**: `ErrEmptyName` 错误定义了但从未被使用。

```go
// ErrEmptyName indicates that a name parameter was empty.
var ErrEmptyName = fmt.Errorf("name must not be empty")
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
// 如果计划使用，应该在注册工具时验证名称
func (s *MCPServer) RegisterTool(name string, handler ToolHandler, schema json.RawMessage, description string) error {
    if name == "" {
        return ErrEmptyName
    }
    // ...
}
```

---

## 🏗️ Technical Debt（技术债务）

### 1. 魔法数字散布在代码中

**位置**: 多个文件

**问题**: 大量魔法数字散布在代码中。

**示例**:
```go
// transport_sse.go:52
if timeout == 0 {
    timeout = 30 * time.Second  // ← 魔法数字

// transport_sse.go:60
msgCh:   make(chan *JSONRPCMessage, 64),  // ← 魔法数字

// transport_stdio.go:82
t.stdout.Buffer(make([]byte, 0, 1024*1024), 1024*1024)  // ← 魔法数字

// manager.go:206
statuses := make([]MCPServerStatus, 0, len(m.clients))  // ← 魔法数字
```

**影响**:
- 代码可读性差
- 难以理解和维护
- 调优困难

**建议**:
```go
// constants.go
const (
    DefaultTimeoutSSE    = 30 * time.Second
    DefaultStreamBufferSize = 64
    DefaultBufferSize   = 1024 * 1024
    DefaultSliceCapacity = 16
)

// transport_sse.go
if timeout == 0 {
    timeout = DefaultTimeoutSSE
}

msgCh:   make(chan *JSONRPCMessage, DefaultStreamBufferSize)
```

---

### 2. 缺少输入验证

**位置**: `internal/mcp/manager.go:99-106`

**问题**: `ConnectServer()` 没有验证 server name 是否为空。

```go
func (m *MCPManager) ConnectServer(ctx context.Context, name string) error {
    m.mu.RLock()
    sc := m.findServerConfig(name)
    m.mu.RUnlock()

    if sc == nil {
        return fmt.Errorf("server %q not found in config", name)
    }

    // ...
}
```

**问题分析**:
- 没有验证 `name` 是否为空
- 如果 `name` 为空，`findServerConfig` 可能返回 nil
- 错误信息不够明确

**影响**:
- 错误信息不明确
- 难以调试

**建议**:
```go
func (m *MCPManager) ConnectServer(ctx context.Context, name string) error {
    if name == "" {
        return fmt.Errorf("server name cannot be empty")
    }

    m.mu.RLock()
    sc := m.findServerConfig(name)
    m.mu.RUnlock()

    if sc == nil {
        return fmt.Errorf("server %q not found in config", name)
    }

    // ...
}
```

---

### 3. 缺少配置验证

**位置**: `internal/mcp/manager.go:55-60`

**问题**: `NewMCPManager()` 没有验证配置参数。

```go
func NewMCPManager(config *MCPManagerConfig, registry *core.Registry) *MCPManager {
    return &MCPManager{
        clients:  make(map[string]*managedClient),
        registry: registry,
        config:   config,
    }
}
```

**问题分析**:
- 没有验证 `config` 是否为 nil
- 没有验证 `registry` 是否为 nil
- 没有验证 `config.Servers` 是否为空
- 可能导致后续运行时错误

**影响**:
- 可能传入 nil 参数
- 运行时 panic
- 难以调试

**建议**:
```go
func NewMCPManager(config *MCPManagerConfig, registry *core.Registry) (*MCPManager, error) {
    if config == nil {
        return nil, fmt.Errorf("config cannot be nil")
    }

    if registry == nil {
        return nil, fmt.Errorf("registry cannot be nil")
    }

    if len(config.Servers) == 0 {
        slog.Warn("mcp: no servers configured")
    }

    return &MCPManager{
        clients:  make(map[string]*managedClient),
        registry: registry,
        config:   config,
    }, nil
}
```

---

### 4. 缺少并发安全说明

**位置**: `internal/mcp/manager.go:38-43`

**问题**: `MCPManager` 结构体没有文档说明其并发安全性。

```go
type MCPManager struct {
    clients  map[string]*managedClient
    registry *core.Registry
    mu       sync.RWMutex
    config   *MCPManagerConfig
}
```

**问题分析**:
- `MCPManager` 的方法可能被多个 goroutine 同时调用
- 但没有文档说明是否并发安全
- 没有锁保护

**影响**:
- 可能导致并发问题
- 难以正确使用
- 可能出现竞态条件

**建议**:
```go
// MCPManager is thread-safe for concurrent calls to methods like ConnectServer,
// DisconnectServer, RefreshTools, and ListServers. The registry is not thread-safe
// and must be accessed externally with appropriate synchronization.
type MCPManager struct {
    clients  map[string]*managedClient
    registry *core.Registry
    mu       sync.RWMutex
    config   *MCPManagerConfig
}
```

---

## 🐛 Potential Bugs（潜在Bug）

### 1. ⚠️ **资源泄漏**: Stdio stderr 未关闭

**位置**: `internal/mcp/transport_stdio.go:95-108`

**问题**: stderr goroutine 可能导致资源泄漏。

```go
// Drain stderr in background to prevent blocking, logging output for diagnostics.
t.stderrWg.Add(1)
go func() {
    defer t.stderrWg.Done()
    scanner := bufio.NewScanner(t.stderr)
    for scanner.Scan() {
        // ...
    }
    if err := scanner.Err(); err != nil {
        slog.Error("stderr error", "err", err)
    }
}()
```

**问题分析**:
- stderr goroutine 永远运行（除非 `Start` 被调用 `Close`）
- 如果 `Close` 被多次调用，stderr goroutine 可能继续运行
- stderr 可能一直被读取，导致 goroutine 泄漏

**影响**:
- Goroutine 泄漏
- 资源泄漏
- 系统卡死

**建议**:
```go
// 方案 1: 使用 sync.Once 确保只启动一次
type StdioTransport struct {
    // ...
    startedOnce sync.Once
    stderrWg   sync.WaitGroup
}

func (t *StdioTransport) Start(ctx context.Context) error {
    t.mu.Lock()
    defer t.mu.Unlock()

    if t.started.Load() {
        return fmt.Errorf("transport already started")
    }

    // ... 创建 process

    t.started.Store(true)

    // Drain stderr in background
    t.startedOnce.Do(func() {
        t.stderrWg.Add(1)
        go func() {
            defer t.stderrWg.Done()
            scanner := bufio.NewScanner(t.stderr)
            for scanner.Scan() {
                // ...
            }
        }()
    })

    // ...
}

func (t *StdioTransport) Close() error {
    t.mu.Lock()
    defer t.mu.Unlock()

    // 停止读取 stderr
    t.stderrWg.Wait()

    // ... 关闭其他资源
}
```

---

### 2. ⚠️ **资源泄漏**: Stdio stdout 未关闭

**位置**: `internal/mcp/transport_stdio.go:70-91`

**问题**: stdoutPipe 可能未被正确关闭。

```go
stdoutPipe, err := t.cmd.StdoutPipe()
if err != nil {
    return fmt.Errorf("stdout pipe: %w", err)
}
t.stdoutPipe = stdoutPipe
t.stdout = bufio.NewScanner(stdoutPipe)

t.stderr, err = t.cmd.StderrPipe()
if err != nil {
    return fmt.Errorf("stderr pipe: %w", err)
}

if err := t.cmd.Start(); err != nil {
    return fmt.Errorf("start process: %w", err)
}

t.started.Store(true)
```

**问题分析**:
- `stdoutPipe` 和 `stderrPipe` 在 `Close()` 中没有关闭
- 如果 `Start` 失败，这些 pipe 可能未被关闭
- 可能导致资源泄漏

**影响**:
- 资源泄漏
- 文件描述符泄漏

**建议**:
```go
// 在 Start 方法中保存 pipe
type StdioTransport struct {
    // ...
    stdoutPipe io.ReadCloser
    stderrPipe io.ReadCloser
}

func (t *StdioTransport) Start(ctx context.Context) error {
    // ... 创建 pipe

    stdoutPipe, err := t.cmd.StdoutPipe()
    if err != nil {
        return fmt.Errorf("stdout pipe: %w", err)
    }
    t.stdoutPipe = stdoutPipe
    t.stdout = bufio.NewScanner(stdoutPipe)

    stderrPipe, err := t.cmd.StderrPipe()
    if err != nil {
        stdoutPipe.Close()  // 清理已创建的 pipe
        return fmt.Errorf("stderr pipe: %w", err)
    }
    t.stderrPipe = stderrPipe

    // ...
}

func (t *StdioTransport) Close() error {
    t.mu.Lock()
    defer t.mu.Unlock()

    if t.started.Load() {
        // 停止读取 stdout
        t.stdout = nil

        // 停止读取 stderr
        t.stderrWg.Wait()

        // 关闭 pipe
        if t.stdoutPipe != nil {
            t.stdoutPipe.Close()
        }
        if t.stderrPipe != nil {
            t.stderrPipe.Close()
        }
    }

    // ... 关闭 cmd
}
```

---

### 3. ⚠️ **竞态条件**: SSE transport channel 缓冲区溢出

**位置**: `internal/mcp/transport_sse.go:39-60`

**问题**: channel 缓冲区大小固定为 64，可能溢出。

```go
type SSETransport struct {
    config     SSEConfig
    httpClient *http.Client
    msgCh      chan *JSONRPCMessage
    cancel     context.CancelFunc
    eg         errgroup.Group
    mu         sync.Mutex
    started    bool
    postURL    string
    respBody   io.Closer
}

func NewSSETransport(config SSEConfig) *SSETransport {
    return &SSETransport{
        config: config,
        httpClient: &http.Client{
            Timeout: timeout,
        },
        msgCh:   make(chan *JSONRPCMessage, 64),  // ← 固定缓冲区
        postURL: config.URL,
    }
}
```

**问题分析**:
- channel 缓冲区大小固定为 64
- 如果接收速度 > 发送速度，channel 会阻塞
- 可能导致接收 goroutine 阻塞

**影响**:
- 性能问题
- 可能导致死锁

**建议**:
```go
// 方案 1: 使用无缓冲 channel + 多个接收 goroutine
// 方案 2: 动态调整缓冲区大小
// 方案 3: 限制接收速度

type SSETransport struct {
    msgCh chan *JSONRPCMessage
}

func NewSSETransport(config SSEConfig) *SSETransport {
    return &SSETransport{
        msgCh:   make(chan *JSONRPCMessage, 1),  // 无缓冲，强制同步
    }
}
```

---

### 4. ⚠️ **竞态条件**: SSE transport receiveLoop panic

**位置**: `internal/mcp/transport_sse.go:92-150`

**问题**: receiveLoop 可能 panic。

```go
func (t *SSETransport) receiveLoop(ctx context.Context) error {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.config.URL, nil)
    if err != nil {
        return fmt.Errorf("create sse request: %w", err)
    }

    req.Header.Set("Accept", "text/event-stream")
    req.Header.Set("Cache-Control", "no-cache")
    for k, v := range t.config.Headers {
        req.Header.Set(k, v)
    }

    resp, err := t.httpClient.Do(req)
    if err != nil {
        return fmt.Errorf("sse connect: %w", err)
    }

    if resp.StatusCode != http.StatusOK {
        respBody, _ := io.ReadAll(resp.Body)
        resp.Body.Close()
        return fmt.Errorf("sse connect: status %d", resp.StatusCode)
    }

    t.respBody = resp.Body
    scanner := bufio.NewScanner(resp.Body)
    for scanner.Scan() {
        line := scanner.Text()

        // ... 解析 line
    }

    if err := scanner.Err(); err != nil {
        return fmt.Errorf("sse read: %w", err)
    }

    return nil
}
```

**问题分析**:
- `scanner.Err()` 可能返回错误
- 但 `resp.Body.Close()` 在 scanner 循环之后
- 如果 scanner.Err() 失败，可能关闭 resp.Body 两次

**影响**:
- 资源泄漏
- Panic

**建议**:
```go
func (t *SSETransport) receiveLoop(ctx context.Context) error {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.config.URL, nil)
    if err != nil {
        return fmt.Errorf("create sse request: %w", err)
    }

    // ... 设置 headers

    resp, err := t.httpClient.Do(req)
    if err != nil {
        return fmt.Errorf("sse connect: %w", err)
    }

    if resp.StatusCode != http.StatusOK {
        respBody, _ := io.ReadAll(resp.Body)
        resp.Body.Close()
        return fmt.Errorf("sse connect: status %d", resp.StatusCode)
    }

    t.respBody = resp.Body
    scanner := bufio.NewScanner(resp.Body)

    defer func() {
        scannerErr := scanner.Err()
        resp.Body.Close()
        if scannerErr != nil {
            slog.Error("sse scan error", "err", scannerErr)
        }
    }()

    for scanner.Scan() {
        line := scanner.Text()

        // ... 解析 line
    }

    return nil
}
```

---

### 5. ⚠️ **空指针解引用**: client.mu 可能为 nil

**位置**: `internal/mcp/manager.go:246-251`

**问题**: `mc.client.mu` 可能为 nil。

```go
func (m *MCPManager) registerTools(mc *managedClient) ([]string, error) {
    tools := mc.client.ToolCount()
    if tools == 0 {
        return nil, nil
    }

    // Get all tool definitions.
    mc.client.mu.RLock()  // ← client.mu 可能为 nil
    defs := make([]*MCPToolDef, 0, len(mc.client.tools))
    for _, def := range mc.client.tools {
        defs = append(defs, def)
    }
    mc.client.mu.RUnlock()
    // ...
}
```

**问题分析**:
- `mc.client` 可能是 nil（如果 client 未初始化）
- 没有检查 `mc.client` 是否为 nil
- 直接访问 `mc.client.mu` 可能 panic

**影响**:
- 空指针解引用
- 运行时 panic

**建议**:
```go
func (m *MCPManager) registerTools(mc *managedClient) ([]string, error) {
    if mc == nil || mc.client == nil {
        return nil, fmt.Errorf("managed client or client is nil")
    }

    tools := mc.client.ToolCount()
    if tools == 0 {
        return nil, nil
    }

    // Get all tool definitions.
    mc.client.mu.RLock()
    defs := make([]*MCPToolDef, 0, len(mc.client.tools))
    for _, def := range mc.client.tools {
        defs = append(defs, def)
    }
    mc.client.mu.RUnlock()
    // ...
}
```

---

### 6. ⚠️ **性能问题**: Manager 遍历所有服务器

**位置**: `internal/mcp/manager.go:202-024`

**问题**: `ListServers()` 每次都遍历所有服务器。

```go
func (m *MCPManager) ListServers() []MCPServerStatus {
    m.mu.RLock()
    defer m.mu.RUnlock()

    statuses := make([]MCPServerStatus, 0, len(m.clients))
    for _, mc := range m.clients {
        status := MCPServerStatus{
            Name:      mc.config.Name,
            Connected: mc.client.IsConnected(),
            ToolCount: mc.client.ToolCount(),
            ConnAt:    mc.connAt,
        }
        // ...
    }

    return statuses
}
```

**问题分析**:
- 每次调用都遍历所有服务器
- 如果服务器很多，性能会下降
- 没有缓存机制

**影响**:
- 性能问题
- CPU 占用高

**建议**:
```go
// 方案 1: 添加缓存
type MCPManager struct {
    clients  map[string]*managedClient
    registry *core.Registry
    mu       sync.RWMutex
    config   *MCPManagerConfig
    statusCache  *MCPStatusCache  // 新增缓存
    statusCacheMu sync.RWMutex
}

// 方案 2: 限制服务器数量
// 在配置中限制最大服务器数量
```

---

## 📋 优先级建议

### 🔴 高优先级（立即修复）
1. **资源泄漏**: Stdio stderr 未关闭
2. **资源泄漏**: Stdio stdout 未关闭
3. **空指针解引用**: client.mu 可能为 nil

### 🟠 中优先级（近期修复）
4. **竞态条件**: SSE transport receiveLoop panic
5. **性能问题**: Manager 遍历所有服务器

### 🟡 低优先级（技术债务）
6. 死代码: `ErrDuplicateRegistration` 未使用
7. 死代码: `ErrEmptyName` 未使用
8. 魔法数字: 添加常量定义
9. 缺少输入验证: ConnectServer
10. 缺少配置验证: NewMCPManager
11. 缺少并发安全说明: MCPManager

---

## 🎯 修复建议

### 立即行动

```bash
# 1. 修复 Stdio transport 资源泄漏
# 在 Close() 中关闭 stdoutPipe 和 stderrPipe

# 2. 修复 client.mu 空指针解引用
# 在 registerTools 中检查 mc.client 是否为 nil

# 3. 修复 receiveLoop panic
# 使用 defer 确保 resp.Body.Close()
```

### 后续优化

1. 删除未使用的错误
2. 添加常量定义
3. 添加输入验证
4. 添加配置验证
5. 添加并发安全说明
6. 优化 ListServers 性能

---

## 总结

MCP 模块整体设计良好，核心功能完整，但存在一些关键问题需要立即修复：

### ✅ **优点**:
- 清晰的接口设计
- 支持多种传输协议（SSE、stdio）
- 完整的并发控制
- 良好的错误处理
- 详细的注释

### ⚠️ **需要改进**:
- **资源泄漏**: Stdio stderr/stdout 未关闭
- **空指针解引用**: client.mu 可能为 nil
- **竞态条件**: SSE transport receiveLoop panic
- **性能问题**: ListServers 遍历所有服务器

**建议优先修复 🔴 高优先级问题，确保系统的稳定性和正确性。**

---

## 附录：文件清单

### 核心文件
- `internal/mcp/client.go` - MCP 客户端（362 行）
- `internal/mcp/server.go` - MCP 服务器（781 行）
- `internal/mcp/manager.go` - MCP 管理器（292 行）
- `internal/mcp/transport_sse.go` - SSE 传输（277 行）
- `internal/mcp/transport_stdio.go` - Stdio 传输（241 行）
- `internal/mcp/transport_server.go` - HTTP Server 传输（523 行）
- `internal/mcp/jsonrpc.go` - JSON-RPC 协议（200 行）

### 测试文件
- `internal/mcp/client_test.go` - 客户端测试
- `internal/mcp/server_test.go` - 服务器测试
- `internal/mcp/manager_test.go` - 管理器测试
- `internal/mcp/transport_test.go` - 传输测试
- `internal/mcp/transport_sse_test.go` - SSE 测试
- `internal/mcp/transport_stdio_test.go` - Stdio 测试

---

*报告生成于 2026-06-23*
*分析工具: 手动代码审查 + grep 搜索*
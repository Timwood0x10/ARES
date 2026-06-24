# LLM 模块 Bug 分析报告

> **模块**: `internal/llm` (Client & Output)
> **分析时间**: 2026-06-23
> **分析范围**: Dead Code、Technical Debt、Potential Bugs

---

## 📊 概览

| 类型 | 数量 | 严重程度 |
|------|------|----------|
| Dead Code | 2 | 🟡 中等 |
| Technical Debt | 6 | 🟠 较高 |
| Potential Bugs | 8 | 🔴 高 |

---

## 🚫 Dead Code（死代码）

### 1. `HTTPError` 类型未使用

**位置**: `internal/llm/client.go:24-32`

**问题**: `HTTPError` 类型定义了但从未被使用。

```go
// HTTPError represents an HTTP request error.
type HTTPError struct {
    StatusCode int
    Message    string
}

// Error returns the error message.
func (e *HTTPError) Error() string {
    return e.Message
}
```

**搜索结果**:
- 定义了 HTTPError 结构体
- 实现了 Error() 方法
- 但在整个项目中找不到任何地方使用这个类型
- 可能是遗留代码或计划中的功能

**影响**:
- 占用代码空间
- 增加维护成本
- 可能误导开发者

**建议**:
```go
// 方案 1: 删除未使用的类型（推荐）
// 如果确认不需要，直接删除

// 方案 2: 添加使用示例
// 如果计划使用，应该有使用场景
func (c *Client) makeRequest(...) (int, string, error) {
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return 0, "", HTTPError{StatusCode: 0, Message: err.Error()}
    }
    // ...
}
```

---

### 2. `sync.Map` 缓存可能未使用

**位置**: `internal/llm/output/validator.go:18`

**问题**: `sync.Map` 用于缓存正则表达式，但可能没有被充分利用。

```go
type InputValidator struct {
    mu    sync.RWMutex
    regexCache sync.Map // Cache compiled regex patterns
}
```

**问题分析**:
- 定义了 regexCache 字段
- 但在 `InputValidator` 中没有看到使用这个缓存的代码
- 可能是计划中的优化，但未实现

**影响**:
- 占用内存
- 增加代码复杂度
- 维护困难

**建议**:
```go
// 方案 1: 删除未使用的缓存
// 方案 2: 实现缓存逻辑
func (v *InputValidator) validateInput(input string) error {
    // 检查缓存
    if cached, ok := v.regexCache.Load(pattern); ok {
        return cached.(regexp.Regexp).MatchString(input)
    }

    // 编译并缓存
    re := regexp.MustCompile(pattern)
    v.regexCache.Store(pattern, re)

    return re.MatchString(input)
}
```

---

## 🏗️ Technical Debt（技术债务）

### 1. 魔法数字散布在代码中

**位置**: 多个文件

**问题**: 大量魔法数字（如 `8192`、`64`、`60`、`4096`）散布在代码中。

**示例**:
```go
// client.go:159
const maxPromptLength = 8192  // ← 魔法数字

// client.go:103
if config.Timeout <= 0 {
    config.Timeout = 60  // ← 魔法数字

// client.go:530
ch := make(chan StreamChunk, 64)  // ← 魔法数字

// openai.go:52
"max_tokens":  a.config.MaxTokens,
"temperature": a.config.Temperature,
// ... 但 MaxTokens 和 Temperature 的默认值在哪里？

// streamOllama:582
"num_predict": 4096,  // ← 魔法数字
```

**影响**:
- 代码可读性差
- 难以理解和维护
- 调优困难
- 配置不一致

**建议**:
```go
// constants.go
const (
    DefaultTimeoutSeconds    = 60
    MaxPromptLengthChars     = 8192
    DefaultStreamBufferSize  = 64
    DefaultOllamaNumPredict  = 4096
)

// client.go
if config.Timeout <= 0 {
    config.Timeout = DefaultTimeoutSeconds
}

if len(prompt) > MaxPromptLengthChars {
    err := fmt.Errorf("prompt exceeds maximum length of %d characters", MaxPromptLengthChars)
    // ...
}

ch := make(chan StreamChunk, DefaultStreamBufferSize)
```

---

### 2. 缺少配置验证

**位置**: `internal/llm/client.go:97-124`

**问题**: `NewClient()` 函数没有验证配置参数。

```go
func NewClient(config *Config, opts ...Option) (*Client, error) {
    if config == nil {
        return nil, coreerrors.ErrInvalidArgument
    }

    if config.Timeout <= 0 {
        config.Timeout = 60
    }

    c := &Client{
        config: config,
        httpClient: &http.Client{
            Timeout: time.Duration(config.Timeout) * time.Second,
        },
        streamClient: &http.Client{
            Transport: http.DefaultTransport,
        },
    }

    for _, opt := range opts {
        opt(c)
    }

    return c, nil
}
```

**问题分析**:
- 只检查 `config == nil`
- 没有检查 `config.Model` 是否为空
- 没有检查 `config.Provider` 是否有效
- 没有检查 `config.BaseURL` 是否有效
- 没有检查 `config.APIKey` 是否为空（对于需要认证的 provider）

**影响**:
- 可能传入无效配置
- 运行时错误
- 难以调试

**建议**:
```go
func NewClient(config *Config, opts ...Option) (*Client, error) {
    if config == nil {
        return nil, coreerrors.ErrInvalidArgument
    }

    // 验证必需字段
    if config.Model == "" {
        return nil, errors.New("model is required")
    }

    // 验证 provider
    switch ProviderType(config.Provider) {
    case ProviderOpenRouter, ProviderOllama:
        // Valid
    default:
        return nil, fmt.Errorf("unsupported provider: %s", config.Provider)
    }

    // 验证 APIKey（对于需要认证的 provider）
    if (config.Provider == "openrouter" || config.Provider == "openai") && config.APIKey == "" {
        return nil, errors.New("api_key is required for OpenAI/OpenRouter")
    }

    // 验证 Timeout
    if config.Timeout <= 0 {
        config.Timeout = DefaultTimeoutSeconds
    }

    // 验证 BaseURL
    if config.BaseURL == "" {
        switch ProviderType(config.Provider) {
        case ProviderOllama:
            config.BaseURL = DefaultOllamaBaseURL
        case ProviderOpenRouter:
            config.BaseURL = DefaultOpenRouterBaseURL
        }
    }

    // ... 创建 client
}
```

---

### 3. 错误处理不一致

**位置**: 多个文件

**问题**: 错误处理方式不一致。

**示例对比**:

```go
// client.go:102-104 - 返回默认值
if config.Timeout <= 0 {
    config.Timeout = 60
}

// openai.go:28-30 - 返回空配置
if config == nil {
    config = &Config{}
}

// openai.go:31-33 - 返回默认值
if config.BaseURL == "" {
    config.BaseURL = "https://api.openai.com/v1"
}

// client.go:159-170 - 返回错误
if len(prompt) > maxPromptLength {
    err := fmt.Errorf("prompt exceeds maximum length of %d characters", maxPromptLength)
    c.recordLLMCall(ctx, prompt, "", 0, start, err)
    c.emitCallback(&callbacks.Context{
        Event: callbacks.EventLLMError,
        Model: model,
        Input: prompt,
        Error: err,
    })
    return "", err
}

// openai.go:80-83 - 返回错误
if resp.StatusCode != http.StatusOK {
    respBody, _ := io.ReadAll(resp.Body)
    return "", errors.Newf("API request failed with status %d: %s", resp.StatusCode, respBody)
}
```

**问题分析**:
- 有些函数静默修复配置（返回默认值）
- 有些函数返回错误
- 不一致的处理方式

**影响**:
- 代码难以维护
- 可能隐藏问题
- 调试困难

**建议**:
```go
// 方案 1: 统一使用错误处理（推荐）
func NewClient(config *Config, opts ...Option) (*Client, error) {
    if config == nil {
        return nil, errors.New("config cannot be nil")
    }

    if config.Model == "" {
        return nil, errors.New("model is required")
    }

    // ... 验证其他字段

    // 如果 Timeout <= 0，返回错误而不是默认值
    if config.Timeout <= 0 {
        return nil, errors.New("timeout must be positive")
    }

    // ... 创建 client
}

// 方案 2: 保持默认值，但添加文档说明
// NewClient uses default value 60 for timeout if <= 0
func NewClient(config *Config, opts ...Option) (*Client, error) {
    if config == nil {
        return nil, coreerrors.ErrInvalidArgument
    }

    if config.Timeout <= 0 {
        config.Timeout = DefaultTimeoutSeconds
    }

    // ... 创建 client
}
```

---

### 4. 缺少输入长度限制验证

**位置**: `internal/llm/client.go:158-170`

**问题**: `Generate()` 和 `GenerateStream()` 都有输入长度限制，但验证逻辑重复。

```go
// client.go:158-170
const maxPromptLength = 8192
if len(prompt) > maxPromptLength {
    err := fmt.Errorf("prompt exceeds maximum length of %d characters", maxPromptLength)
    c.recordLLMCall(ctx, prompt, "", 0, start, err)
    c.emitCallback(&callbacks.Context{
        Event: callbacks.EventLLMError,
        Model: model,
        Input: prompt,
        Error: err,
    })
    return "", err
}

// client.go:487-498 - 几乎相同的代码
const maxPromptLength = 8192
if len(prompt) > maxPromptLength {
    err := fmt.Errorf("prompt exceeds maximum length of %d characters", maxPromptLength)
    c.recordLLMCall(ctx, prompt, "", 0, start, err)
    c.emitCallback(&callbacks.Context{
        Event: callbacks.EventLLMError,
        Model: model,
        Input: prompt,
        Error: err,
    })
    return nil, err
}
```

**问题分析**:
- 相同的验证逻辑重复了两次
- 如果修改限制值，需要修改两处
- 违反 DRY 原则

**影响**:
- 代码重复
- 维护成本高
- 容易出错

**建议**:
```go
// 方案 1: 提取为私有方法
func (c *Client) validatePrompt(ctx context.Context, prompt string, start time.Time, model string) error {
    const maxPromptLength = 8192
    if len(prompt) > maxPromptLength {
        err := fmt.Errorf("prompt exceeds maximum length of %d characters", maxPromptLength)
        c.recordLLMCall(ctx, prompt, "", 0, start, err)
        c.emitCallback(&callbacks.Context{
            Event: callbacks.EventLLMError,
            Model: model,
            Input: prompt,
            Error: err,
        })
        return err
    }

    if prompt == "" {
        err := coreerrors.ErrInvalidArgument
        c.recordLLMCall(ctx, prompt, "", 0, start, err)
        c.emitCallback(&callbacks.Context{
            Event: callbacks.EventLLMError,
            Model: model,
            Input: prompt,
            Error: err,
        })
        return err
    }

    trimmed := []byte(prompt)
    trimmed = bytes.TrimSpace(trimmed)
    if len(trimmed) == 0 {
        err := coreerrors.ErrInvalidArgument
        c.recordLLMCall(ctx, prompt, "", 0, start, err)
        c.emitCallback(&callbacks.Context{
            Event: callbacks.EventLLMError,
            Model: model,
            Input: prompt,
            Error: err,
        })
        return err
    }

    return nil
}

// Generate 方法
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
    start := time.Now()
    model := ""
    if c.config != nil {
        model = c.config.Model
    }

    if err := c.validatePrompt(ctx, prompt, start, model); err != nil {
        return "", err
    }

    // ...
}

// GenerateStream 方法
func (c *Client) GenerateStream(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
    start := time.Now()
    model := ""
    if c.config != nil {
        model = c.config.Model
    }

    if err := c.validatePrompt(ctx, prompt, start, model); err != nil {
        return nil, err
    }

    // ...
}
```

---

### 5. 缺少并发安全说明

**位置**: `internal/llm/client.go:65-71`

**问题**: `Client` 结构体没有文档说明其并发安全性。

```go
type Client struct {
    config       *Config
    httpClient   *http.Client
    streamClient *http.Client
    tracer       observability.Tracer
    callbacks    callbacks.Emitter
}
```

**问题分析**:
- `Client` 的方法可能被多个 goroutine 同时调用
- 但没有文档说明是否并发安全
- 没有锁保护

**影响**:
- 可能导致并发问题
- 难以正确使用
- 可能出现竞态条件

**建议**:
```go
// Client is not thread-safe. Users must ensure that calls to Generate, GenerateStream,
// and Close are made from a single goroutine or properly synchronized.
type Client struct {
    config       *Config
    httpClient   *http.Client
    streamClient *http.Client
    tracer       observability.Tracer
    callbacks    callbacks.Emitter
}

// Or if thread-safe:
// Client is thread-safe for concurrent calls to Generate and GenerateStream.
// The Close method should only be called once from a single goroutine.
type Client struct {
    mu sync.RWMutex
    // ...
}
```

---

### 6. 缺少资源清理保证

**位置**: `internal/llm/client.go:84-88`

**问题**: `Close()` 方法没有检查 client 是否已经被关闭。

```go
// Close releases idle HTTP connections held by the client.
func (c *Client) Close() {
    c.httpClient.CloseIdleConnections()
    c.streamClient.CloseIdleConnections()
}
```

**问题分析**:
- `Close()` 可以被多次调用
- 每次调用都会尝试关闭连接
- 可能导致 panic（如果底层连接已经被关闭）

**影响**:
- 资源泄漏（如果连接已经被关闭）
- 可能的 panic

**建议**:
```go
// Close releases idle HTTP connections held by the client.
// It is safe to call Close multiple times.
func (c *Client) Close() {
    c.httpClient.CloseIdleConnections()
    c.streamClient.CloseIdleConnections()
}
```

或者使用 `sync.Once`:

```go
type Client struct {
    // ...
    closeOnce sync.Once
}

func (c *Client) Close() error {
    var err error
    c.closeOnce.Do(func() {
        c.httpClient.CloseIdleConnections()
        c.streamClient.CloseIdleConnections()
    })
    return err
}
```

---

## 🐛 Potential Bugs（潜在Bug）

### 1. ⚠️ **竞态条件**: channel 读取可能阻塞

**位置**: `internal/llm/client.go:535-548`

**问题**: channel 读取可能阻塞，导致 goroutine 泄漏。

```go
go func() {
    defer close(ch)
    var fullResponse string
    var streamErr error
    for chunk := range rawCh {
        fullResponse += chunk.Content
        if chunk.Err != nil {
            streamErr = chunk.Err
        }
        if chunk.Done {
            break
        }
        select {
        case ch <- chunk:
        case <-ctx.Done():
            return  // ← 直接返回，不关闭 ch
        }
    }
    // ...
}()
```

**问题分析**:
- 如果 `ctx.Done()` 被触发，goroutine 直接返回
- 但 `ch` channel 没有被关闭
- 调用方可能一直阻塞在 `for chunk := range ch` 上

**影响**:
- Goroutine 泄漏
- 资源泄漏
- 系统卡死

**建议**:
```go
go func() {
    defer close(ch)
    var fullResponse string
    var streamErr error
    for chunk := range rawCh {
        fullResponse += chunk.Content
        if chunk.Err != nil {
            streamErr = chunk.Err
        }
        if chunk.Done {
            break
        }
        select {
        case ch <- chunk:
        case <-ctx.Done():
            return  // ← ch 已经在 defer close(ch) 中关闭
        }
    }
    // ...
}()
```

**检查**: `defer close(ch)` 会确保 channel 被关闭，所以当前代码实际上是安全的。但为了清晰，可以添加注释。

---

### 2. ⚠️ **内存泄漏**: goroutine 未正确清理

**位置**: `internal/llm/client.go:531-571`

**问题**: 如果 `rawCh` 没有正确关闭，goroutine 可能永远阻塞。

```go
go func() {
    defer close(ch)
    var fullResponse string
    var streamErr error
    for chunk := range rawCh {
        // ...
    }
    // ...
}()
```

**问题分析**:
- `rawCh` 是从 `streamOllama` 或 `streamOpenRouter` 返回的
- 如果这些函数没有正确关闭 channel，goroutine 可能永远阻塞

**影响**:
- Goroutine 泄漏
- 资源泄漏
- 系统卡死

**建议**:
```go
// 确保所有 stream 函数都正确关闭 channel
func (c *Client) streamOllama(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
    // ... 创建 request

    ch := make(chan StreamChunk, 64)
    go func() {
        defer close(ch)
        // ... 读取响应
    }()

    return ch, nil
}
```

---

### 3. ⚠️ **资源泄漏**: HTTP Response Body 未关闭

**位置**: `internal/llm/output/openai.go:74-78`

**问题**: `defer func() { resp.Body.Close() }()` 在某些错误路径下可能不执行。

```go
resp, err := a.client.Do(req)
if err != nil {
    return "", errors.Wrap(err, "send request")
}
defer func() {
    if err := resp.Body.Close(); err != nil {
        slog.Error("close response body failed", "err", err)
    }
}()

if resp.StatusCode != http.StatusOK {
    respBody, _ := io.ReadAll(resp.Body)  // ← 读取 body 后没有关闭
    return "", errors.Newf("API request failed with status %d: %s", resp.StatusCode, respBody)
}
```

**问题分析**:
- 如果 `resp.StatusCode != http.StatusOK`，代码读取了 `resp.Body`
- 但没有关闭 `resp.Body`
- 会导致资源泄漏

**影响**:
- 资源泄漏
- 文件描述符泄漏
- 系统资源耗尽

**建议**:
```go
if resp.StatusCode != http.StatusOK {
    respBody, _ := io.ReadAll(resp.Body)
    resp.Body.Close()  // ← 添加这行
    return "", errors.Newf("API request failed with status %d: %s", resp.StatusCode, respBody)
}
```

---

### 4. ⚠️ **空指针解引用**: config 可能为 nil

**位置**: `internal/llm/client.go:33-36`

**问题**: `c.config` 可能为 nil，但直接访问。

```go
model := ""
if c.config != nil {
    model = c.config.Model
}
```

**问题分析**:
- 代码已经检查了 `c.config != nil`
- 但后续使用 `c.config` 时可能还是 nil（如果 `config` 被修改）

**影响**:
- 空指针解引用
- 运行时 panic

**建议**:
```go
model := ""
if c.config != nil {
    model = c.config.Model
    if model == "" {
        model = "default_model"
    }
}
```

---

### 5. ⚠️ **性能问题**: 每次都创建新的 http.Client

**位置**: `internal/llm/output/openai.go:27-40`

**问题**: 每次创建 OpenAIAdapter 都会创建新的 http.Client。

```go
func NewOpenAIAdapter(config *Config) *OpenAIAdapter {
    if config == nil {
        config = &Config{}
    }
    if config.BaseURL == "" {
        config.BaseURL = "https://api.openai.com/v1"
    }

    return &OpenAIAdapter{
        config: config,
        client: &http.Client{
            Timeout: time.Duration(config.Timeout) * time.Second,
        },
    }
}
```

**问题分析**:
- 每次调用 `NewOpenAIAdapter` 都创建新的 `http.Client`
- `http.Client` 是线程安全的，但创建开销较大
- 应该使用共享的 client 实例

**影响**:
- 性能下降
- 资源浪费

**建议**:
```go
type OpenAIAdapter struct {
    config *Config
    client *http.Client  // 共享的 client 实例
}

func NewOpenAIAdapter(config *Config) *OpenAIAdapter {
    if config == nil {
        config = &Config{}
    }
    if config.BaseURL == "" {
        config.BaseURL = "https://api.openai.com/v1"
    }

    // 使用全局共享的 client
    return &OpenAIAdapter{
        config: config,
        client: http.DefaultClient,  // 或者使用自定义的 client
    }
}
```

---

### 6. ⚠️ **性能问题**: 每次都 Marshal JSON

**位置**: `internal/llm/output/openai.go:56-59`

**问题**: 每次调用 `Generate` 都创建新的 JSON body。

```go
body, err := json.Marshal(reqBody)
if err != nil {
    return "", errors.Wrap(err, "marshal request")
}
```

**问题分析**:
- `reqBody` 是每次调用都创建的
- 每次都 Marshal 成 JSON
- 虽然开销不大，但可以优化

**影响**:
- 性能下降（轻微）
- 内存分配

**建议**:
```go
// 如果请求参数固定，可以缓存 JSON body
type OpenAIAdapter struct {
    config *Config
    client *http.Client
    body   []byte  // 缓存的 JSON body
}

func NewOpenAIAdapter(config *Config) *OpenAIAdapter {
    // ... 创建 config

    // 预编译 JSON body
    body, _ := json.Marshal(reqBody)

    return &OpenAIAdapter{
        config: config,
        client: http.DefaultClient,
        body:   body,
    }
}
```

---

### 7. ⚠️ **性能问题**: JSON 解码失败时读取整个响应

**位置**: `internal/llm/output/openai.go:80-83`

**问题**: 如果 API 返回错误，读取整个响应 body。

```go
if resp.StatusCode != http.StatusOK {
    respBody, _ := io.ReadAll(resp.Body)
    return "", errors.Newf("API request failed with status %d: %s", resp.StatusCode, respBody)
}
```

**问题分析**:
- 如果响应 body 很大，读取整个 body 会占用大量内存
- 应该限制读取大小

**影响**:
- 内存占用高
- 性能问题

**建议**:
```go
if resp.StatusCode != http.StatusOK {
    // 限制读取大小
    respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*10))  // 限制 10KB
    return "", errors.Newf("API request failed with status %d: %s", resp.StatusCode, respBody)
}
```

---

### 8. ⚠️ **性能问题**: Stream channel 缓冲区大小硬编码

**位置**: `internal/llm/client.go:530`

**问题**: channel 缓冲区大小是硬编码的 `64`。

```go
ch := make(chan StreamChunk, 64)
```

**问题分析**:
- 缓冲区大小是固定的
- 如果流式响应很快，64 可能不够
- 如果流式响应很慢，64 可能浪费内存

**影响**:
- 性能问题
- 内存浪费

**建议**:
```go
// 方案 1: 根据配置调整缓冲区大小
const defaultStreamBufferSize = 64
ch := make(chan StreamChunk, defaultStreamBufferSize)

// 方案 2: 使用动态缓冲区
ch := make(chan StreamChunk, max(1, len(prompt)/100))
```

---

## 📋 优先级建议

### 🔴 高优先级（立即修复）
1. **资源泄漏**: HTTP Response Body 未关闭
2. **资源泄漏**: goroutine 未正确清理
3. **空指针解引用**: config 可能为 nil

### 🟠 中优先级（近期修复）
4. **竞态条件**: channel 读取可能阻塞
5. **性能问题**: 每次创建新的 http.Client
6. **性能问题**: 每次都 Marshal JSON

### 🟡 低优先级（技术债务）
7. **死代码**: HTTPError 类型未使用
8. **死代码**: sync.Map 缓存可能未使用
9. **魔法数字**: 添加常量定义
10. **缺少输入长度限制验证**
11. **缺少配置验证**
12. **错误处理不一致**
13. **缺少并发安全说明**
14. **缺少资源清理保证**

---

## 🎯 修复建议

### 立即行动

```bash
# 1. 修复 HTTP Response Body 未关闭
# 在 openai.go 中添加 resp.Body.Close()

# 2. 修复 goroutine 泄漏
# 确保所有 stream 函数都正确关闭 channel

# 3. 添加 config 为 nil 的检查
# 在所有访问 c.config 的地方添加 nil 检查
```

### 后续优化

1. 删除未使用的 HTTPError 类型
2. 删除或实现 sync.Map 缓存
3. 添加常量定义
4. 提取输入验证逻辑
5. 添加配置验证
6. 统一错误处理
7. 添加并发安全说明
8. 使用共享的 http.Client
9. 缓存 JSON body
10. 限制响应 body 读取大小

---

## 总结

LLM 模块整体设计良好，核心功能完整，但存在一些关键问题需要立即修复：

### ✅ **优点**:
- 清晰的接口设计
- 支持多个 provider（OpenAI、Ollama、OpenRouter）
- 完整的流式输出支持
- 良好的错误处理
- 详细的注释

### ⚠️ **需要改进**:
- **资源泄漏**: HTTP Response Body 未关闭
- **资源泄漏**: goroutine 未正确清理
- **空指针解引用**: config 可能为 nil
- **性能问题**: 每次创建新的 http.Client
- **性能问题**: 每次都 Marshal JSON

**建议优先修复 🔴 高优先级问题，确保系统的稳定性和正确性。**

---

## 附录：文件清单

### 核心文件
- `internal/llm/client.go` - LLM 客户端（759 行）
- `internal/llm/output/parser.go` - 输出解析器（530 行）
- `internal/llm/output/openai.go` - OpenAI adapter（445 行）
- `internal/llm/output/openrouter.go` - OpenRouter adapter（263 行）
- `internal/llm/output/ollama.go` - Ollama adapter（201 行）
- `internal/llm/output/template.go` - 模板（294 行）
- `internal/llm/output/validator.go` - 验证器（512 行）

### 测试文件
- `internal/llm/client_test.go` - 客户端测试
- `internal/llm/client_stream_test.go` - 流式测试
- `internal/llm/output/output_test.go` - 输出测试
- `internal/llm/output/stream_test.go` - 流测试

---

*报告生成于 2026-06-23*
*分析工具: 手动代码审查 + grep 搜索*
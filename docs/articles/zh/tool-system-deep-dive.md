# GoAgentX 架构深度解析（五）：工具系统 -- 能力矩阵与安全执行

## 一、引言

在上一篇文章中，我们深入探讨了 GoAgentX 的 Workflow Engine，了解了如何通过有向无环图（DAG）编排多 Agent 协作流程。然而，无论 Workflow 的编排多么精妙，最终任务的执行仍然需要落实到具体的工具调用上。

工具系统（Tool System）是 Agent 与外部世界交互的核心桥梁。一个 Agent 的能力边界，本质上是由其可调用的工具集合决定的。GoAgentX 的工具系统并非简单的工具集合，而是一套完整的能力抽象框架，涵盖了从**工具定义**、**注册发现**、**参数校验**到**安全执行**的完整生命周期。

本文将深入分析 GoAgentX 工具系统的架构设计、核心接口、22+ 内置工具的实现细节、安全模型，以及已知的设计缺陷。

## 二、架构总览：三层分层 + 注册中心模式

GoAgentX 的工具系统采用经典的三层分层架构，配合注册中心（Registry）模式实现工具的管理与调度。

```
┌──────────────────────────────────────────────────────┐
│                   Registry                           │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐   │
│  │ Tool A   │  │ Tool B   │  │ Tool C ...        │   │
│  │ (Name: )  │  │ (Name: )  │  │ (Name: )          │   │
│  └──────────┘  └──────────┘  └──────────────────┘   │
│   sync.RWMutex + map[string]Tool                     │
└──────────────────────────────────────────────────────┘
                        │
        ┌───────────────┼───────────────┐
        │               │               │
   Core Interface    Base Layer     Builtin Impls
   (Tool, Result,    (BaseTool,     (Calculator,
    Registry, ...)     ToolFunc)      CodeRunner, ...)
```

### 2.1 核心接口层（Core Interface Layer）

核心接口层定义在 `/Users/scc/go/src/goagent/internal/tools/resources/core/` 目录下，是整个工具系统的契约基础。

**Tool 接口**（`tool.go`）：

```go
type Tool interface {
    Name() string
    Description() string
    Category() ToolCategory
    Capabilities() []Capability
    Execute(ctx context.Context, params map[string]interface{}) (Result, error)
    Parameters() *ParameterSchema
}
```

每个工具必须实现六个方法：
- `Name()` -- 全局唯一的工具标识符，用于注册与查找
- `Description()` -- 工具的功能描述，供 LLM 理解何时使用
- `Category()` -- 工具分类（System / Core / Data / Knowledge / Memory / Domain / External）
- `Capabilities()` -- 能力标记数组，支持运行时能力过滤
- `Execute()` -- 实际的工具执行逻辑，接收 `map[string]interface{}` 参数
- `Parameters()` -- 参数模式的元数据定义，用于参数校验与 LLM 的参数生成引导

**Result 结构体**（`result.go`）：

```go
type Result struct {
    Success  bool                   `json:"success"`
    Data     interface{}            `json:"data,omitempty"`
    Error    string                 `json:"error,omitempty"`
    Metadata map[string]interface{} `json:"metadata,omitempty"`
}
```

Result 结构体通过 `Success` 字段区分成功与失败，`Data` 携带执行结果，`Metadata` 携带执行元数据。配套的辅助函数 `NewResult()`、`NewErrorResult()`、`ResultWithTiming()` 提供了便捷的构造方式。

**Registry 注册中心**（`registry.go`）：

```go
type Registry struct {
    tools map[string]Tool
    mu    sync.RWMutex
}

func NewRegistry() *Registry
func (r *Registry) Register(tool Tool) error
func (r *Registry) Get(name string) Tool
func (r *Registry) List() []Tool
func (r *Registry) Execute(ctx context.Context, name string, params map[string]interface{}) (Result, error)
func (r *Registry) Filter(filter ToolFilter) []Tool
```

Registry 使用 `sync.RWMutex` 保证并发安全，提供了 `Register`、`Unregister`、`Get`、`List`、`Execute`、`Filter` 等核心操作。配套的 `ToolGroup` 类型支持工具的逻辑分组，`ToolFilter` 支持按 Enabled/Disabled/Categories 进行运行时过滤。

全局单例 `GlobalRegistry` 与包级便利函数（`Register`、`Get`、`List`、`Execute`）使得工具的注册和使用都非常简洁。

**Capability 能力标记系统**：

```go
const (
    CapabilityMath     Capability = "math"
    CapabilityKnowledge Capability = "knowledge"
    CapabilityMemory   Capability = "memory"
    CapabilityNetwork  Capability = "network"
    CapabilityFile     Capability = "file"
    CapabilityText     Capability = "text"
    CapabilityTime     Capability = "time"
    CapabilityExternal Capability = "external"
)
```

能力标记系统支持 Agent 根据自身权限过滤可用工具，例如禁用网络访问的 Agent 将无法调用带有 `CapabilityNetwork` 标记的工具。

### 2.2 基础实现层（Base Layer）

基础实现层定义在 `/Users/scc/go/src/goagent/internal/tools/resources/base/` 目录下。

**BaseTool 结构体**：

```go
type BaseTool struct {
    name, description string
    category          core.ToolCategory
    capabilities      []core.Capability
    parameters        *core.ParameterSchema
    metadata          *core.ToolMetadata
}
```

BaseTool 为 `Tool` 接口的 5 个方法提供了默认实现（除了 `Execute`），开发者只需关注 `Execute` 方法的实现。配套的 `ToolLifecycle` 接口支持 `Init()` 和 `Stop()` 钩子方法（默认 no-op）。

**ToolFunc 函数式适配器**：

```go
func ToolFunc(name, description string, category core.ToolCategory,
    capabilities []core.Capability, params *core.ParameterSchema,
    execute func(context.Context, map[string]interface{}) (core.Result, error)) *BaseTool
```

ToolFunc 提供了函数式的工具定义方式，适用于简单的工具，无需定义新的结构体类型。

**工厂函数**：

```go
func NewBaseTool(name, description string, category core.ToolCategory, params *ParameterSchema) *BaseTool
func NewBaseToolWithCapabilities(name, description string, category core.ToolCategory,
    capabilities []core.Capability, params *ParameterSchema) *BaseTool
func NewBaseToolWithCategory(name, description string, category core.ToolCategory, params *ParameterSchema) *BaseTool
```

注意：`NewBaseToolWithCategory` 创建的工具具有空的 `capabilities` 列表，这是已知问题（详见下文）。

### 2.3 注册入口

注册入口在 `/Users/scc/go/src/goagent/internal/tools/resources/builtin/builtin.go` 中定义：

```go
func RegisterGeneralTools() {
    // -- 系统工具 --
    Register(NewIDGenerator())
    // -- 执行工具 --
    Register(NewCodeRunner())
    // -- 文件工具 --
    Register(NewFileTools())
    // -- 数学工具 --
    Register(NewCalculator())
    Register(NewDateTime())
    Register(NewTextProcessor())
    // -- 数据工具 --
    Register(NewJSONTools())
    Register(NewDataValidation())
    Register(NewDataTransform())
    Register(NewRegexTool())
    // -- 日志分析 --
    Register(NewLogAnalyzer())
    // -- 网络工具 --
    Register(NewHTTPRequest())
    Register(NewWebScraper(nil))
    // -- 规划工具 --
    Register(NewTaskPlanner(nil))
    // -- 知识库工具 --
    Register(NewKnowledgeSearch(nil))
    Register(NewKnowledgeAdd(nil))
    Register(NewKnowledgeUpdate(nil))
    Register(NewKnowledgeDelete(nil))
    Register(NewCorrectKnowledge(nil))
    // -- 记忆工具 --
    Register(NewMemorySearch(nil))
    Register(NewUserProfile(nil, nil))
    Register(NewDistilledMemorySearch(nil))
}
```

总共注册了 22 个工具，覆盖 8 大类别。

## 三、22+ 工具能力矩阵

### 3.1 系统工具（System Category）

**IDGenerator** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/system/id_generator.go`)

IDGenerator 使用 `github.com/google/uuid` 库生成 UUID v4 和短 ID（UUID 前 8 字符）。支持批量生成（1-100 个），适用于需要唯一标识符的场景。

### 3.2 执行工具（Execution）

**CodeRunner** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/execution/code_runner.go`)

CodeRunner 是系统中风险最高的工具，因此采用了多层安全防护：

1. **危险模式检测**：拦截 18 种危险代码模式（import os, import subprocess, eval(, exec(, open(, system( 等）
2. **混淆检测**：拦截 7 种混淆模式（chr(, ord(, \\x, base64., getattr, setattr, compile(）
3. **超时控制**：默认 30s，最大 60s
4. **输出限制**：默认 10KB 输出，最小 1KB
5. **代码长度限制**：最多 10000 字符
6. **进程组隔离**：`cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`，可单独终止整个进程树
7. **临时工作目录**：执行在临时目录中进行，执行完毕后自动清理

### 3.3 文件工具（File Tools）

**FileTools** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/file/file_tools.go`)

FileTools 支持读、写、列出文件操作，实现了**安全作用域控制**：

```go
type FileTools struct {
    *base.BaseTool
    allowedDir string
}
```

安全机制：
- `allowedDir` 字段限制了文件操作的范围（通过 `WithAllowedDir` 选项设置）
- 写文件时，如果父目录不存在则自动创建（`os.MkdirAll(dir, 0750)`）
- 读文件时支持 `offset`/`limit` 分页读取
- 文件不存在时，通过 `findSimilarFiles()` 提供路径推荐
- 列出文件支持递归、隐藏文件过滤、Glob 模式匹配

### 3.4 数学与文本工具（Math & Text）

**Calculator** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/math/calculator.go`)

Calculator 实现了完整的递归下降解析器，支持 `+`, `-`, `*`, `/`, `()`：

```go
evaluateExpression("100*(100+1)/2")  // 5050
```

解析流程：`parseAddSub` -> `parseMulDiv` -> `parseFactor` -> `parseNumber`，正确处理了运算符优先级和括号嵌套。

**DateTime** 支持 now/format/parse/add/diff 五种操作，支持多种常见时间格式的自动识别。

**TextProcessor** 支持 count/split/replace/uppercase/lowercase/trim/contains 七种操作。

**JSONTools** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/text/json_tools.go`)

JSONTools 支持 parse/extract/merge/pretty 四种操作。其中的 `extract` 支持点号导航（`user.name`）和数组索引（`items[0]`），`merge` 实现了深度递归合并。

**DataValidation** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/text/data_validation.go`)

支持 validate_json / validate_email（简化版 RFC 5322）/ validate_url（仅 http/https）/ validate_schema（简化 JSON Schema 校验）。

**DataTransform** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/text/data_transform.go`)

支持 csv_to_json（header/row 两种模式）/ json_to_csv（自动提取所有 key）/ flatten_json（递归展开，可配置分隔符）。

**RegexTool** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/text/regex_tool.go`)

支持 match/extract（捕获组）/ replace，支持 i/m/s 正则标记，max_results 限制匹配数量。

### 3.5 日志分析（Log Analyzer）

**LogAnalyzer** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/text/log_analyzer.go`)

LogAnalyzer 支持三种操作：
- `parse_log` -- 自动检测 JSON / Common Log Format / Combined Log Format / 简单格式，返回结构化条目
- `find_errors` -- 使用 8 个默认错误模式（error, exception, failed, fatal, panic, stack trace, timeout, denied）
- `extract_metrics` -- 使用 6 个默认指标模式（response_time_ms, latency_seconds, request_count, memory_mb, cpu_percent, throughput_rps），支持自定义模式

**已知问题**：LogAnalyzer 使用了 `NewBaseToolWithCategory()` 而非 `NewBaseToolWithCapabilities()`，导致其 `capabilities` 为空列表，在基于能力过滤时可能被错误排除。

### 3.6 网络工具（Network）

**HTTPRequest** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/network/http_request.go`)

支持 GET/POST/PUT/DELETE/PATCH 五种方法，自动解析 JSON 响应，支持自定义请求头，通过 `SetClient()` 支持 HTTP client 的依赖注入。

**WebScraper** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/network/web_scraper.go`)

通过 regexp 移除 script/style/nav/header/footer 等非内容元素，提取 title/body/links。支持 `HTTPGetter` 接口的依赖注入，默认使用 `DefaultHTTPClient`。

### 3.7 规划工具（Planning）

**TaskPlanner** (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/planning/task_planner.go`)

TaskPlanner 是系统中唯一使用 LLM 驱动的工具，支持三种操作：

```go
type TaskPlanner struct {
    *base.BaseTool
    llmClient *llm.Client
}
```

- `plan_tasks` -- 根据目标和可用工具生成完整的任务计划
- `decompose_task` -- 将复杂任务分解为更小的子任务，含依赖关系和优先级
- `estimate_time` -- 估算任务完成时间（LLM 不可用时返回默认值 30 分钟）

核心实现中包含一个精巧的 `extractJSON()` 函数，能正确处理嵌套括号、转义序列和引号字符串，从 LLM 的文本回复中提取 JSON 块：

```go
func extractJSON(text string) string {
    start := strings.Index(text, "{")
    // 括号计数 + 引号状态机 + 转义处理
    for i := start; i < len(text); i++ {
        if escapeNext { escapeNext = false; continue }
        if char == '\\' { escapeNext = true; continue }
        if char == '"' { inString = !inString; continue }
        if !inString {
            if char == '{' { braceCount++ }
            if char == '}' { braceCount--; if braceCount == 0 { return text[start:i+1] } }
        }
    }
}
```

### 3.8 知识库工具（Knowledge Base）

知识库工具组包含四种 CRUD 操作的工具：`KnowledgeSearch`、`KnowledgeAdd`、`KnowledgeUpdate`、`KnowledgeDelete`，以及一个专用的 `CorrectKnowledge`。

这些工具实现了**多租户隔离**，所有操作都需要 `tenant_id` 参数。它们通过接口（`KnowledgeSearcher`、`KnowledgeService`）进行依赖注入，便于测试和替换实现。

```go
type KnowledgeSearcher interface {
    Search(ctx context.Context, tenantID, query string) ([]*RetrievalResult, error)
}

type KnowledgeService interface {
    GetKnowledge(ctx context.Context, tenantID, itemID string) (*KnowledgeItem, error)
    UpdateKnowledge(ctx context.Context, tenantID string, item *KnowledgeItem) (*KnowledgeItem, error)
    AddKnowledge(ctx context.Context, item *KnowledgeItem) (*KnowledgeItem, error)
    DeleteKnowledge(ctx context.Context, tenantID, itemID string) error
}
```

`KnowledgeUpdate` 采用"读取-修改-写入"模式：先获取现有条目，然后只覆盖变更字段，保留其他字段不变。这使得 LLM 只需提供变更部分即可触发更新。

`CorrectKnowledge` (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/knowledge/correct_knowledge.go`) 直接操作 `repositories.KnowledgeRepositoryInterface`，通过添加 `corrected_at` 和 `correction` 元数据标记追踪纠正操作。

### 3.9 记忆工具（Memory）

记忆工具组包含三个工具：

**MemorySearch** -- 通过 `MemoryManager.SearchSimilarTasks()` 进行语义搜索，结果整理为统一的 memory 格式，limit 范围 1-20。

**UserProfile** -- 从蒸馏记忆中提取用户画像，包括技术栈（从"精通"/"擅长"关键词中解析）和偏好（从"喜欢"关键词中解析）。

**DistilledMemorySearch** -- 直接从数据库的蒸馏记忆中搜索，支持按 `user_id` 查询或向量搜索（向量搜索目前占位）。

## 四、安全模型

GoAgentX 工具系统的安全模型采用了**纵深防御**策略：

### 4.1 CodeRunner 沙箱

CodeRunner 是攻击面最大的工具，其安全防护覆盖了多个层面：

| 安全层 | 措施 | 实现 |
|--------|------|------|
| 静态分析 | 危险模式检测（18 种） | `strings.Contains` 匹配 |
| 静态分析 | 混淆检测（7 种） | `strings.Contains` 匹配 |
| 运行时 | 超时控制 | `context.WithTimeout` |
| 进程隔离 | 进程组隔离 | `Setpgid: true` |
| 资源限制 | 输出大小限制 | 截断 + 最大长度 |
| 资源限制 | 代码长度限制 | 10000 字符 |
| 环境隔离 | 临时工作目录 | `os.MkdirTemp` |
| 环境隔离 | 最小环境变量 | 仅提供 PATH |
| 功能开关 | Python/JS 可独立启用 | `enablePython` / `enableJS` |

### 4.2 FileTools 作用域控制

FileTools 通过 `allowedDir` 实现路径白名单，所有文件路径在操作前都会经过安全校验：

```
if t.allowedDir != "" {
    absPath, _ := filepath.Abs(filePath)
    absDir, _ := filepath.Abs(t.allowedDir)
    if !strings.HasPrefix(absPath, absDir) {
        return core.NewErrorResult("access denied: path is outside allowed directory")
    }
}
```

### 4.3 知识库多租户隔离

所有知识库工具都要求 `tenant_id` 参数，在数据层面实现了租户隔离。

### 4.4 LLM 参数引导

`ParameterSchema` 通过 `Type`、`Description`、`Enum`、`Required` 等字段为 LLM 提供参数生成的引导信息，减少了参数错误注入的风险。

## 五、扩展性设计

GoAgentX 工具系统的扩展性体现在多个维度：

1. **接口驱动**：`Tool` 接口使得新增工具只需实现接口方法
2. **函数式适配**：`ToolFunc` 支持零样板代码的工具定义
3. **依赖注入**：`KnowledgeSearcher`、`KnowledgeService`、`HTTPGetter`、`MemoryManager` 等接口支持
4. **装饰器模式**：`WithMetadata` 函数包装器可在不修改工具代码的情况下添加元数据
5. **组合模式**：`ToolGroup` 支持对工具进行逻辑分组
6. **运行时过滤**：`ToolFilter` 和 `Registry.Filter()` 支持运行时动态筛选

## 六、已知问题与设计缺陷

### 6.1 LogAnalyzer 缺少能力标记

```go
// 问题代码（NewBaseToolWithCategory 不会设置 capabilities）
BaseTool: base.NewBaseToolWithCategory("log_analyzer", "Parse logs...", core.CategoryCore, params),

// 应该使用（NewBaseToolWithCapabilities 会设置 capabilities）
BaseTool: base.NewBaseToolWithCapabilities("log_analyzer", "Parse logs...", core.CategoryCore,
    []core.Capability{core.CapabilityText}, params),
```

### 6.2 UserProfile 偏好提取缺陷

在 `memory_tools.go` 中，`extractPreferences` 函数存在多个缺陷：

1. **"不喜欢" 被 "喜欢" 误匹配**：`strings.Contains(content, "喜欢")` 会匹配包含"不喜欢"的内容，导致偏好提取错误
2. **只处理第一个 "喜欢"**：`strings.Split(content, "喜欢")` 只处理分割后的第一个元素
3. **不区分大小写缺失**：`addUniqueString` 虽然使用了 `strings.EqualFold` 做去重，但偏好提取逻辑中未使用

### 6.3 CodeRunner 静态分析局限性

当前的危险模式检测基于简单的 `strings.Contains` 字符串匹配，可以轻易被绕过：

- 字符串拼接：`"im" + "port os"` 可绕过 `"import os"` 检测
- 编码绕过：Base64 解码后的代码可以完全绕过所有模式检测
- Unicode 混淆：使用 Unicode 同形字符可以绕过关键词匹配

这些局限性需要在后续版本中通过更高级的静态分析技术（AST 解析、语义分析）来解决。

### 6.4 TaskPlanner 的 LLM 依赖

TaskPlanner 在 LLM 客户端不可用时只能返回默认值（30 分钟估算），缺乏无 LLM 模式下的备选规划策略。

## 七、总结

GoAgentX 的工具系统是一个经过精心设计的能力抽象框架。通过 Registry 模式实现了工具的集中管理，通过三层分层架构（Core Interface -> Base Layer -> Builtin Implementations）实现了关注点分离，通过 Capability 标记系统实现了细粒度的能力控制，通过多层安全防护（静态检测 / 进程隔离 / 超时控制 / 作用域限制）实现了安全执行。

22+ 内置工具覆盖了计算、文本处理、文件操作、网络请求、Web 抓取、代码执行、任务规划、知识库 CRUD、记忆搜索等常用能力，足以支撑大多数 Agent 应用场景。

然而，系统中仍存在一些值得关注的缺陷和局限性，如 LogAnalyzer 缺少能力标记、UserProfile 偏好提取的边界情况、CodeRunner 静态分析的局限性等。这些问题为后续的优化和演进指明了方向。

在下一篇文章中，我们将探讨 GoAgentX 的事件系统和可观测性架构。

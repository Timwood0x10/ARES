# GoAgentX Architecture Deep Dive (5): Tool System -- Capability Matrix and Safe Execution

## 1. Introduction

In any agent framework, the tool system is the bridge between language model reasoning and real-world action. Without tools, an LLM is confined to its training corpus -- it cannot calculate, search the web, read files, execute code, or access external knowledge bases. GoAgentX's tool system is designed as a layered, extensible architecture that addresses three fundamental challenges: **discoverability** (how does the agent know what tools exist?), **security** (how do we prevent malicious or accidental damage?), and **interoperability** (how do diverse tools present a uniform interface to the orchestration layer?).

This article provides a deep-dive analysis of GoAgentX's tool system, covering its core interfaces, registry pattern, built-in tool categories, security model, capability detection engine, and extensibility mechanisms. We will examine source code from key files to understand the design decisions that make this system both powerful and safe.

## 2. Architecture Overview

The tool system follows a clean layered architecture:

```
Application/Agent Layer
        |
   Registry Layer (GlobalRegistry, ToolGroup, ToolFilter)
        |
   Core Interface Layer (Tool, Result, Capability)
        |
   Base Implementation Layer (BaseTool, ToolFunc)
        |
   Built-in Tool Layer (math, network, text, knowledge, memory, execution, file, system, planning)
```

At the top, agents and workflows discover and invoke tools through a **thread-safe Registry**. Tools implement a minimal **Tool interface** with metadata, parameter schema, and an `Execute` method. A **BaseTool** struct provides default implementations for lifecycle hooks, and a **ToolFunc** adapter allows creating tools from simple functions without defining a full struct. The **CapabilityEngine** adds semantic discovery, matching natural language queries to appropriate tools based on keyword analysis.

## 3. Core Interface Layer

### 3.1 The Tool Interface

The foundational abstraction is the `Tool` interface, defined in `/Users/scc/go/src/goagent/internal/tools/resources/core/tool.go`:

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

This interface is intentionally minimal -- just seven methods. The `Category()` method classifies tools into one of seven categories:

```go
type ToolCategory string

const (
    CategorySystem     ToolCategory = "system"     // file ops, ID generation
    CategoryCore       ToolCategory = "core"       // HTTP, calculator, datetime
    CategoryData       ToolCategory = "data"       // JSON, validation
    CategoryKnowledge  ToolCategory = "knowledge"  // knowledge base CRUD
    CategoryMemory     ToolCategory = "memory"     // memory search, user profile
    CategoryDomain     ToolCategory = "domain"     // domain-specific (weather, search)
    CategoryExternal   ToolCategory = "external"   // MCP protocol tools
)
```

This categorization serves two purposes: it helps the registry filter tools by domain (e.g., only expose knowledge tools to certain workflows), and it aids capability detection.

### 3.2 Parameter Schema

Each tool declares its parameters using a `ParameterSchema` struct:

```go
type ParameterSchema struct {
    Type       string                `json:"type"`
    Properties map[string]*Parameter `json:"properties"`
    Required   []string              `json:"required"`
}

type Parameter struct {
    Type        string        `json:"type"`
    Description string        `json:"description"`
    Default     interface{}   `json:"default,omitempty"`
    Enum        []interface{} `json:"enum,omitempty"`
    Min         *float64      `json:"min,omitempty"`
    Max         *float64      `json:"max,omitempty"`
}
```

This schema is JSON-serializable, making it directly usable for LLM function-calling APIs, REST endpoints, and documentation generation. The `Enum`, `Min`, and `Max` fields enable automatic input validation.

### 3.3 Result Type

Every tool returns a `Result` struct, defined in `/Users/scc/go/src/goagent/internal/tools/resources/core/result.go`:

```go
type Result struct {
    Success  bool                   `json:"success"`
    Data     interface{}            `json:"data,omitempty"`
    Error    string                 `json:"error,omitempty"`
    Metadata map[string]interface{} `json:"metadata,omitempty"`
}
```

The `ResultWithTiming` utility function wraps a result with execution metadata:

```go
func ResultWithTiming(result Result, duration time.Duration) Result {
    if result.Metadata == nil {
        result.Metadata = make(map[string]interface{})
    }
    result.Metadata["duration_ms"] = duration.Milliseconds()
    result.Metadata["timestamp"] = time.Now().Unix()
    return result
}
```

This timing metadata is critical for observability -- it allows the agent to track which tools are slow, detect performance regressions, and feed execution metrics back into the planning loop.

The `ResultList` type aggregates multiple tool results, counting successes and failures:

```go
type ResultList struct {
    Results []Result `json:"results"`
    Total   int      `json:"total"`
    Success int      `json:"success"`
    Failed  int      `json:"failed"`
}
```

## 4. Registry Pattern

### 4.1 Thread-Safe Registry

The `Registry` struct, in `/Users/scc/go/src/goagent/internal/tools/resources/core/registry.go`, is the central tool management hub:

```go
type Registry struct {
    tools map[string]Tool
    mu    sync.RWMutex
}

func NewRegistry() *Registry {
    return &Registry{
        tools: make(map[string]Tool),
    }
}
```

The use of `sync.RWMutex` is a deliberate design choice: read operations (`Get`, `List`, `Count`, `GetSchemas`) acquire only a read lock, allowing concurrent reads without contention. Write operations (`Register`, `Unregister`, `Clear`) acquire an exclusive write lock. This matters because in a multi-agent scenario, many agents may query the tool registry simultaneously, but registration typically happens once during startup.

### 4.2 GlobalRegistry Singleton

GoAgentX provides a singleton `GlobalRegistry` with package-level convenience functions:

```go
var GlobalRegistry = NewRegistry()

func Register(tool Tool) error { return GlobalRegistry.Register(tool) }
func Get(name string) (Tool, bool) { return GlobalRegistry.Get(name) }
func List() []string { return GlobalRegistry.List() }
func Execute(ctx context.Context, name string, params map[string]interface{}) (Result, error) {
    return GlobalRegistry.Execute(ctx, name, params)
}
```

This singleton pattern simplifies tool registration for the common case: most applications never need multiple registries. However, the `Registry` type is fully exported, so users can create isolated registries for different tenants, workflows, or testing contexts.

### 4.3 Tool Filtering

The `ToolFilter` mechanism allows selective tool exposure:

```go
type ToolFilter struct {
    Enabled    []string       // if non-empty, only these tools
    Disabled   []string       // these tools are excluded
    Categories []ToolCategory // only these categories
}

func (r *Registry) Filter(filter *ToolFilter) *Registry {
    // Returns a new Registry with matching tools
}
```

This is useful for role-based access control -- you can create a filtered registry for a read-only agent that excludes execution tools, or a filtered registry for a data-science agent that only exposes data transformation tools.

### 4.4 ToolGroup Composition

The `ToolGroup` type groups related tools under a named namespace:

```go
type ToolGroup struct {
    name        string
    description string
    registry    *Registry
}
```

This enables logical organization -- for example, all knowledge base tools (search, add, update, delete, correct) can be grouped together as "knowledge_base", making it easier to reason about tool dependencies and capabilities.

## 5. Base Implementation Layer

### 5.1 BaseTool

The `BaseTool` struct, in `/Users/scc/go/src/goagent/internal/tools/resources/base/base_tool.go`, provides default implementations of all `Tool` interface methods plus lifecycle hooks:

```go
type BaseTool struct {
    name         string
    description  string
    category     core.ToolCategory
    capabilities []core.Capability
    parameters   *core.ParameterSchema
    metadata     *core.ToolMetadata
}

func (t *BaseTool) Init(ctx context.Context) error { return nil }  // lifecycle hook
func (t *BaseTool) Stop(ctx context.Context) error { return nil }  // lifecycle hook
```

Concrete tools embed `*base.BaseTool` and override `Execute()`. The `Init` and `Stop` hooks are optional -- most tools don't need them, but resource-intensive tools (like database connections or network listeners) can use them for proper lifecycle management.

Three constructors handle common patterns:

```go
func NewBaseTool(name, description string, params *core.ParameterSchema) *BaseTool
func NewBaseToolWithCategory(name, description string, category core.ToolCategory, params *core.ParameterSchema) *BaseTool
func NewBaseToolWithCapabilities(name, description string, category core.ToolCategory, capabilities []core.Capability, params *core.ParameterSchema) *BaseTool
```

### 5.2 ToolFunc Adapter

For simple tools that don't need their own struct, `ToolFunc` wraps a Go function as a `Tool`:

```go
type ToolFunc struct {
    BaseTool
    fn func(ctx context.Context, params map[string]interface{}) (core.Result, error)
}

func NewToolFunc(name, description string, params *core.ParameterSchema,
    fn func(ctx context.Context, params map[string]interface{}) (core.Result, error),
) *ToolFunc {
    return &ToolFunc{
        BaseTool: *NewBaseTool(name, description, params),
        fn:       fn,
    }
}

func (t *ToolFunc) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
    return t.fn(ctx, params)
}
```

This is a classic functional adapter pattern -- it converts any function with the right signature into a full `Tool` implementation without requiring a dedicated struct.

## 6. Built-in Tool Categories

The `RegisterGeneralTools()` function in `/Users/scc/go/src/goagent/internal/tools/resources/builtin/builtin.go` registers all built-in tools:

```go
func RegisterGeneralTools() error {
    tools := []core.Tool{
        // Math capability
        builtin_math.NewCalculator(),
        builtin_math.NewDateTime(),
        builtin_math.NewTextProcessor(),

        // Network capability
        builtin_network.NewHTTPRequest(),
        builtin_network.NewWebScraper(...),

        // File capability
        builtin_file.NewFileTools(),

        // Text capability
        builtin_text.NewJSONTools(),
        builtin_text.NewDataValidation(),
        builtin_text.NewDataTransform(),
        builtin_text.NewRegexTool(),
        builtin_text.NewLogAnalyzer(),

        // Knowledge capability
        builtin_knowledge.NewKnowledgeSearch(nil),
        builtin_knowledge.NewKnowledgeAdd(nil),
        builtin_knowledge.NewKnowledgeUpdate(nil),
        builtin_knowledge.NewKnowledgeDelete(nil),
        builtin_knowledge.NewCorrectKnowledge(nil),

        // Memory capability
        builtin_memory.NewMemorySearch(nil),
        builtin_memory.NewUserProfile(nil, nil),
        builtin_memory.NewDistilledMemorySearch(nil),

        // System capability
        builtin_system.NewIDGenerator(),

        // Execution capability
        builtin_execution.NewCodeRunner(),

        // Planning capability
        builtin_planning.NewTaskPlanner(nil),
    }
    // ... registration loop
}
```

This gives us approximately 20 built-in tools across 8 capability domains. Let's examine the most architecturally interesting ones.

### 6.1 Calculator -- Recursive Descent Parsing

The Calculator tool, in `/Users/scc/go/src/goagent/internal/tools/resources/builtin/math/calculator.go`, implements a recursive descent parser with four mutually recursive functions:

```go
func parseAddSub(expr string) (float64, error)      // + and -
func parseMulDiv(expr string) (float64, string, error)  // * and /
func parseFactor(expr string) (float64, string, error)  // parentheses
func parseNumber(expr string) (float64, string, error)  // numeric literals
```

The parsing chain is: `parseAddSub` calls `parseMulDiv`, which calls `parseFactor`, which can call back to `parseExpression` for parenthesized sub-expressions. This gives us correct operator precedence without external dependencies.

Division by zero is explicitly handled:

```go
case '/':
    right, newRemaining, err := parseFactor(remaining[1:])
    if right == 0 {
        return 0, "", fmt.Errorf("division by zero")
    }
    left /= right
```

### 6.2 HTTPRequest -- Full HTTP Client

The HTTP Request tool (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/network/http_request.go`) supports GET, POST, PUT, DELETE, and PATCH methods. It includes:

- Configurable timeout (default 30s, settable per-request)
- Automatic JSON body parsing
- Response header collection
- Duration tracking via `time.Since(startTime)`
- A `SetClient` method for dependency injection of custom HTTP clients

```go
req, err := http.NewRequestWithContext(reqCtx, method, url, bodyReader)
// ...
startTime := time.Now()
resp, err := t.client.Do(req)
duration := time.Since(startTime)

// Try to parse as JSON
var jsonBody interface{}
if err := json.Unmarshal(respBody, &jsonBody); err != nil {
    jsonBody = string(respBody)
}
```

The JSON parsing attempt means the response is always usable: if the server returns JSON, it's parsed; otherwise it's returned as a string.

### 6.3 WebScraper -- HTML Parsing with Dependency Injection

The WebScraper tool (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/network/web_scraper.go`) uses dependency injection for its HTTP client:

```go
type WebScraper struct {
    *base.BaseTool
    getter HTTPGetter  // interface for HTTP fetching
}

type HTTPGetter interface {
    Get(ctx context.Context, url string) (string, error)
}
```

This design makes the scraper testable: tests can inject a mock `HTTPGetter` that returns canned HTML without making real network calls. The HTML parsing uses regex-based extraction:

```go
func extractBody(html string, removeNav bool) string {
    // Remove script and style tags
    html = regexp.MustCompile(`<script[^>]*>.*?</script>`).ReplaceAllString(html, " ")
    html = regexp.MustCompile(`<style[^>]*>.*?</style>`).ReplaceAllString(html, " ")

    // Remove navigation elements if requested
    if removeNav {
        html = regexp.MustCompile(`<nav[^>]*>.*?</nav>`).ReplaceAllString(html, " ")
        html = regexp.MustCompile(`<header[^>]*>.*?</header>`).ReplaceAllString(html, " ")
        html = regexp.MustCompile(`<footer[^>]*>.*?</footer>`).ReplaceAllString(html, " ")
    }
    // ... extract body content and strip HTML tags
}
```

### 6.4 JSONTools -- Deep Merge and Path Extraction

The JSONTools tool (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/text/json_tools.go`) implements four operations: parse, extract, merge, and pretty-print.

The `extract` operation implements a simple path navigation supporting dot notation and array indices:

```go
// Supports dot notation (e.g., "user.name") and array indices (e.g., "items[0]")
func (t *JSONTools) extract(ctx context.Context, data, path string) (core.Result, error) {
    parts := strings.Split(path, ".")
    current := js

    for _, part := range parts {
        if strings.Contains(part, "[") && strings.Contains(part, "]") {
            // Handle array index
            base := strings.Split(part, "[")[0]
            indexStr := strings.Split(strings.Split(part, "[")[1], "]")[0]
            // ... navigate to array[index]
        } else {
            // Handle object field
            obj, ok := current.(map[string]interface{})
            current, exists = obj[part]
        }
    }
}
```

The `deepMerge` function performs recursive object merging, which is essential for combining configuration objects:

```go
func (t *JSONTools) deepMerge(base, override map[string]interface{}) map[string]interface{} {
    result := make(map[string]interface{})
    for k, v := range base { result[k] = v }
    for k, v := range override {
        if baseVal, exists := result[k]; exists {
            baseObj, ok1 := baseVal.(map[string]interface{})
            overrideObj, ok2 := v.(map[string]interface{})
            if ok1 && ok2 {
                result[k] = t.deepMerge(baseObj, overrideObj)
            } else {
                result[k] = v
            }
        } else {
            result[k] = v
        }
    }
    return result
}
```

### 6.5 LogAnalyzer -- Multi-Format Log Parsing

The LogAnalyzer (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/text/log_analyzer.go`) supports auto-detect of log formats and can parse JSON logs, Common Log Format, Combined Log Format, and simple text logs:

```go
func (t *LogAnalyzer) parseLog(ctx context.Context, logContent, logFormat string) (core.Result, error) {
    // Auto-detect format
    if format == "auto" {
        if strings.Contains(logContent, "\"timestamp\"") || strings.Contains(logContent, "{\"") {
            format = "json"
        } else if strings.Contains(logContent, " - - ") {
            format = "common"
        } else {
            format = "simple"
        }
    }
    // ... parse lines according to detected format
}
```

It also includes error detection with configurable patterns and metric extraction with statistical summaries (min, max, avg, sum).

### 6.6 Knowledge CRUD -- Multi-Tenant Isolation

The knowledge base tools (search, add, update, delete) all require a `tenant_id` parameter, enforcing multi-tenant isolation at the tool level. These tools accept a `KnowledgeSearcher` or `KnowledgeService` interface, enabling different backend implementations:

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

### 6.7 TaskPlanner -- LLM-Driven Planning

The TaskPlanner tool (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/planning/task_planner.go`) is unique because it itself calls an LLM to generate plans, decompose tasks, and estimate time. It uses structured prompts with JSON response parsing:

```go
func (t *TaskPlanner) buildPlanningPrompt(goal, context string, availableTools []string) string {
    prompt := `You are a task planning assistant...
Goal: ` + goal + `
// ... includes available tools, context, and JSON format specification
Please provide a detailed task plan in the following JSON format:
{
  "summary": "...",
  "steps": [...],
  "estimated_time": "X hours",
  "required_tools": [...],
  "risks": [...]
}`
    return prompt
}
```

The `extractJSON` function uses bracket counting with string-awareness to handle nested JSON in LLM responses:

```go
func extractJSON(text string) string {
    start := strings.Index(text, "{")
    braceCount := 0
    inString := false
    escapeNext := false
    for i := start; i < len(text); i++ {
        char := text[i]
        if escapeNext { escapeNext = false; continue }
        if char == '\\' { escapeNext = true; continue }
        if char == '"' { inString = !inString; continue }
        if !inString {
            switch char {
            case '{': braceCount++
            case '}': braceCount--
                if braceCount == 0 { return text[start : i+1] }
            }
        }
    }
    return ""
}
```

## 7. Security Model

### 7.1 CodeRunner -- Sandboxed Execution

The CodeRunner tool (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/execution/code_runner.go`) is the most security-critical tool. It implements a multi-layered defense:

**Layer 1 -- Dangerous Pattern Detection:**
```go
dangerousPatterns: []string{
    "import os", "import subprocess", "import shutil",
    "import sys", "import socket", "import pickle",
    "import marshal", "import ctypes", "import multiprocessing",
    "eval(", "exec(", "open(", "system(", "popen", "fork(",
    "__import__", "__builtins__",
},
```

**Layer 2 -- Obfuscation Detection:**
```go
obfuscationPatterns: []string{
    "chr(", "ord(", "\\x", "base64.",
    "getattr", "setattr", "compile(",
},
```

**Layer 3 -- Execution Guards:**
- Code length limit: 10,000 characters
- Timeout: 30 seconds default, 60 seconds maximum
- Output limit: 10KB default (configurable)
- Process group isolation via `Setpgid: true`

```go
func (t *CodeRunner) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
    if len(code) > 10000 {
        return core.NewErrorResult("code exceeds maximum length of 10000 characters"), nil
    }

    timeoutSeconds := getInt(params, "timeout_seconds", 30)
    if timeoutSeconds > 60 { timeoutSeconds = 60 }

    cmd := exec.CommandContext(ctx, "python3", "-c", code)
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
    cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
    workDir, _ := os.MkdirTemp("", "code-runner-*")
    // ... execution in isolated temp directory
}
```

The `Setpgid: true` flag creates a new process group, ensuring that if the parent process is killed, child processes are also terminated. The temporary directory is cleaned up via `os.RemoveAll` in a deferred call.

### 7.2 FileTools -- Path Validation

FileTools (`/Users/scc/go/src/goagent/internal/tools/resources/builtin/file/file_tools.go`) implements path-based security with the `allowedDir` constraint:

```go
type FileTools struct {
    *base.BaseTool
    allowedDir string  // if set, restricts file access to this directory
}
```

Before any read or write operation, the tool validates that the resolved absolute path is within the allowed directory:

```go
if t.allowedDir != "" {
    absPath, err := filepath.Abs(filePath)
    absDir, err := filepath.Abs(t.allowedDir)
    if !strings.HasPrefix(absPath, absDir) {
        return core.NewErrorResult("access denied: path is outside allowed directory"), nil
    }
}
```

This prevents directory traversal attacks. The `Skippedir` sentinel is used in recursive listing to skip hidden directories:

```go
if !includeHidden {
    base := filepath.Base(path)
    if strings.HasPrefix(base, ".") {
        if info.IsDir() { return filepath.SkipDir }
        return nil
    }
}
```

## 8. Capability Detection Engine

The capability engine (`/Users/scc/go/src/goagent/internal/tools/resources/core/capability.go`) provides semantic tool discovery. It maps natural language keywords to capabilities:

```go
var capabilityKeywords = map[Capability][]string{
    CapabilityMath:     {"calculate", "compute", "sum", "add", "subtract", "math", "formula", "count", "statistics", "quantify"},
    CapabilityKnowledge: {"knowledge", "learn", "information", "document", "article", "wiki", "参考资料", "知识"},
    CapabilityMemory:   {"memory", "remember", "recall", "history", "previous", "past", "forget", "记忆", "之前"},
    CapabilityText:     {"format", "parse", "transform", "convert", "json", "csv", "validate", "regex", "extract", "字符串"},
    CapabilityNetwork:  {"http", "api", "request", "fetch", "web", "url", "scrape", "网络", "请求"},
    CapabilityTime:     {"time", "date", "datetime", "schedule", "timestamp", "时区", "日期"},
    CapabilityFile:     {"file", "read", "write", "save", "load", "文件", "目录"},
    CapabilityExternal: {"execute", "run", "shell", "command", "code", "python", "script", "执行"},
}
```

The `Detect` method performs case-insensitive matching:

```go
func (e *CapabilityEngine) Detect(query string) []Capability {
    queryLower := strings.ToLower(query)
    detected := make([]Capability, 0, len(capabilityKeywords))
    detectedSet := make(map[Capability]bool)

    for cap, keywords := range capabilityKeywords {
        for _, keyword := range keywords {
            if strings.Contains(queryLower, keyword) {
                if !detectedSet[cap] {
                    detected = append(detected, cap)
                    detectedSet[cap] = true
                }
                break
            }
        }
    }
    return detected
}
```

The `Match` method combines detection with tool lookup, returning a complete set of tools that match the query's implied capabilities:

```go
func (e *CapabilityEngine) Match(query string) []Tool {
    caps := e.Detect(query)
    return e.Filter(caps)
}
```

## 9. Dependency Injection Patterns

Several tools accept interfaces in their constructors, enabling loose coupling and testability:

| Tool | Injected Interface | Purpose |
|------|-------------------|---------|
| WebScraper | `HTTPGetter` | Fetch web pages |
| Knowledge* | `KnowledgeSearcher` / `KnowledgeService` | Knowledge backend |
| MemorySearch | `MemoryManager` | Memory retrieval |
| UserProfile | `MemoryManager` + `DistilledMemoryRepositoryInterface` | User data access |
| TaskPlanner | `*llm.Client` | LLM-based planning |
| CorrectKnowledge | (repository layer) | Direct DB correction |

This is a clean example of the Dependency Inversion Principle -- high-level tools depend on abstractions, not concrete implementations.

## 10. Extensibility Mechanisms

### 10.1 Adding a New Built-in Tool

To add a new tool, a developer needs to:

1. Create a struct embedding `*base.BaseTool` (or use `base.ToolFunc` for simple cases)
2. Implement `Execute(ctx, params) (Result, error)`
3. Register it in `RegisterGeneralTools()`

For example, adding a simple echo tool:

```go
package mytool

type EchoTool struct {
    *base.BaseTool
}

func NewEchoTool() *EchoTool {
    return &EchoTool{
        BaseTool: base.NewBaseTool("echo", "Echo back the input text", &core.ParameterSchema{
            Type: "object",
            Properties: map[string]*core.Parameter{
                "text": {Type: "string", Description: "Text to echo"},
            },
            Required: []string{"text"},
        }),
    }
}

func (t *EchoTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
    text, _ := params["text"].(string)
    return core.NewResult(true, map[string]interface{}{"echo": text}), nil
}
```

### 10.2 External Tools via MCP

Tools with `CategoryExternal` are reserved for tools connected through the Model Context Protocol (MCP), allowing third-party tools to be integrated dynamically without modifying the GoAgentX core. This is how the system supports plugins, custom APIs, and external service integrations.

### 10.3 Custom Filtering and Groups

The `ToolFilter` and `ToolGroup` mechanisms allow runtime tool composition. A workflow for a junior developer might expose only `file_tools`, `code_runner`, and `knowledge_search`, while a data analysis workflow might expose `json_tools`, `data_transform`, and `calculator`.

## 11. Performance Considerations

The tool system is designed for low-latency operation:

- **Lock-free reads**: The `sync.RWMutex` in `Registry` allows concurrent reads without blocking.
- **Lazy construction**: Most tools are singletons created once during startup.
- **Direct invocation**: `Registry.Execute` looks up the tool by name in O(1) average time and calls its `Execute` method directly -- no reflection, no serialization overhead for internal calls.
- **Capability caching**: The `CapabilityEngine` builds its capability map once and rebuilds only when tools are registered or unregistered.

## 12. Summary

GoAgentX's tool system is an exercise in pragmatic architecture. It combines:

1. **A minimal, clean interface** (`Tool`) with seven methods that every tool must implement
2. **Thread-safe registration** via `sync.RWMutex` for concurrent access
3. **Hierarchical categorization** through `ToolCategory` and `Capability` enums
4. **Semantic discovery** with the `CapabilityEngine` for natural language tool matching
5. **Deep security** with multi-layer sandboxing in CodeRunner and path validation in FileTools
6. **Testability** through interface-based dependency injection
7. **Extensibility** via the functional adapter pattern, grouped registries, and MCP protocol external tools

The system balances power and safety: it provides rich tooling for LLM agents (web scraping, code execution, knowledge management, file operations) while implementing guardrails that prevent misuse. The separation of the tool interface, registry, and capability detection into distinct layers means each concern can be evolved independently.

For developers extending GoAgentX, the tool system is the primary integration point. Whether adding a custom database query tool, a Slack integration, or a domain-specific calculator, the patterns documented here provide a clear path forward.

---

*This article is part of the GoAgentX Architecture Deep Dive series. Source code referenced from the `improve` branch.*
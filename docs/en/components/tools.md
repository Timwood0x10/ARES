# Tools Design Document

## 1. Overview

The Tools module provides executable capability abstractions for Agents. The framework adopts a unified Tool interface design, supporting 6 major categories of tools covering system operations, data processing, knowledge management, and more.

### Core Philosophy

- **Unified Interface**: All tools implement the same `Tool` interface
- **Categorized Management**: Divided into 6 categories by function for Agent on-demand loading
- **Plug and Play**: 20+ built-in tools with support for custom extensions
- **Parameter Validation**: Each tool defines a complete parameter Schema

## 2. Core Interfaces

### 2.1 Tool Interface

```go
type Tool interface {
    Name() string                          // Tool name
    Description() string                   // Tool description
    Category() ToolCategory                // Tool category
    Execute(ctx context.Context, params map[string]interface{}) (Result, error)
    Parameters() *ParameterSchema          // Parameter definition
}
```

### 2.2 Result Structure

```go
type Result struct {
    Success  bool                   `json:"success"`
    Data     interface{}            `json:"data,omitempty"`
    Error    string                 `json:"error,omitempty"`
    Metadata map[string]interface{} `json:"metadata,omitempty"`
}
```

### 2.3 ParameterSchema Structure

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
}
```

## 3. Tool Categories

| Category | Description | Typical Tools |
|----------|-------------|---------------|
| `system` | System-level operations | file_tools, id_generator |
| `core` | General core tools | http_request, calculator, datetime |
| `data` | Data processing | json_tools, data_validation |
| `knowledge` | Knowledge base management | knowledge_search, knowledge_add |
| `memory` | Memory system | memory_search, user_profile |
| `domain` | Domain-specific | fashion_search, weather_check |

## 4. Built-in Tools List

### 4.1 Core Category Tools

| Tool | Description | Operations |
|------|-------------|------------|
| `http_request` | HTTP requests | GET, POST, PUT, DELETE, PATCH |
| `calculator` | Mathematical calculations | `add`, `subtract`, `multiply`, `divide`, `power`, `sqrt`, `abs`, `max`, `min` |
| `datetime` | Date/time operations | `now`, `format`, `parse`, `add`, `diff` |
| `text_processor` | Text processing | `count`, `split`, `replace`, `uppercase`, `lowercase`, `trim`, `contains` |
| `regex_tool` | Regex operations | `match`, `extract`, `replace` |
| `log_analyzer` | Log analysis | `parse_log`, `find_errors`, `extract_metrics` |
| `task_planner` | Task planning | `plan_tasks`, `decompose_task`, `estimate_time` |

#### calculator Operations

| Operation | Description | Example |
|-----------|-------------|---------|
| `add` | Addition | `operands: [1, 2, 3]` → 6 |
| `subtract` | Subtraction | `operands: [10, 3]` → 7 |
| `multiply` | Multiplication | `operands: [2, 3, 4]` → 24 |
| `divide` | Division | `operands: [100, 2]` → 50 |
| `power` | Power | `operands: [2, 10]` → 1024 |
| `sqrt` | Square root | `operands: [16]` → 4 |
| `abs` | Absolute value | `operands: [-5]` → 5 |
| `max` | Maximum | `operands: [1, 5, 3]` → 5 |
| `min` | Minimum | `operands: [1, 5, 3]` → 1 |

#### datetime Operations

| Operation | Description | Required Params |
|-----------|-------------|-----------------|
| `now` | Get current time | - |
| `format` | Format time | `time_string`, `format` |
| `parse` | Parse time string | `time_string` |
| `add` | Add duration | `time_string`, `duration` |
| `diff` | Calculate difference | `time_string` |

#### text_processor Operations

| Operation | Description | Required Params |
|-----------|-------------|-----------------|
| `count` | Count chars/words/lines | `text` |
| `split` | Split text | `text`, `separator` |
| `replace` | Replace text | `text`, `old`, `new` |
| `uppercase` | To uppercase | `text` |
| `lowercase` | To lowercase | `text` |
| `trim` | Trim whitespace | `text` |
| `contains` | Check contains | `text`, `substring` |

#### regex_tool Operations

| Operation | Description | Required Params |
|-----------|-------------|-----------------|
| `match` | Match check | `text`, `pattern` |
| `extract` | Extract groups | `text`, `pattern` |
| `replace` | Regex replace | `text`, `pattern`, `replacement` |

#### log_analyzer Operations

| Operation | Description | Required Params |
|-----------|-------------|-----------------|
| `parse_log` | Parse logs | `log_content`, `log_format` |
| `find_errors` | Find errors | `log_content` |
| `extract_metrics` | Extract metrics | `log_content` |

#### task_planner Operations

| Operation | Description | Required Params |
|-----------|-------------|-----------------|
| `plan_tasks` | Generate task plan | `goal` |
| `decompose_task` | Decompose task | `goal`, `task` |
| `estimate_time` | Estimate time | `goal` |

#### http_request Example

```go
result, _ := registry.Execute(ctx, "http_request", map[string]interface{}{
    "url":    "https://api.example.com/data",
    "method": "POST",
    "headers": map[string]interface{}{
        "Content-Type": "application/json",
    },
    "body":    `{"key": "value"}`,
    "timeout": 30,
})
```

#### calculator Example

```go
// Addition
result, _ := registry.Execute(ctx, "calculator", map[string]interface{}{
    "operation": "add",
    "operands":  []interface{}{10, 20, 30},
})
// result.Data = {"result": 60}

// Power
result, _ := registry.Execute(ctx, "calculator", map[string]interface{}{
    "operation": "power",
    "operands":  []interface{}{2, 10},
})
// result.Data = {"result": 1024}
```

### 4.2 Data Category Tools

| Tool | Description | Operations |
|------|-------------|------------|
| `json_tools` | JSON processing | `parse`, `extract`, `merge`, `pretty` |
| `data_validation` | Data validation | `validate_json`, `validate_email`, `validate_url`, `validate_schema` |
| `data_transform` | Data transformation | `csv_to_json`, `json_to_csv`, `flatten_json` |

#### json_tools Operations

| Operation | Description | Required Params |
|-----------|-------------|-----------------|
| `parse` | Parse JSON | `data` |
| `extract` | Extract field (dot notation supported) | `data`, `path` |
| `merge` | Deep merge JSON objects | `data`, `merge_data` |
| `pretty` | Pretty print | `data`, `indent` |

#### data_validation Operations

| Operation | Description | Required Params |
|-----------|-------------|-----------------|
| `validate_json` | Validate JSON format | `data` |
| `validate_email` | Validate email | `data` |
| `validate_url` | Validate URL | `data` |
| `validate_schema` | Validate JSON Schema | `data`, `schema` |

#### data_transform Operations

| Operation | Description | Required Params |
|-----------|-------------|-----------------|
| `csv_to_json` | Convert CSV to JSON | `data` |
| `json_to_csv` | Convert JSON to CSV | `data` |
| `flatten_json` | Flatten nested JSON | `data`, `separator` |

#### json_tools Example

```go
// Extract JSON field
result, _ := registry.Execute(ctx, "json_tools", map[string]interface{}{
    "operation": "extract",
    "data":      `{"user": {"name": "Alice", "age": 30}}`,
    "path":      "user.name",
})
// result.Data = {"value": "Alice"}

// Merge JSON
result, _ := registry.Execute(ctx, "json_tools", map[string]interface{}{
    "operation":  "merge",
    "data":       `{"a": 1}`,
    "merge_data": `{"b": 2}`,
})
// result.Data = {"merged": {"a": 1, "b": 2}}
```

### 4.3 System Category Tools

| Tool | Description | Operations |
|------|-------------|------------|
| `file_tools` | File operations | `read`, `write`, `list` |
| `id_generator` | ID generation | `generate_uuid`, `generate_short_id` |
| `code_runner` | Code execution | `run_python`, `run_js` |

#### file_tools Operations

| Operation | Description | Required Params |
|-----------|-------------|-----------------|
| `read` | Read file | `file_path` |
| `write` | Write file | `file_path`, `content` |
| `list` | List directory | `directory_path` |

#### id_generator Operations

| Operation | Description | Required Params |
|-----------|-------------|-----------------|
| `generate_uuid` | Generate UUID v4 | - |
| `generate_short_id` | Generate short ID (8 chars) | - |

#### code_runner Operations

| Operation | Description | Required Params | Note |
|-----------|-------------|-----------------|------|
| `run_python` | Execute Python code | `code` | Enabled by default |
| `run_js` | Execute JavaScript | `code` | Disabled by default |

#### file_tools Example

```go
// Read file
result, _ := registry.Execute(ctx, "file_tools", map[string]interface{}{
    "operation": "read",
    "file_path": "/tmp/test.txt",
    "offset":    0,
    "limit":     100,
})

// List directory
result, _ := registry.Execute(ctx, "file_tools", map[string]interface{}{
    "operation":      "list",
    "directory_path": "/tmp",
    "pattern":        "*.go",
    "recursive":      true,
})
```

### 4.4 Knowledge Category Tools

| Tool | Description | Main Parameters |
|------|-------------|-----------------|
| `knowledge_search` | Knowledge search | tenant_id, query, top_k, min_score |
| `knowledge_add` | Add knowledge | tenant_id, content, source, category, tags |
| `knowledge_update` | Update knowledge | tenant_id, item_id, content, reason |
| `knowledge_delete` | Delete knowledge | tenant_id, item_id, reason |
| `correct_knowledge` | Correct knowledge | tenant_id, item_id, correction |

> Note: Knowledge tools require injecting a `RetrievalService` instance.

### 4.5 Memory Category Tools

| Tool | Description | Main Parameters |
|------|-------------|-----------------|
| `memory_search` | Memory search | query, limit |
| `user_profile` | User profile | user_id, tenant_id, session_id |
| `distilled_memory_search` | Distilled memory search | query, user_id, limit |

> Note: Memory tools require injecting a `MemoryManager` instance.

### 4.6 Domain Category Tools

| Tool | Description | Main Parameters |
|------|-------------|-----------------|
| `fashion_search` | Fashion search | query, category, style, budget |
| `weather_check` | Weather query | location, date |
| `style_recommend` | Style recommendation | profile, occasion, season |

> Note: Domain tools require injecting the corresponding service instance.

## 5. Tool Registration

### 5.1 Global Registration

```go
import "goagent/internal/tools/resources"

// Register all built-in tools
resources.RegisterGeneralTools()

// Execute tool
result, err := resources.Execute(ctx, "calculator", map[string]interface{}{
    "operation": "add",
    "operands":  []interface{}{1, 2},
})
```

### 5.2 Custom Registry

```go
registry := resources.NewRegistry()

// Register tools
registry.Register(resources.NewCalculator())
registry.Register(resources.NewHTTPRequest())

// Filter tools
filtered := registry.Filter(&resources.ToolFilter{
    Categories: []resources.ToolCategory{resources.CategoryCore},
})
```

### 5.3 Custom Tools

```go
type MyTool struct {
    *resources.BaseTool
}

func NewMyTool() *MyTool {
    params := &resources.ParameterSchema{
        Type: "object",
        Properties: map[string]*resources.Parameter{
            "input": {
                Type:        "string",
                Description: "Input text",
            },
        },
        Required: []string{"input"},
    }

    return &MyTool{
        BaseTool: resources.NewBaseToolWithCategory(
            "my_tool",
            "My custom tool",
            resources.CategoryCore,
            params,
        ),
    }
}

func (t *MyTool) Execute(ctx context.Context, params map[string]interface{}) (resources.Result, error) {
    input := params["input"].(string)
    // Processing logic
    return resources.NewResult(true, map[string]interface{}{
        "output": strings.ToUpper(input),
    }), nil
}
```

## 6. Agent Integration

### 6.1 Predefined Configurations

The framework provides multiple Agent tool configurations:

```go
// Leader Agent - Focus on coordination and decision-making
config := resources.CreateAgentToolConfigs.Leader()

// Worker Agent - Focus on task execution
config := resources.CreateAgentToolConfigs.Worker()

// Research Agent - Focus on information gathering
config := resources.CreateAgentToolConfigs.Research()

// All tools
config := resources.CreateAgentToolConfigs.All()
```

### 6.2 Integration Example

```go
type MyAgent struct {
    tools *resources.AgentTools
}

func NewMyAgent() (*MyAgent, error) {
    // Register built-in tools
    resources.RegisterGeneralTools()

    // Create Agent toolset
    config := resources.CreateAgentToolConfigs.Worker()
    agentTools := resources.NewAgentTools(config)

    return &MyAgent{tools: agentTools}, nil
}

func (a *MyAgent) Execute(ctx context.Context, task string) (string, error) {
    // Get tool schemas (for LLM Function Calling)
    schemas := a.tools.GetSchemas()

    // Generate tool prompt
    prompt := a.tools.GenerateToolPrompt()

    // Execute tool
    result, err := a.tools.Execute(ctx, "calculator", map[string]interface{}{
        "operation": "add",
        "operands":  []interface{}{1, 2},
    })

    return fmt.Sprintf("%v", result.Data), nil
}
```

## 7. Helper Functions

```go
// Safely get string parameter
func getString(params map[string]interface{}, key string) string

// Safely get integer parameter
func getInt(params map[string]interface{}, key string, defaultVal int) int

// Safely get boolean parameter
func getBool(params map[string]interface{}, key string, defaultVal bool) bool
```

## 8. Error Handling

Tool execution returns a unified Result structure, judged by the `Success` field:

```go
result, err := registry.Execute(ctx, "http_request", params)
if err != nil {
    // System-level error
    log.Error("tool execution failed", "error", err)
    return
}

if !result.Success {
    // Business-level error
    log.Warn("tool returned error", "error", result.Error)
    return
}

// Success
data := result.Data
```

## 9. Extension Guide

### Adding New Tools

1. Create a new file in `internal/tools/resources/`
2. Implement the `Tool` interface (recommended to embed `BaseTool`)
3. Register in `RegisterGeneralTools()` in `builtin.go`
4. Update this document

### Tool Metadata

```go
tool := resources.WithMetadata(myTool, resources.ToolMetadata{
    Version: "1.0.0",
    Author:  "team",
    Tags:    []string{"utility", "data"},
    Deprecated: false,
})
```

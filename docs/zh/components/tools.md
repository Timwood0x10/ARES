# Tools 设计文档

## 1. 概述

Tools 模块为 Agent 提供可执行的能力抽象。框架采用统一的 Tool 接口设计，支持 6 大类工具，涵盖系统操作、数据处理、知识管理等场景。

### 核心理念

- **统一接口**：所有工具实现相同的 `Tool` 接口
- **分类管理**：按功能划分 6 个类别，便于 Agent 按需加载
- **即插即用**：内置 20+ 工具，支持自定义扩展
- **参数校验**：每个工具定义完整的参数 Schema

## 2. 核心接口

### 2.1 Tool 接口

```go
type Tool interface {
    Name() string                          // 工具名称
    Description() string                   // 工具描述
    Category() ToolCategory                // 工具类别
    Execute(ctx context.Context, params map[string]interface{}) (Result, error)
    Parameters() *ParameterSchema          // 参数定义
}
```

### 2.2 Result 结构

```go
type Result struct {
    Success  bool                   `json:"success"`
    Data     interface{}            `json:"data,omitempty"`
    Error    string                 `json:"error,omitempty"`
    Metadata map[string]interface{} `json:"metadata,omitempty"`
}
```

### 2.3 ParameterSchema 结构

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

## 3. 工具分类

| 类别 | 说明 | 典型工具 |
|------|------|----------|
| `system` | 系统级操作 | file_tools, id_generator |
| `core` | 通用核心工具 | http_request, calculator, datetime |
| `data` | 数据处理 | json_tools, data_validation |
| `knowledge` | 知识库管理 | knowledge_search, knowledge_add |
| `memory` | 记忆系统 | memory_search, user_profile |
| `domain` | 领域特定 | fashion_search, weather_check |

## 4. 内置工具列表

### 4.1 Core 类工具

| 工具名 | 说明 | Operations |
|--------|------|------------|
| `http_request` | HTTP 请求 | GET, POST, PUT, DELETE, PATCH |
| `calculator` | 数学计算 | `add`, `subtract`, `multiply`, `divide`, `power`, `sqrt`, `abs`, `max`, `min` |
| `datetime` | 日期时间操作 | `now`, `format`, `parse`, `add`, `diff` |
| `text_processor` | 文本处理 | `count`, `split`, `replace`, `uppercase`, `lowercase`, `trim`, `contains` |
| `regex_tool` | 正则操作 | `match`, `extract`, `replace` |
| `log_analyzer` | 日志分析 | `parse_log`, `find_errors`, `extract_metrics` |
| `task_planner` | 任务规划 | `plan_tasks`, `decompose_task`, `estimate_time` |

#### calculator 支持的 Operations

| Operation | 说明 | 示例 |
|-----------|------|------|
| `add` | 加法 | `operands: [1, 2, 3]` → 6 |
| `subtract` | 减法 | `operands: [10, 3]` → 7 |
| `multiply` | 乘法 | `operands: [2, 3, 4]` → 24 |
| `divide` | 除法 | `operands: [100, 2]` → 50 |
| `power` | 幂运算 | `operands: [2, 10]` → 1024 |
| `sqrt` | 平方根 | `operands: [16]` → 4 |
| `abs` | 绝对值 | `operands: [-5]` → 5 |
| `max` | 最大值 | `operands: [1, 5, 3]` → 5 |
| `min` | 最小值 | `operands: [1, 5, 3]` → 1 |

#### datetime 支持的 Operations

| Operation | 说明 | 必需参数 |
|-----------|------|----------|
| `now` | 获取当前时间 | - |
| `format` | 格式化时间 | `time_string`, `format` |
| `parse` | 解析时间字符串 | `time_string` |
| `add` | 增加时间 | `time_string`, `duration` |
| `diff` | 计算时间差 | `time_string` |

#### text_processor 支持的 Operations

| Operation | 说明 | 必需参数 |
|-----------|------|----------|
| `count` | 统计字符/词/行 | `text` |
| `split` | 分割文本 | `text`, `separator` |
| `replace` | 替换文本 | `text`, `old`, `new` |
| `uppercase` | 转大写 | `text` |
| `lowercase` | 转小写 | `text` |
| `trim` | 去空格 | `text` |
| `contains` | 检查包含 | `text`, `substring` |

#### regex_tool 支持的 Operations

| Operation | 说明 | 必需参数 |
|-----------|------|----------|
| `match` | 匹配检查 | `text`, `pattern` |
| `extract` | 提取捕获组 | `text`, `pattern` |
| `replace` | 正则替换 | `text`, `pattern`, `replacement` |

#### log_analyzer 支持的 Operations

| Operation | 说明 | 必需参数 |
|-----------|------|----------|
| `parse_log` | 解析日志 | `log_content`, `log_format` |
| `find_errors` | 查找错误 | `log_content` |
| `extract_metrics` | 提取指标 | `log_content` |

#### task_planner 支持的 Operations

| Operation | 说明 | 必需参数 |
|-----------|------|----------|
| `plan_tasks` | 生成任务计划 | `goal` |
| `decompose_task` | 分解任务 | `goal`, `task` |
| `estimate_time` | 估算时间 | `goal` |

#### http_request 示例

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

#### calculator 示例

```go
// calculator 支持的操作: add, subtract, multiply, divide, power, sqrt, abs, max, min

// 加法
result, _ := registry.Execute(ctx, "calculator", map[string]interface{}{
    "operation": "add",
    "operands":  []interface{}{10, 20, 30},
})
// result.Data = {"result": 60}

// 幂运算
result, _ := registry.Execute(ctx, "calculator", map[string]interface{}{
    "operation": "power",
    "operands":  []interface{}{2, 10},
})
// result.Data = {"result": 1024}
```

#### datetime 示例

```go
// datetime 支持的操作: now, format, parse, add, diff

// 获取当前时间
result, _ := registry.Execute(ctx, "datetime", map[string]interface{}{
    "operation": "now",
})
// result.Data = {"formatted": "2024-01-15 10:30:00", "unix": 1705301400}

// 计算时间差
result, _ := registry.Execute(ctx, "datetime", map[string]interface{}{
    "operation": "diff",
    "time_string": "2024-01-01",
})
// result.Data = {"days": 14, "hours": 10.5, ...}
```

#### text_processor 示例

```go
// text_processor 支持的操作: count, split, replace, uppercase, lowercase, trim, contains

// 字符统计
result, _ := registry.Execute(ctx, "text_processor", map[string]interface{}{
    "operation": "count",
    "text": "Hello World",
})
// result.Data = {"length": 11, "words": 2, "lines": 1}
```

#### regex_tool 示例

```go
// regex_tool 支持的操作: match, extract, replace

// 匹配
result, _ := registry.Execute(ctx, "regex_tool", map[string]interface{}{
    "operation": "match",
    "text": "email: test@example.com",
    "pattern": `[a-z]+@[a-z]+\.[a-z]+`,
    "flags": []string{"i"},
})
// result.Data = {"matched": true, "match_count": 1}
```

#### log_analyzer 示例

```go
// log_analyzer 支持的操作: parse_log, find_errors, extract_metrics

// 解析日志
result, _ := registry.Execute(ctx, "log_analyzer", map[string]interface{}{
    "operation": "parse_log",
    "log_content": "2024-01-15 ERROR: connection failed\n2024-01-15 INFO: retry",
    "log_format": "auto",
})
// result.Data = {"entries": [...], "count": 2}

// 查找错误
result, _ := registry.Execute(ctx, "log_analyzer", map[string]interface{}{
    "operation": "find_errors",
    "log_content": "ERROR: failed\nINFO: ok",
})
// result.Data = {"errors": [...], "error_count": 1}
```

#### task_planner 示例

```go
// task_planner 支持的操作: plan_tasks, decompose_task, estimate_time

// 规划任务
result, _ := registry.Execute(ctx, "task_planner", map[string]interface{}{
    "operation": "plan_tasks",
    "goal": "部署一个 Web 应用",
    "available_tools": []string{"file_tools", "http_request"},
})
// result.Data = {"steps": [...], "step_count": 5}
```

### 4.2 Data 类工具

| 工具名 | 说明 | Operations |
|--------|------|------------|
| `json_tools` | JSON 处理 | `parse`, `extract`, `merge`, `pretty` |
| `data_validation` | 数据校验 | `validate_json`, `validate_email`, `validate_url`, `validate_schema` |
| `data_transform` | 数据转换 | `csv_to_json`, `json_to_csv`, `flatten_json` |

#### json_tools 支持的 Operations

| Operation | 说明 | 必需参数 |
|-----------|------|----------|
| `parse` | 解析 JSON | `data` |
| `extract` | 提取字段（支持 dot notation） | `data`, `path` |
| `merge` | 深度合并 JSON 对象 | `data`, `merge_data` |
| `pretty` | 格式化输出 | `data`, `indent` |

#### data_validation 支持的 Operations

| Operation | 说明 | 必需参数 |
|-----------|------|----------|
| `validate_json` | 验证 JSON 格式 | `data` |
| `validate_email` | 验证邮箱 | `data` |
| `validate_url` | 验证 URL | `data` |
| `validate_schema` | 验证 JSON Schema | `data`, `schema` |

#### data_transform 支持的 Operations

| Operation | 说明 | 必需参数 |
|-----------|------|----------|
| `csv_to_json` | CSV 转 JSON | `data` |
| `json_to_csv` | JSON 转 CSV | `data` |
| `flatten_json` | 扁平化嵌套 JSON | `data`, `separator` |

#### json_tools 示例

```go
// json_tools 支持的操作: parse, extract, merge, pretty

// 提取 JSON 字段 (支持 dot  notation: user.name, items[0].id)
result, _ := registry.Execute(ctx, "json_tools", map[string]interface{}{
    "operation": "extract",
    "data":      `{"user": {"name": "Alice", "age": 30}}`,
    "path":      "user.name",
})
// result.Data = {"value": "Alice"}

// 合并 JSON (深度合并)
result, _ := registry.Execute(ctx, "json_tools", map[string]interface{}{
    "operation":  "merge",
    "data":       `{"a": 1}`,
    "merge_data": `{"b": 2}`,
})
// result.Data = {"merged": {"a": 1, "b": 2}}
```

#### data_transform 示例

```go
// data_transform 支持的操作: csv_to_json, json_to_csv, flatten_json

// CSV 转 JSON
result, _ := registry.Execute(ctx, "data_transform", map[string]interface{}{
    "operation": "csv_to_json",
    "data":      "name,age\nAlice,30",
    "has_header": true,
})
// result.Data = [{"name": "Alice", "age": "30"}]

// JSON 扁平化
result, _ := registry.Execute(ctx, "data_transform", map[string]interface{}{
    "operation": "flatten_json",
    "data":      `{"user": {"name": "Alice"}}`,
    "separator": ".",
})
// result.Data = {"user.name": "Alice"}
```

#### data_validation 示例

```go
// data_validation 支持的操作: validate_json, validate_email, validate_url, validate_schema

// 验证邮箱
result, _ := registry.Execute(ctx, "data_validation", map[string]interface{}{
    "operation": "validate_email",
    "data":      "test@example.com",
})
// result.Data = {"valid": true, "local_part": "test", "domain": "example.com"}
```

### 4.3 System 类工具

| 工具名 | 说明 | Operations |
|--------|------|------------|
| `file_tools` | 文件操作 | `read`, `write`, `list` |
| `id_generator` | ID 生成 | `generate_uuid`, `generate_short_id` |
| `code_runner` | 代码执行 | `run_python`, `run_js` |

#### file_tools 支持的 Operations

| Operation | 说明 | 必需参数 |
|-----------|------|----------|
| `read` | 读取文件 | `file_path` |
| `write` | 写入文件 | `file_path`, `content` |
| `list` | 列出目录 | `directory_path` |

#### id_generator 支持的 Operations

| Operation | 说明 | 必需参数 |
|-----------|------|----------|
| `generate_uuid` | 生成 UUID v4 | - |
| `generate_short_id` | 生成短 ID（8位） | - |

#### code_runner 支持的 Operations

| Operation | 说明 | 必需参数 | 备注 |
|-----------|------|----------|------|
| `run_python` | 执行 Python 代码 | `code` | 默认启用 |
| `run_js` | 执行 JavaScript | `code` | 默认禁用 |

#### file_tools 示例

```go
// file_tools 支持的操作: read, write, list

// 读取文件
result, _ := registry.Execute(ctx, "file_tools", map[string]interface{}{
    "operation": "read",
    "file_path": "/tmp/test.txt",
    "offset":    0,
    "limit":     100,
})
// result.Data = {"lines": [...], "total_lines": 100}

// 写入文件
result, _ := registry.Execute(ctx, "file_tools", map[string]interface{}{
    "operation": "write",
    "file_path": "/tmp/output.txt",
    "content":   "Hello World",
    "mode":      "write",
})

// 列出目录
result, _ := registry.Execute(ctx, "file_tools", map[string]interface{}{
    "operation":      "list",
    "directory_path": "/tmp",
    "pattern":        "*.go",
    "recursive":      true,
})
```

#### id_generator 示例

```go
// id_generator 支持的操作: generate_uuid, generate_short_id

// 生成 UUID
result, _ := registry.Execute(ctx, "id_generator", map[string]interface{}{
    "operation": "generate_uuid",
    "count":     3,
})
// result.Data = {"ids": ["uuid1", "uuid2", "uuid3"], "count": 3}

// 生成短 ID
result, _ := registry.Execute(ctx, "id_generator", map[string]interface{}{
    "operation": "generate_short_id",
})
// result.Data = {"id": "a1b2c3d4"}
```

#### code_runner 示例

```go
// code_runner 支持的操作: run_python, run_js
// 注意: 默认禁用 JS，仅启用 Python

// 执行 Python
result, _ := registry.Execute(ctx, "code_runner", map[string]interface{}{
    "operation": "run_python",
    "code":      "print('Hello, World!')",
    "timeout_seconds": 30,
})
// result.Data = {"success": true, "output": "Hello, World!", "execution_time": 100}

// 安全性: 危险模式会被拦截
result, _ := registry.Execute(ctx, "code_runner", map[string]interface{}{
    "operation": "run_python",
    "code":      "import os; os.system('rm -rf /')",
})
// result.Data = {"success": false, "error": "potentially dangerous pattern detected"}
```

### 4.4 Knowledge 类工具

| 工具名 | 说明 | 主要参数 |
|--------|------|----------|
| `knowledge_search` | 知识搜索 | tenant_id, query, top_k, min_score |
| `knowledge_add` | 添加知识 | tenant_id, content, source, category, tags |
| `knowledge_update` | 更新知识 | tenant_id, item_id, content, reason |
| `knowledge_delete` | 删除知识 | tenant_id, item_id, reason |
| `correct_knowledge` | 纠正知识 | tenant_id, item_id, correction |

> 注意：Knowledge 类工具需要注入 `RetrievalService` 实例才能使用。

### 4.5 Memory 类工具

| 工具名 | 说明 | 主要参数 |
|--------|------|----------|
| `memory_search` | 记忆搜索 | query, limit |
| `user_profile` | 用户画像 | user_id, tenant_id, session_id |
| `distilled_memory_search` | 蒸馏记忆搜索 | query, user_id, limit |

> 注意：Memory 类工具需要注入 `MemoryManager` 实例。

### 4.6 Domain 类工具

| 工具名 | 说明 | 主要参数 |
|--------|------|----------|
| `fashion_search` | 时尚搜索 | query, category, style, budget |
| `weather_check` | 天气查询 | location, date |
| `style_recommend` | 风格推荐 | profile, occasion, season |

> 注意：Domain 类工具需要注入对应的服务实例。

## 5. 工具注册

### 5.1 全局注册

```go
import "goagent/internal/tools/resources"

// 注册所有内置工具
resources.RegisterGeneralTools()

// 执行工具
result, err := resources.Execute(ctx, "calculator", map[string]interface{}{
    "operation": "add",
    "operands":  []interface{}{1, 2},
})
```

### 5.2 自定义注册表

```go
registry := resources.NewRegistry()

// 注册工具
registry.Register(resources.NewCalculator())
registry.Register(resources.NewHTTPRequest())

// 过滤工具
filtered := registry.Filter(&resources.ToolFilter{
    Categories: []resources.ToolCategory{resources.CategoryCore},
})
```

### 5.3 自定义工具

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
    // 处理逻辑
    return resources.NewResult(true, map[string]interface{}{
        "output": strings.ToUpper(input),
    }), nil
}
```

## 6. Agent 集成

### 6.1 预定义配置

框架提供多种 Agent 工具配置：

```go
// Leader Agent - 侧重协调和决策
config := resources.CreateAgentToolConfigs.Leader()

// Worker Agent - 侧重任务执行
config := resources.CreateAgentToolConfigs.Worker()

// Research Agent - 侧重信息收集
config := resources.CreateAgentToolConfigs.Research()

// 全部工具
config := resources.CreateAgentToolConfigs.All()
```

### 6.2 集成示例

```go
type MyAgent struct {
    tools *resources.AgentTools
}

func NewMyAgent() (*MyAgent, error) {
    // 注册内置工具
    resources.RegisterGeneralTools()

    // 创建 Agent 工具集
    config := resources.CreateAgentToolConfigs.Worker()
    agentTools := resources.NewAgentTools(config)

    return &MyAgent{tools: agentTools}, nil
}

func (a *MyAgent) Execute(ctx context.Context, task string) (string, error) {
    // 获取工具 Schema（用于 LLM Function Calling）
    schemas := a.tools.GetSchemas()

    // 生成工具 Prompt
    prompt := a.tools.GenerateToolPrompt()

    // 执行工具
    result, err := a.tools.Execute(ctx, "calculator", map[string]interface{}{
        "operation": "add",
        "operands":  []interface{}{1, 2},
    })

    return fmt.Sprintf("%v", result.Data), nil
}
```

## 7. 辅助函数

```go
// 安全获取字符串参数
func getString(params map[string]interface{}, key string) string

// 安全获取整数参数
func getInt(params map[string]interface{}, key string, defaultVal int) int

// 安全获取布尔参数
func getBool(params map[string]interface{}, key string, defaultVal bool) bool
```

## 8. 错误处理

工具执行返回统一的 Result 结构，通过 `Success` 字段判断成败：

```go
result, err := registry.Execute(ctx, "http_request", params)
if err != nil {
    // 系统级错误
    log.Error("tool execution failed", "error", err)
    return
}

if !result.Success {
    // 业务级错误
    log.Warn("tool returned error", "error", result.Error)
    return
}

// 成功
data := result.Data
```

## 9. 扩展指南

### 添加新工具

1. 在 `internal/tools/resources/` 创建新文件
2. 实现 `Tool` 接口（推荐嵌入 `BaseTool`）
3. 在 `builtin.go` 的 `RegisterGeneralTools()` 中注册
4. 更新本文档

### 工具元数据

```go
tool := resources.WithMetadata(myTool, resources.ToolMetadata{
    Version: "1.0.0",
    Author:  "team",
    Tags:    []string{"utility", "data"},
    Deprecated: false,
})
```

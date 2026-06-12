# LLM Output 标准化设计文档

## 1. 概述

由于系统中可能使用多种 LLM（GPT-OSS、Llama 等），需要设计统一的输出标准化机制，确保不同模型的输出格式一致，保证 Agent 工作流的稳定运行。

## 2. 多层保障机制

```
┌─────────────────────────────────────────────────────────────────┐
│                    LLM Output Standardization                    │
└─────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│  Layer 1: Prompt Template                                       │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ {{.Instructions}}                                        │   │
│  │ Output Format:                                           │   │
│  │ ```json                                                  │   │
│  │ { "items": [...], "reason": "..." }                      │   │
│  │ ```                                                      │   │
│  └──────────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌────────────────────────────────────────────────────────────────┐
│  Layer 2: JSON Schema / Tool Calling                            │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ {                                                          │   │
│  │   "type": "object",                                       │   │
│  │   "properties": {                                         │   │
│  │     "items": { "type": "array" },                        │   │
│  │     "reason": { "type": "string" }                       │   │
│  │   }                                                       │   │
│  │ }                                                          │   │
│  └──────────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌────────────────────────────────────────────────────────────────┐
│  Layer 3: Output Parser & Validator                             │
│  ┌────────────────────┐    ┌────────────────────┐            │
│  │    OutputParser    │───▶│ SchemaValidator    │            │
│  │  (JSON解析/修复)   │    │   (格式校验)       │            │
│  └────────────────────┘    └────────────────────┘            │
└────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌────────────────────────────────────────────────────────────────┐
│  Layer 4: LLM Adapter Layer                                     │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐                  │   │
│  │  │ Ollama  │  │ OpenAI  │  │ Anthropic│                  │   │
│  │  │ Adapter │  │ Adapter │  │ Adapter  │                  │   │
│  │  └─────────┘  └─────────┘  └─────────┘                  │   │
│  └──────────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────────┘
```

## 3. Layer 1: Prompt Template

### 3.1 基础模板

```go
type OutputTemplate struct {
    Description string `yaml:"description"`
    Schema      string `yaml:"schema"`
    Examples    []string `yaml:"examples"`
}

var RecommendOutputTemplate = `
你是一个专业的时尚顾问。请严格按照以下 JSON 格式输出：

{{.Schema}}

要求：
1. 只输出 JSON，不要其他内容
2. 字段值必须符合类型定义
3. 如果不确定的值，使用 null

{{.Examples}}

用户输入: {{.Input}}
`
```

### 3.2 Agent 特定模板

```yaml
# agent_top.md 中的 Output Format
## Output Format
```json
{
  "items": [
    {
      "item_id": "string",
      "name": "string",
      "price": 0.00,
      "reason": "string"
    }
  ],
  "summary": "string",
  "confidence": 0.0
}
```
```

## 4. Layer 2: JSON Schema / Tool Calling

### 4.1 Schema 定义

```go
type OutputSchema struct {
    Name        string      `json:"name"`
    Description string      `json:"description"`
    Type        string      `json:"type"` // "object"
    Properties  Properties  `json:"properties"`
    Required    []string   `json:"required"`
}

type Properties struct {
    Items     ItemSchema     `json:"items"`
    Summary   StringSchema   `json:"summary"`
    Confidence FloatSchema   `json:"confidence"`
}

// 推荐结果 Schema
var RecommendResultSchema = OutputSchema{
    Name:        "recommend_result",
    Description: "穿搭推荐结果",
    Type:        "object",
    Properties: Properties{
        Items: ItemSchema{
            Type: "array",
            Items: map[string]interface{}{
                "type": "object",
                "properties": map[string]interface{}{
                    "item_id":   map[string]string{"type": "string"},
                    "name":      map[string]string{"type": "string"},
                    "price":     map[string]string{"type": "number"},
                    "reason":    map[string]string{"type": "string"},
                },
            },
        },
        Summary: StringSchema{
            Type:        "string",
            Description: "推荐总结",
        },
        Confidence: FloatSchema{
            Type:        "number",
            Description: "置信度 0-1",
            Minimum:     0,
            Maximum:    1,
        },
    },
    Required: []string{"items", "summary"},
}
```

### 4.2 Tool Calling 强制结构化

```go
type ToolCallRequest struct {
    Name       string      `json:"name"`
    Arguments  interface{} `json:"arguments"` // JSON Schema 强制
}

// 使用 Tool Calling
req := &ChatRequest{
    Model: "gpt-oss:20b",
    Tools: []ToolDefinition{
        {
            Type: "function",
            Function: FunctionDefinition{
                Name:        "recommend",
                Description: "返回穿搭推荐结果",
                Parameters:  RecommendResultSchema,
            },
        },
    },
}
```

## 5. Layer 3: Output Parser & Validator

### 5.1 Output Parser

```go
type OutputParser struct {
    schemas map[string]*OutputSchema
}

func (p *OutputParser) Parse(output string, schemaName string) (interface{}, error) {
    schema := p.schemas[schemaName]
    
    // 1. 提取 JSON
    jsonStr := p.extractJSON(output)
    if jsonStr == "" {
        return nil, ErrNoJSONFound
    }
    
    // 2. 尝试解析
    var result interface{}
    if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
        // 3. 尝试修复
        fixed, fixErr := p.fixJSON(jsonStr)
        if fixErr != nil {
            return nil, fmt.Errorf("parse failed: %v, fix failed: %w", err, fixErr)
        }
        if err := json.Unmarshal([]byte(fixed), &result); err != nil {
            return nil, err
        }
    }
    
    return result, nil
}

// fixJSON 尝试修复破损的 JSON
func (p *OutputParser) fixJSON(jsonStr string) (string, error) {
    // 常见修复：
    // 1. 移除 markdown 代码块标记
    // 2. 补全缺失的引号
    // 3. 修复尾部逗号
    // 4. 处理单引号为双引号
    
    // 使用正则或简单修复
    re := regexp.MustCompile("```json|```")
    jsonStr = re.ReplaceAllString(jsonStr, "")
    
    // ... 更多修复逻辑
}
```

### 5.2 Schema Validator

```go
type SchemaValidator struct {
    validator *validate.Schema
}

func (v *SchemaValidator) Validate(data interface{}, schema *OutputSchema) error {
    // 使用 go-playground/validator
    s, err := schemaToValidator(schema)
    if err != nil {
        return err
    }
    
    err = s.Validate(data)
    if err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }
    
    return nil
}

// 自动重试机制
func (p *OutputParser) ParseWithRetry(output string, schemaName string, maxRetries int) (interface{}, error) {
    var lastErr error
    
    for i := 0; i < maxRetries; i++ {
        result, err := p.Parse(output, schemaName)
        if err == nil {
            return result, nil
        }
        lastErr = err
        
        // 重试时添加修复提示
        output = output + "\n\n注意：请确保输出是有效的 JSON 格式。"
    }
    
    return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}
```

## 6. Layer 4: LLM Adapter Layer

### 6.1 抽象接口

```go
type LLMAdapter interface {
    // Chat 聊天
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    
    // ChatWithSchema 结构化输出
    ChatWithSchema(ctx context.Context, req *ChatRequest, schema *OutputSchema) (*ChatResponse, error)
    
    // Embedding 嵌入
    Embedding(ctx context.Context, text string) ([]float64, error)
    
    // Name 适配器名称
    Name() string
}
```

### 6.2 Ollama Adapter

```go
type OllamaAdapter struct {
    baseURL string
    client  *http.Client
}

func (a *OllamaAdapter) ChatWithSchema(ctx context.Context, req *ChatRequest, schema *OutputSchema) (*ChatResponse, error) {
    // Ollama 支持 JSON Schema
    req.Format = "json" // 强制 JSON 输出
    
    return a.Chat(ctx, req)
}

func (a *OllamaAdapter) Name() string {
    return "ollama"
}
```

### 6.3 OpenAI Adapter

```go
type OpenAIAdapter struct {
    apiKey string
    client *http.Client
}

func (a *OpenAIAdapter) ChatWithSchema(ctx context.Context, req *ChatRequest, schema *OutputSchema) (*ChatResponse, error) {
    // 使用 Tool Calling
    req.Tools = []ToolDefinition{
        {
            Type: "function",
            Function: FunctionDefinition{
                Name:        "output",
                Description: schema.Description,
                Parameters:  schema,
            },
        },
    }
    
    return a.Chat(ctx, req)
}

func (a *OpenAIAdapter) Name() string {
    return "openai"
}
```

### 6.4 统一入口

```go
type LLMFactory struct {
    adapters map[string]LLMAdapter
}

func (f *LLMFactory) GetAdapter(name string) (LLMAdapter, error) {
    adapter, ok := f.adapters[name]
    if !ok {
        return nil, fmt.Errorf("adapter %s not found", name)
    }
    return adapter, nil
}

func (f *LLMFactory) Chat(ctx context.Context, adapterName string, req *ChatRequest) (*ChatResponse, error) {
    adapter, err := f.GetAdapter(adapterName)
    if err != nil {
        return nil, err
    }
    return adapter.Chat(ctx, req)
}
```

## 7. 完整调用流程

```go
func ExecuteAgent(ctx context.Context, agent *AgentDefinition, input string) (*AgentResult, error) {
    // 1. 获取适配器
    adapter, _ := llmFactory.GetAdapter(agent.LLM)
    
    // 2. 构建 Prompt（包含 Schema）
    prompt := buildPrompt(agent, input)
    
    // 3. 调用 LLM（强制结构化）
    resp, err := adapter.ChatWithSchema(ctx, &ChatRequest{
        Model:    agent.LLM,
        Messages: []Message{{Role: "user", Content: prompt}},
    }, agent.OutputSchema)
    if err != nil {
        return nil, err
    }
    
    // 4. Parser 解析
    result, err := parser.ParseWithRetry(resp.Message.Content, agent.Name, 3)
    if err != nil {
        return nil, err
    }
    
    // 5. Validator 校验
    if err := validator.Validate(result, agent.OutputSchema); err != nil {
        return nil, err
    }
    
    return result, nil
}
```

## 8. 配置示例

```yaml
# config.yaml
llm:
  default: ollama
  adapters:
    ollama:
      base_url: "http://localhost:11434"
      models:
        - name: gpt-oss:20b
          output_schema: recommend_result
        - name: llama3.2:3b
          output_schema: simple_text
          
output:
  schemas:
    recommend_result:
      type: object
      properties:
        items:
          type: array
        summary:
          type: string
          
  parser:
    max_retries: 3
    fix_json: true
    
  validator:
    strict_mode: true
```

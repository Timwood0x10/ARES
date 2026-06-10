# LLM Output Standardization Design Document

## 1. Overview

Since multiple LLMs (GPT-OSS, Llama, etc.) may be used in the system, a unified output standardization mechanism is needed to ensure consistent output formats across different models, ensuring stable operation of the Agent workflow.

## 2. Multi-Layer Protection Mechanism

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
│  │  (JSON Parse/Fix) │    │   (Validation)     │            │
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

### 3.1 Basic Template

```go
type OutputTemplate struct {
    Description string `yaml:"description"`
    Schema      string `yaml:"schema"`
    Examples    []string `yaml:"examples"`
}

var RecommendOutputTemplate = `
You are a professional fashion consultant. Please output strictly in the following JSON format:

{{.Schema}}

Requirements:
1. Only output JSON, nothing else
2. Field values must match type definitions
3. Use null for uncertain values

{{.Examples}}

User input: {{.Input}}
`
```

### 3.2 Agent-Specific Template

```yaml
# Output Format in agent_top.md
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

### 4.1 Schema Definition

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

// Recommendation Result Schema
var RecommendResultSchema = OutputSchema{
    Name:        "recommend_result",
    Description: "Fashion recommendation result",
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
            Description: "Recommendation summary",
        },
        Confidence: FloatSchema{
            Type:        "number",
            Description: "Confidence 0-1",
            Minimum:     0,
            Maximum:    1,
        },
    },
    Required: []string{"items", "summary"},
}
```

### 4.2 Tool Calling for Structured Output

```go
type ToolCallRequest struct {
    Name       string      `json:"name"`
    Arguments  interface{} `json:"arguments"` // JSON Schema enforced
}

// Using Tool Calling
req := &ChatRequest{
    Model: "gpt-oss:20b",
    Tools: []ToolDefinition{
        {
            Type: "function",
            Function: FunctionDefinition{
                Name:        "recommend",
                Description: "Return fashion recommendation result",
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
    
    // 1. Extract JSON
    jsonStr := p.extractJSON(output)
    if jsonStr == "" {
        return nil, ErrNoJSONFound
    }
    
    // 2. Try to parse
    var result interface{}
    if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
        // 3. Try to fix
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

// fixJSON tries to fix broken JSON
func (p *OutputParser) fixJSON(jsonStr string) (string, error) {
    // Common fixes:
    // 1. Remove markdown code block markers
    // 2. Complete missing quotes
    // 3. Fix trailing commas
    // 4. Handle single quotes to double quotes
    
    // Use regex or simple fixes
    re := regexp.MustCompile("```json|```")
    jsonStr = re.ReplaceAllString(jsonStr, "")
    
    // ... more fix logic
}
```

### 5.2 Schema Validator

```go
type SchemaValidator struct {
    validator *validate.Schema
}

func (v *SchemaValidator) Validate(data interface{}, schema *OutputSchema) error {
    // Use go-playground/validator
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

// Auto-retry mechanism
func (p *OutputParser) ParseWithRetry(output string, schemaName string, maxRetries int) (interface{}, error) {
    var lastErr error
    
    for i := 0; i < maxRetries; i++ {
        result, err := p.Parse(output, schemaName)
        if err == nil {
            return result, nil
        }
        lastErr = err
        
        // Add fix hint on retry
        output = output + "\n\nNote: Please ensure output is valid JSON format."
    }
    
    return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}
```

## 6. Layer 4: LLM Adapter Layer

### 6.1 Abstract Interface

```go
type LLMAdapter interface {
    // Chat chat
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    
    // ChatWithSchema structured output
    ChatWithSchema(ctx context.Context, req *ChatRequest, schema *OutputSchema) (*ChatResponse, error)
    
    // Embedding embedding
    Embedding(ctx context.Context, text string) ([]float64, error)
    
    // Name adapter name
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
    // Ollama supports JSON Schema
    req.Format = "json" // Force JSON output
    
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
    // Use Tool Calling
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

### 6.4 Unified Entry

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

## 7. Complete Call Flow

```go
func ExecuteAgent(ctx context.Context, agent *AgentDefinition, input string) (*AgentResult, error) {
    // 1. Get adapter
    adapter, _ := llmFactory.GetAdapter(agent.LLM)
    
    // 2. Build Prompt (including Schema)
    prompt := buildPrompt(agent, input)
    
    // 3. Call LLM (forced structured)
    resp, err := adapter.ChatWithSchema(ctx, &ChatRequest{
        Model:    agent.LLM,
        Messages: []Message{{Role: "user", Content: prompt}},
    }, agent.OutputSchema)
    if err != nil {
        return nil, err
    }
    
    // 4. Parser parse
    result, err := parser.ParseWithRetry(resp.Message.Content, agent.Name, 3)
    if err != nil {
        return nil, err
    }
    
    // 5. Validator validate
    if err := validator.Validate(result, agent.OutputSchema); err != nil {
        return nil, err
    }
    
    return result, nil
}
```

## 8. Configuration Example

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

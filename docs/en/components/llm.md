# LLM (Ollama) Design Document

## 1. Overview

The LLM module is responsible for communication with the Ollama service, supporting calls to various large language models including GPT-OSS and Llama3.2.

## 2. Core Functions

| Function | Description |
|----------|-------------|
| **Model Invocation** | Support streaming and non-streaming calls |
| **Embedding Generation** | Generate text vectors for RAG |
| **Conversation Management** | Manage conversation context |
| **Model Selection** | Select model based on task type |

## 3. Supported Models

| Model | Use Case | Context Length |
|-------|----------|----------------|
| gpt-oss:20b | Complex reasoning, recommendation generation | 32K |
| llama3.2:3b | Fast response, lightweight tasks | 8K |

## 4. Core Interfaces

```go
type LLM interface {
    // Chat chat
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    
    // ChatStream streaming chat
    ChatStream(ctx context.Context, req *ChatRequest) (<-chan *ChatResponse, error)
    
    // Embedding generate embedding vector
    Embedding(ctx context.Context, text string) ([]float64, error)
}

type ChatRequest struct {
    Model       string            `json:"model"`
    Messages    []Message         `json:"messages"`
    Temperature float64          `json:"temperature"` // 0.0-2.0
    MaxTokens   int               `json:"max_tokens"`
    Stream      bool              `json:"stream"`
    Tools       []ToolDefinition  `json:"tools,omitempty"`
}

type ChatResponse struct {
    Model      string      `json:"model"`
    Message    Message     `json:"message"`
    Done       bool        `json:"done"`
    TotalTokens int        `json:"total_tokens"`
}

type Message struct {
    Role    string `json:"role"`    // system, user, assistant
    Content string `json:"content"`
}
```

## 5. Ollama Client

```go
type OllamaClient struct {
    baseURL    string
    httpClient *http.Client
    timeout    time.Duration
}

func NewOllamaClient(baseURL string) *OllamaClient {
    return &OllamaClient{
        baseURL: baseURL,
        httpClient: &http.Client{
            Timeout: 120 * time.Second,
        },
        timeout: 60 * time.Second,
    }
}

// Chat implementation
func (c *OllamaClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
    // Build request
    endpoint := fmt.Sprintf("%s/api/chat", c.baseURL)
    // Send request
    // Handle response
    return response, nil
}

// Embedding implementation
func (c *OllamaClient) Embedding(ctx context.Context, text string) ([]float64, error) {
    endpoint := fmt.Sprintf("%s/api/embeddings", c.baseURL)
    // Generate embedding
}
```

## 6. Prompt Templates

```go
// User profile parsing prompt
var ParseProfilePrompt = `You are a fashion consultant. Extract the following from user input:
- Name
- Gender
- Age
- Occupation
- Style preference
- Budget range
- Preferred colors

User input: {{.Input}}

Return in JSON format.`

// Task planning prompt
var TaskPlanningPrompt = `Based on user profile {{.Profile}}, decide which Agents to use:
- agent_top: Tops
- agent_bottom: Bottoms
- agent_shoes: Shoes
- agent_head: Head accessories
- agent_accessory: Accessories

Return the list of Agents to invoke.`

// Result aggregation prompt
var AggregationPrompt = `Integrate the following recommendations into a final outfit suggestion:
{{.Results}}

User profile: {{.Profile}}`
```

## 7. Error Handling

| Error Code | Description | Strategy |
|------------|-------------|----------|
| 04-001 | LLMRequestFailed | Retry 3 times |
| 04-002 | LLMTimeout | Switch model or retry |
| 04-003 | LLMQuotaExceeded | Rate limit wait |
| 04-004 | LLMInvalidResponse | Retry or return error |

## 8. Configuration Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| ollama_base_url | http://localhost:11434 | Ollama service URL |
| default_model | gpt-oss:20b | Default model |
| embedding_model | llama3.2:3b | Embedding model |
| timeout | 60s | Request timeout |
| max_retries | 3 | Max retries |
| temperature | 0.7 | Default temperature |
| max_tokens | 2048 | Max tokens |

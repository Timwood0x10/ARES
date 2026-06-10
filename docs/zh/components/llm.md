# LLM (Ollama) 设计文档

## 1. 概述

LLM 模块负责与 Ollama 服务通信，支持多种大语言模型的调用，包括 GPT-OSS 和 Llama3.2 等。

## 2. 核心功能

| 功能 | 说明 |
|------|------|
| **模型调用** | 支持流式和非流式调用 |
| **嵌入生成** | 生成文本向量用于 RAG |
| **对话管理** | 管理对话上下文 |
| **模型选择** | 根据任务类型选择模型 |

## 3. 支持的模型

| 模型 | 用途 | 上下文长度 |
|------|------|------------|
| gpt-oss:20b | 复杂推理、推荐生成 | 32K |
| llama3.2:3b | 快速响应、轻量任务 | 8K |

## 4. 核心接口

```go
type LLM interface {
    // Chat 聊天
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    
    // ChatStream 流式聊天
    ChatStream(ctx context.Context, req *ChatRequest) (<-chan *ChatResponse, error)
    
    // Embedding 生成嵌入向量
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

## 5. Ollama 客户端

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

// Chat 实现
func (c *OllamaClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
    // 构建请求
    endpoint := fmt.Sprintf("%s/api/chat", c.baseURL)
    // 发送请求
    // 处理响应
    return response, nil
}

// Embedding 实现
func (c *OllamaClient) Embedding(ctx context.Context, text string) ([]float64, error) {
    endpoint := fmt.Sprintf("%s/api/embeddings", c.baseURL)
    // 生成嵌入
}
```

## 6. Prompt 模板

```go
// 用户画像解析 Prompt
var ParseProfilePrompt = `你是一个时尚顾问。请从用户输入中提取以下信息：
- 姓名
- 性别
- 年龄
- 职业
- 风格偏好
- 预算范围
- 偏好颜色

用户输入: {{.Input}}

请以 JSON 格式返回。`

// 任务规划 Prompt
var TaskPlanningPrompt = `根据用户画像 {{.Profile}}，请决定需要哪些 Agent 进行推荐：
- agent_top: 上衣
- agent_bottom: 下装
- agent_shoes: 鞋子
- agent_head: 头部配饰
- agent_accessory: 配饰

请返回需要调用的 Agent 列表。`

// 结果聚合 Prompt
var AggregationPrompt = `请整合以下推荐结果，生成最终搭配建议：
{{.Results}}

用户画像: {{.Profile}}`
```

## 7. 错误处理

| 错误码 | 说明 | 处理策略 |
|--------|------|----------|
| 04-001 | LLMRequestFailed | 重试 3 次 |
| 04-002 | LLMTimeout | 切换模型或重试 |
| 04-003 | LLMQuotaExceeded | 限流等待 |
| 04-004 | LLMInvalidResponse | 重试或返回错误 |

## 8. 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| ollama_base_url | http://localhost:11434 | Ollama 服务地址 |
| default_model | gpt-oss:20b | 默认模型 |
| embedding_model | llama3.2:3b | 嵌入模型 |
| timeout | 60s | 请求超时 |
| max_retries | 3 | 最大重试 |
| temperature | 0.7 | 默认温度 |
| max_tokens | 2048 | 最大 token 数 |

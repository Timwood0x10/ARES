# ARES API Client 使用指南

## 概述

`api/client` 是 ARES 系统对外提供的 Go 语言编程接口。外部项目可以通过它嵌入 ARES 的核心能力（LLM、Memory、Retrieval、Agent、Workflow），无需 import 任何 `internal/` 包。

## 安装

```bash
go get github.com/Timwood0x10/ares
```

## 快速开始

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/Timwood0x10/ares/api/client"
    "github.com/Timwood0x10/ares/api/core"
)

// 实现 core.LLMService 接口
type myLLM struct{}

func (m *myLLM) Generate(ctx context.Context, req *core.GenerateRequest) (*core.GenerateResponse, error) {
    return &core.GenerateResponse{Content: "Hello from ARES!"}, nil
}
func (m *myLLM) GenerateSimple(ctx context.Context, prompt string) (string, error) {
    return "Hello, " + prompt, nil
}
func (m *myLLM) GenerateEmbedding(ctx context.Context, req *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
    return &core.EmbeddingResponse{Embedding: []float32{0.1, 0.2}}, nil
}
func (m *myLLM) GetConfig() *core.LLMConfig                    { return &core.LLMConfig{Provider: "ollama"} }
func (m *myLLM) IsEnabled() bool                                 { return true }
func (m *myLLM) GetProvider() core.LLMProvider                   { return "ollama" }
func (m *myLLM) GetModel() string                                { return "llama3.2" }

func main() {
    cl, err := client.NewClient(&client.Config{
        BaseConfig: &core.BaseConfig{RequestTimeout: 30 * time.Second},
        LLM:       &myLLM{},
    })
    if err != nil {
        panic(err)
    }
    defer cl.Close(context.Background())

    svc, _ := cl.LLM()
    resp, _ := svc.GenerateSimple(context.Background(), "ARES")
    fmt.Println(resp) // Output: Hello, ARES
}
```

## 支持的 Service 接口

| 访问器 | 返回接口 | 需要实现 |
|---|---|---|
| `cl.LLM()` | `core.LLMService` | Generate, GenerateSimple, 等 |
| `cl.Memory()` | `core.MemoryService` | CreateSession, AddMessage, 等 |
| `cl.Retrieval()` | `core.RetrievalService` | Search, GetEmbedding, 等 |
| `cl.Agent()` | `core.AgentService` | CreateAgent, ExecuteTask, 等 |
| `cl.Workflow()` | `core.WorkflowService` | Execute, ExecuteStream |
| `cl.Runtime()` | `*runtimeSvc.Service` | 具体类型 |

## Config 详解

```go
type Config struct {
    // 基础配置
    BaseConfig *core.BaseConfig   // Timeout, MaxRetries, RetryDelay

    // Service 实例（已实现对应接口的对象）
    Agent     core.AgentService     // agent 服务
    Memory    core.MemoryService    // 记忆服务
    Retrieval core.RetrievalService // 检索服务
    LLM       core.LLMService       // LLM 服务
    Workflow  *workflowSvc.Config   // 工作流配置
}
```

所有 Service 字段都是接口类型（`core.AgentService`、`core.LLMService` 等）。外部调用者可以提供自己的实现，也可以使用 `api/bootstrap` 构建。

## 使用 api/bootstrap 构建服务

```go
import (
    "github.com/Timwood0x10/ares/api/bootstrap"
    "github.com/Timwood0x10/ares/api/client"
)

func main() {
    ares, _ := bootstrap.New(ctx, &bootstrap.Config{
        Runtime:   runtime.DefaultConfig(),
        Evolution: &evosvc.Config{...},
    })

    cl, _ := client.NewClient(&client.Config{
        LLM: ares.LLM,  // 从 bootstrap 获取
    })
}
```

## 测试指南

每个 `api/core` 接口都提供了对应的 mock 类型：

```go
import (
    "github.com/Timwood0x10/ares/api/core"
)

// 编译期验证：mockMemoryService 实现了 core.MemoryService
var _ core.MemoryService = (*mockMemoryService)(nil)
```

完整的 mock 文件在 `api/core/mock_*_test.go` 中。

## 设计原则

1. **零 internal 导入** — `api/client` 不 import 任何 `internal/` 包
2. **接口隔离** — 每个模块只依赖自己需要的接口，不依赖具体实现
3. **向后兼容** — 新增接口使用新方法名，不修改已有接口

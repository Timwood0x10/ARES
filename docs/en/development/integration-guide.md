# Integration Guide

**Last Updated**: 2026-03-23

## Introduction

This document describes how to integrate the GoAgent framework into existing projects, supporting two integration modes:

1. **Library Mode**: Use GoAgent directly as a dependency library
2. **Service Mode**: Run GoAgent as a standalone service

## Integration Modes

### Library Mode (Recommended for Go Projects)

Best for:
- Existing project is in Go language
- Need deep integration and customization
- Need high performance and low latency

**Code Location**: `api/service/`

#### Step 1: Add Dependency

Add GoAgent as a dependency in your project:

```bash
go get github.com/yourusername/goagent@latest
```

Or use go.mod:

```go
require github.com/yourusername/goagent v1.0.0
```

#### Step 2: Initialize Agent

Create a Leader Agent:

```go
package main

import (
    "context"
    "github.com/yourusername/goagent/api/service"
    "github.com/yourusername/goagent/internal/config"
)

func main() {
    // Load configuration
    cfg, err := config.Load("config/agent.yaml")
    if err != nil {
        panic(err)
    }

    // Create service
    agentService := service.NewAgentService(cfg)
    
    // Start Agent
    ctx := context.Background()
    if err := agentService.Start(ctx); err != nil {
        panic(err)
    }
    
    // Process request
    response, err := agentService.Process(ctx, "Your question")
    if err != nil {
        panic(err)
    }
    
    fmt.Println(response)
}
```

**Code Location**: `api/service/agent_service.go:30-50`

#### Step 3: Configuration File

Create `config/agent.yaml`:

```yaml
llm:
  provider: "ollama"
  base_url: "http://localhost:11434"
  model: "llama3.2"
  timeout: 60

agents:
  leader:
    id: "my-agent"
    max_steps: 5
    max_parallel_tasks: 3

  sub:
    - id: "agent-tool"
      type: "tool"
      triggers: ["tool", "工具"]

storage:
  enabled: false  # Disable if persistence is not needed
```

**Code Location**: `internal/config/config.go:20-50`

### Service Mode (Recommended for Multi-language Projects)

Best for:
- Existing project is not in Go language
- Need REST API integration
- Need distributed deployment

#### Step 1: Start GoAgent Service

```bash
# Clone project
git clone https://github.com/yourusername/goagent.git
cd goagent

# Configure service
cp config/server.example.yaml config/server.yaml

# Start service
go run cmd/server/main.go
```

Service will start at `http://localhost:8080`.

**Code Location**: `cmd/server/main.go:50-100`

#### Step 2: Call via API

Use REST API to interact with GoAgent:

```bash
# Create session
curl -X POST http://localhost:8080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "user123",
    "agent_config": {
      "type": "travel"
    }
  }'

# Send message
curl -X POST http://localhost:8080/api/v1/sessions/{session_id}/messages \
  -H "Content-Type: application/json" \
  -d '{
    "content": "Help me plan a trip"
  }'
```

**Code Location**: `api/service/handlers.go:80-120`

## Minimal Integration Examples

### Example 1: Simple Q&A Agent

Create a simple Q&A Agent:

```go
package main

import (
    "context"
    "fmt"
    "github.com/yourusername/goagent/api/service"
)

func main() {
    // Use default configuration
    cfg := &service.Config{
        LLM: service.LLMConfig{
            Provider: "ollama",
            Model:    "llama3.2",
        },
    }

    // Create Agent service
    agent := service.NewAgentService(cfg)
    
    // Start
    ctx := context.Background()
    agent.Start(ctx)
    
    // Process query
    result, err := agent.Process(ctx, "What is RAG?")
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    
    fmt.Printf("Answer: %s\n", result)
}
```

**Code Location**: `examples/simple/main.go:1-50`

### Example 2: Agent with Tools

Add tool support:

```go
cfg := &service.Config{
    LLM: service.LLMConfig{
        Provider: "ollama",
        Model:    "llama3.2",
    },
    Tools: service.ToolsConfig{
        Enabled: true,
        Tools: []service.ToolConfig{
            {
                Name: "calculator",
                Type: "builtin",
            },
            {
                Name: "datetime",
                Type: "builtin",
            },
        },
    },
}

agent := service.NewAgentService(cfg)
```

**Code Location**: `internal/tools/resources/core/agent_tools.go:100-150`

### Example 3: Agent with Memory

Enable memory system:

```go
cfg := &service.Config{
    LLM: service.LLMConfig{
        Provider: "ollama",
        Model:    "llama3.2",
    },
    Memory: service.MemoryConfig{
        Enabled: true,
        MaxHistory: 10,
    },
    Storage: service.StorageConfig{
        Enabled: true,
        Type:     "postgres",
        Host:     "localhost",
        Port:     5433,
        User:     "postgres",
        Password: "postgres",
        Database: "goagent",
    },
}

agent := service.NewAgentService(cfg)
```

**Code Location**: `internal/memory/production_manager.go:50-100`

## Configuration

### Basic Configuration

```yaml
# config/agent.yaml
llm:
  provider: "ollama"           # LLM provider
  base_url: "http://localhost:11434"
  model: "llama3.2"
  timeout: 60
  max_tokens: 2048

agents:
  leader:
    id: "agent-id"
    max_steps: 10
    max_parallel_tasks: 4
```

**Code Location**: `internal/config/config.go:20-50`

### Tools Configuration

```yaml
tools:
  enabled: true
  
  # Built-in tools
  builtin:
    - calculator
    - datetime
    - file_read
    - http_request
  
  # Custom tools
  custom:
    - name: "my_tool"
      path: "/path/to/tool"
      type: "executable"
```

**Code Location**: `internal/tools/resources/core/agent_tools.go:80-120`

### Storage Configuration

```yaml
storage:
  enabled: true
  type: "postgres"
  
  # PostgreSQL configuration
  postgres:
    host: "localhost"
    port: 5433
    user: "postgres"
    password: "postgres"
    database: "goagent"
  
  # pgvector configuration
  pgvector:
    enabled: true
    dimension: 1024
```

**Code Location**: `internal/storage/postgres/pool.go:35-80`

## Common Integration Scenarios

### Scenario 1: Web Application Integration

Integrate GoAgent into a web application:

```go
// Web server endpoint
func chatHandler(w http.ResponseWriter, r *http.Request) {
    var request struct {
        Message string `json:"message"`
    }
    
    if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    
    // Process with Agent
    result, err := agentService.Process(r.Context(), request.Message)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    json.NewEncoder(w).Encode(map[string]string{
        "response": result,
    })
}
```

**Code Location**: `examples/devagent/main.go:150-200`

### Scenario 2: CLI Tool Integration

Create a command-line tool:

```go
package main

import (
    "bufio"
    "context"
    "fmt"
    "os"
    "github.com/yourusername/goagent/api/service"
)

func main() {
    cfg := loadConfig()
    agent := service.NewAgentService(cfg)
    
    ctx := context.Background()
    agent.Start(ctx)
    
    scanner := bufio.NewScanner(os.Stdin)
    fmt.Println("Type 'exit' to quit")
    
    for scanner.Scan() {
        input := scanner.Text()
        if input == "exit" {
            break
        }
        
        result, err := agent.Process(ctx, input)
        if err != nil {
            fmt.Printf("Error: %v\n", err)
            continue
        }
        
        fmt.Printf("Agent: %s\n", result)
    }
}
```

**Code Location**: `examples/multi-agent-dialog/main.go:50-100`

### Scenario 3: Microservice Integration

Integrate in microservice architecture:

```go
// Inter-service communication
func (s *OrderService) ProcessWithAgent(ctx context.Context, order *Order) (*OrderResponse, error) {
    // Prepare Agent query
    query := fmt.Sprintf("Process order: %s, quantity: %d", order.ID, order.Quantity)
    
    // Call Agent service (local or remote)
    result, err := s.agentService.Process(ctx, query)
    if err != nil {
        return nil, err
    }
    
    // Parse Agent response
    return s.parseAgentResponse(result, order)
}
```

**Code Location**: `api/client/client.go:50-100`

## Error Handling

### Common Errors and Solutions

#### Error 1: Config File Not Found

```
Error: config file not found
```

**Solution**:
```go
// Check config file path
if _, err := os.Stat(configPath); os.IsNotExist(err) {
    // Use default config
    cfg = service.DefaultConfig()
} else {
    // Load config file
    cfg, err = config.Load(configPath)
}
```

**Code Location**: `internal/config/config.go:80-100`

#### Error 2: LLM Connection Failed

```
Error: failed to connect to LLM service
```

**Solution**:
```go
// Add retry logic
var result string
var err error

for i := 0; i < 3; i++ {
    result, err = agentService.Process(ctx, query)
    if err == nil {
        break
    }
    time.Sleep(time.Second * time.Duration(i+1))
}

if err != nil {
    return "", fmt.Errorf("LLM service unavailable after retries: %w", err)
}
```

**Code Location**: `internal/llm/client.go:150-180`

#### Error 3: Storage Connection Failed

```
Error: failed to connect to database
```

**Solution**:
```go
// Check database connection
if err := pool.Ping(ctx); err != nil {
    // Disable storage, use in-memory mode
    cfg.Storage.Enabled = false
    log.Warn("Storage disabled, using in-memory mode")
}
```

**Code Location**: `internal/storage/postgres/pool.go:50-80`

## Performance Optimization

### Connection Pool Configuration

```go
poolConfig := &pool.Config{
    MaxOpenConns:    25,
    MaxIdleConns:    10,
    ConnMaxLifetime: 5 * time.Minute,
}
```

**Code Location**: `internal/storage/postgres/pool.go:70-100`

### Concurrency Control

```go
cfg := &service.Config{
    Agents: service.AgentsConfig{
        Leader: service.LeaderConfig{
            MaxParallelTasks: 4,  // Control concurrency
        },
    },
}
```

**Code Location**: `internal/agents/leader/agent.go:120-150`

### Cache Configuration

```go
cfg := &service.Config{
    Cache: service.CacheConfig{
        Enabled: true,
        TTL:     30 * time.Minute,
    },
}
```

**Code Location**: `internal/llm/client.go:200-250`

## Testing Integration

### Unit Tests

```go
func TestAgentIntegration(t *testing.T) {
    ctx := context.Background()
    
    // Create test config
    cfg := service.DefaultConfig()
    
    // Create Agent
    agent := service.NewAgentService(cfg)
    
    // Start Agent
    if err := agent.Start(ctx); err != nil {
        t.Fatalf("Failed to start agent: %v", err)
    }
    
    // Test query
    result, err := agent.Process(ctx, "Test query")
    if err != nil {
        t.Errorf("Failed to process query: %v", err)
    }
    
    if result == "" {
        t.Error("Expected non-empty result")
    }
}
```

**Code Location**: `internal/agents/leader/agent_test.go:50-100`

### Integration Tests

```go
func TestEndToEndIntegration(t *testing.T) {
    // Start test database
    db := startTestDB(t)
    defer db.Close()
    
    // Create full config
    cfg := &service.Config{
        LLM: service.LLMConfig{
            Provider: "test",
        },
        Storage: service.StorageConfig{
            Enabled: true,
            Type:     "postgres",
            Host:     db.Host,
            Port:     db.Port,
        },
    }
    
    // Test complete flow
    agent := service.NewAgentService(cfg)
    ctx := context.Background()
    
    agent.Start(ctx)
    result, err := agent.Process(ctx, "Test message")
    
    assert.NoError(t, err)
    assert.NotEmpty(t, result)
}
```

**Code Location**: `api/service/integration_test.go:100-150`

## References

- [Quick Start](quick_start_en.md)
- [Architecture Documentation](arch.md)
- [Configuration Reference](../examples/travel/config/server.yaml)
- [API Documentation](storage/api_en.md)

## Support

For questions or help:
1. Check other sections of this document
2. Refer to example code (`examples/`)
3. Submit an Issue to GitHub

---

**Version**: 1.0  
**Last Updated**: 2026-03-23  
**Maintainer**: GoAgent Team
# GoAgent API Architecture

## Overview

GoAgent API uses a layered architecture, providing a unified, clear, extensible
interface surface. This document describes the structure of the `api/` layer
and how to use it.

The hard rule: **external modules and AI assistants never import `internal/`**.
Everything reachable from outside goes through `api/`.

## Layered Structure

```
api/
├── core/              # Core abstractions (interface definitions, public types)
│   ├── agent.go       # Agent / AgentConfig / AgentService / AgentRepository
│   ├── memory.go      # MemoryService / Message types
│   ├── retrieval.go   # RetrievalService interface
│   ├── llm.go         # LLMService / GenerateRequest / EmbeddingRequest
│   └: callbacks.go, arena.go, cleaning.go, dashboard.go, eval.go
│
├── client/            # Unified client (external entry point)
│   ├── client.go      # NewClient / Agent() / Memory() / LLM() / Workflow()
│   └: config.go, doc.go
│
├── service/           # Service layer (business logic implementations)
│   ├── agent/         # Agent service: Create/Get/Update/Delete/List/Execute
│   ├── memory/        # Memory service: sessions, messages, distillation
│   ├── llm/           # LLM service: Generate / GenerateSimple / Embedding
│   ├── retrieval/     # Retrieval service (knowledge search)
│   ├── evolution/     # Evolution executor (Evolve / BestStrategy / Stats)
│   ├── knowledge/     # Knowledge service (HTTP handlers)
│   ├── runtime/        # Runtime service
│   ├── workflow/        # Workflow service
│   └: arena, callbacks, dashboard, eval, events, flight
│
├── agent/             # Agent public interface (type aliases, AgentEvent)
├── graph/             # Dynamic DAG orchestration (Graph, Node, Edge)
├── knowledge/         # AKG knowledge graph (KnowledgeService, WorkingGraph)
├── evolution/         # Evolution building blocks (Strategy, Population, Mutator, Promoter)
├── embedding/         # Embedding service
├── experience/        # Experience repository + feedback
├── memory/            # Memory manager + distiller
├── llm/               # LLM service interface
├── mcp/               # MCP client
├── tools/             # Tool registry
├── workflow/          # Static workflow engine
├── bootstrap/         # Bootstrap helpers (wires internal components)
├── discovery/         # Discovery API
├── flight/            # Flight API
├── handler/           # HTTP handlers
├── integration/       # Integration API
├── router/            # Router API
└── README.md          # This file
```

### Layer Responsibilities

#### 1. Core Layer (`api/core/`)

**Responsibilities**:
- Define core interfaces (Repository and Service interfaces) for every module
- Define public data structures
- Provide type safety and abstraction

**Properties**:
- Pure interface definitions, no concrete implementations
- All types defined in `core/`
- Both `service/` and `client/` depend on `core/`

**Main interfaces**: `AgentRepository` / `AgentService`, `MemoryService`,
`RetrievalService`, `LLMService`, `WorkflowService`, `Arena`, `Evaluator`.

#### 2. Service Layer (`api/service/`)

**Responsibilities**:
- Implement the `core/` Service interfaces
- Orchestrate business logic
- Handle data validation and conversion
- Bridge to `internal/` implementations

**Properties**:
- Depends on `core/` interfaces
- Depends on `internal/` concrete implementations
- Not exposed directly to external callers — accessed via `client/`

#### 3. Client Layer (`api/client/`)

**Responsibilities**:
- Provide a unified client interface
- Manage lifetimes of all services
- Provide convenient access methods

**Usage**:

```go
client, err := client.NewClient(config)
if err != nil {
    log.Fatal(err)
}
defer client.Close(context.Background())

agentSvc, err := client.Agent()
memorySvc, err := client.Memory()
```

## Usage Guide

### 1. Initialize the Client

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/Timwood0x10/ares/api/client"
    "github.com/Timwood0x10/ares/api/core"
)

func main() {
    config := &client.Config{
        BaseConfig: &core.BaseConfig{
            RequestTimeout: 30 * time.Second,
            MaxRetries:     3,
            RetryDelay:     1 * time.Second,
        },
        // Agent, Memory, Retrieval, LLM, Workflow sub-configs...
    }

    c, err := client.NewClient(config)
    if err != nil {
        log.Fatal(err)
    }
    defer c.Close(context.Background())
}
```

### 2. Agent Service

```go
agentSvc, err := c.Agent()
if err != nil {
    log.Fatal(err)
}

// Create an agent.
agent, err := agentSvc.CreateAgent(ctx, &core.AgentConfig{
    ID:   "agent-001",
    Name: "My Agent",
    Type: "sub",
})
if err != nil {
    log.Fatal(err)
}

// List all agents of a given type.
agents, pagination, err := agentSvc.ListAgents(ctx, &core.AgentFilter{
    Type: "sub",
})
if err != nil {
    log.Fatal(err)
}
```

### 3. Memory Service

```go
memorySvc, err := c.Memory()
if err != nil {
    log.Fatal(err)
}

// Create a session.
sessionID, err := memorySvc.CreateSession(ctx, "user-001")
if err != nil {
    log.Fatal(err)
}

// Add a message.
err = memorySvc.AddMessage(ctx, sessionID, "user", "Hello")
if err != nil {
    log.Fatal(err)
}

// Retrieve messages.
messages, err := memorySvc.GetMessages(ctx, sessionID)
if err != nil {
    log.Fatal(err)
}
```

### 4. LLM Service

```go
llmSvc, err := c.LLM()
if err != nil {
    log.Fatal(err)
}

// Generate text.
response, err := llmSvc.GenerateSimple(ctx, "Write a short poem about spring.")
if err != nil {
    log.Fatal(err)
}
println(response)

// Generate embeddings.
embeddingResp, err := llmSvc.GenerateEmbedding(ctx, &core.EmbeddingRequest{
    Input: "Some text to embed.",
})
if err != nil {
    log.Fatal(err)
}
println(len(embeddingResp.Embedding))
```

## Error Handling

All API calls return errors. Handle them explicitly:

```go
agent, err := agentSvc.CreateAgent(ctx, config)
if err != nil {
    if errors.Is(err, core.ErrInvalidConfig) {
        // Handle invalid configuration.
    } else if errors.Is(err, core.ErrAgentAlreadyExists) {
        // Handle duplicate agent.
    } else {
        log.Fatal(err)
    }
}
```

## Best Practices

### 1. Dependency Injection

Inject dependencies via constructors:

```go
func NewService(config *Config) (*Service, error) {
    if config == nil {
        return nil, core.ErrInvalidConfig
    }
    // ...
}
```

### 2. Depend on Interfaces

Business logic should depend on interfaces, not concrete types:

```go
type Service struct {
    repo core.AgentRepository // depend on the interface
    // ...
}
```

### 3. Context Propagation

All async operations must propagate `context.Context`:

```go
func (s *Service) CreateAgent(ctx context.Context, config *core.AgentConfig) (*core.Agent, error) {
    // use ctx for timeout control and cancellation
}
```

### 4. Error Wrapping

Use `fmt.Errorf` with `%w` to preserve the error chain:

```go
return nil, fmt.Errorf("create agent: %w", err)
```

### 5. Concurrency Safety

Use appropriate synchronization to protect shared state:

```go
var mu sync.Mutex

func (s *Service) UpdateAgent(ctx context.Context, agentID string, updates map[string]any) (*core.Agent, error) {
    mu.Lock()
    defer mu.Unlock()
    // ...
}
```

## Extension Guide

### Adding a new service module

1. Define the interface in `api/core/`
2. Implement the service in `api/service/<module>/`
3. Add a client accessor in `api/client/client.go`

### Adding a new Repository implementation

1. Implement the `Repository` interface defined in `core/`
2. Inject the implementation via the Service config

## Migration Guide

From the legacy API to the new layered API:

1. Update import paths
2. Use the new `client.NewClient` initialization
3. Update error handling code
4. Test all functionality end-to-end

## Notes

1. **Backward compatibility**: the legacy API remains usable, but migration is recommended.
2. **Performance**: the new abstraction layer adds negligible overhead.
3. **Test coverage**: every service must have unit tests.
4. **Docs**: keep this file in sync with the actual `api/` structure.

## Contributing

When contributing code:

1. Follow `plan/rules/code_rules.md`
2. Add unit tests for new features
3. Update relevant documentation
4. Ensure all lint checks pass (`make check`)

## References

- [Code Rules](../plan/rules/code_rules.md)
- [Uber Go Style](../plan/rules/uber_go_style.md)
- [Skills](../plan/rules/skills.md)
- [Architecture](../docs/en/architecture/arch.md)
- [Examples](../examples/)
- [External API Guide](../plan/EXTERNAL_API_GUIDE.md)

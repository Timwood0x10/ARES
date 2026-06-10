# Graph - Dynamic Agent Orchestration

**GoAgent Graph** is a lightweight dynamic agent orchestration system that serves as an optional plugin to the Workflow Engine.

## Overview

Graph provides Go code-based dynamic DAG execution capabilities with support for conditional branching and pluggable schedulers. It runs in parallel with the existing Workflow Engine without depending on the static DAG execution of the Workflow Engine.

### Key Features

- ✅ **Dynamic Decision Making** - Supports runtime conditional branching
- ✅ **Pluggable Schedulers** - Default FIFO, optional priority and short-job-first
- ✅ **Zero Intrusion** - Wraps existing Agent/Tool interfaces
- ✅ **Production-Ready** - Automatically integrated with observability and rate limiting
- ✅ **Minimal** - Core code ~120 lines

### Comparison with Workflow Engine

| Dimension | Workflow Engine | Graph |
|-----------|----------------|-------|
| **Definition Method** | YAML/JSON Configuration | Go Code |
| **Execution Unit** | Step (Agent) | Node (Tool/Agent) |
| **Flow Type** | Static, Predefined | Dynamic, Runtime Decision |
| **Use Cases** | Predefined Workflows | Tool Composition, Conditional Branching |
| **Scheduling Strategy** | Dependency-Driven + FIFO | Pluggable Scheduler |

## Core Concepts

### State - Shared State

Shared state management for Graph runtime:

```go
type State struct {
    values map[string]any
}

func NewState() *State
func (s *State) Get(key string) (any, bool)
func (s *State) Set(key string, val any)
func (s *State) ToParams() map[string]any
```

**Design Points**:
- Lock-free design (single-threaded execution)
- Unified "node." prefix for node result storage
- Easy debugging and state tracking

### Node - Node

Unified node interface that wraps existing Agent/Tool/Func:

```go
type Node interface {
    Execute(ctx context.Context, state *State) error
    ID() string
}

// Three node types:
// 1. AgentNode - Wraps existing Agent
AgentNode(agent base.Agent)

// 2. ToolNode - Wraps existing Tool
ToolNode(tool core.Tool)

// 3. FuncNode - Supports simple functions
FuncNode(id string, fn func(context.Context, *State) error)
```

### Edge - Edge (Conditional Branching)

Edge definition with conditional branching support:

```go
type Edge struct {
    from string
    to   string
    cond Condition
}

type Condition func(state *State) bool

// Convenient condition constructor
func IfFunc(fn func(state *State) bool) Condition
```

**Usage Example**:
```go
g.Edge("check", "success", graph.IfFunc(func(s *State) bool {
    val, _ := s.Get("status")
    return val == "ok"
}))
```

### Graph - Graph

Graph definition and builder:

```go
type Graph struct {
    id        string
    nodes     map[string]Node
    edges     map[string][]*Edge
    start     string
    scheduler Scheduler
    tracer    observability.Tracer
    limiter   ratelimit.Limiter
}

// Builder pattern
g := graph.NewGraph("my-graph").
    Node("node1", graph.NewFuncNode("node1", ...)).
    Node("node2", graph.NewToolNode(tool)).
    Edge("node1", "node2").
    Start("node1")
```

### Scheduler - Scheduler

Pluggable scheduler interface:

```go
type Scheduler interface {
    Select(ready []string) string
}

// Three scheduler types:
// 1. DefaultScheduler - FIFO (default)
DefaultScheduler{}

// 2. PriorityScheduler - Priority scheduling
PriorityScheduler(priorities map[string]int)

// 3. ShortJobScheduler - Short-job-first scheduling
ShortJobScheduler(estimates map[string]int)
```

## Usage Examples

### Basic Usage

```go
g := graph.NewGraph("data-pipeline").
    Node("fetch", graph.NewToolNode(httpTool)).
    Node("parse", graph.NewToolNode(jsonTool)).
    Node("analyze", graph.NewToolNode(llmTool)).
    Edge("fetch", "parse").
    Edge("parse", "analyze").
    Start("fetch")

state := graph.NewState()
state.Set("input", "https://api.example.com/data")

result, err := g.Execute(context.Background(), state)
```

### Conditional Branching

```go
g := graph.NewGraph("error-handling").
    Node("api_call", graph.NewToolNode(apiTool)).
    Node("retry", graph.NewToolNode(retryTool)).
    Node("fallback", graph.NewToolNode(fallbackTool)).
    Edge("api_call", "retry", graph.IfFunc(func(s *State) bool {
        errType, _ := s.Get("error").(string)
        return errType == "timeout"
    })).
    Edge("api_call", "fallback", graph.IfFunc(func(s *State) bool {
        errType, _ := s.Get("error").(string)
        return errType == "permanent"
    })).
    Start("api_call")
```

### Custom Scheduler

```go
// Use priority scheduler
g := graph.NewGraph("priority-graph").
    Node("llm_node", graph.NewToolNode(llmTool)).
    Node("http_node", graph.NewToolNode(httpTool)).
    Node("db_node", graph.NewToolNode(dbTool)).
    Edge("llm_node", "http_node").
    Edge("http_node", "db_node").
    SetScheduler(graph.NewPriorityScheduler(map[string]int{
        "llm_node": 10,
        "http_node": 5,
        "db_node": 3,
    })).
    Start("llm_node")
```

### API Layer Usage

```go
import "goagent/api/service/graph"
import wfgraph "goagent/internal/workflow/graph"

// Create service
service, _ := graph.NewService(&graph.Config{
    RequestTimeout: 30 * time.Second,
    Tracer:        observability.NewLogTracer(nil),
})

// Execute graph
request := &graph.ExecuteRequest{
    GraphID: "my-graph",
    State: map[string]any{"input": "test"},
}

response, err := service.Execute(context.Background(), g, request)
```

## Directory Structure

```
internal/workflow/graph/
├── graph.go          # Graph definition + Edge
├── node.go           # Node wrappers
├── state.go          # State management
├── scheduler.go      # Pluggable schedulers
└── executor.go      # Execution engine

api/service/graph/
├── service.go        # Graph Service
└── service_test.go   # Service tests

examples/graph_demo/
├── basic_example.go
├── conditional_example.go
├── scheduler_example.go
└── config.yaml
```

## API Reference

### Constructors

```go
// Create new graph
func NewGraph(id string) *Graph

// With custom tracer
func NewGraphWithTracer(id string, tracer observability.Tracer) *Graph

// With custom rate limiter
func NewGraphWithLimiter(id string, limiter ratelimit.Limiter) *Graph
```

### Builder Methods

```go
// Add node
func (g *Graph) Node(id string, node Node) *Graph

// Add edge
func (g *Graph) Edge(from, to string, cond ...Condition) *Graph

// Set start node
func (g *Graph) Start(id string) *Graph

// Set scheduler
func (g *Graph) SetScheduler(scheduler Scheduler) *Graph

// Set tracer
func (g *Graph) SetTracer(tracer observability.Tracer) *Graph

// Set limiter
func (g *Graph) SetLimiter(limiter ratelimit.Limiter) *Graph
```

### Service API

```go
// Create service
func NewService(config *Config) (*Service, error)

// Execute graph
func (s *Service) Execute(ctx context.Context, g *wfgraph.Graph, request *ExecuteRequest) (*ExecuteResponse, error)

// Execute with builder function
func (s *Service) ExecuteWithGraphBuilder(ctx context.Context, graphID string, builder func(*wfgraph.Graph) *wfgraph.Graph, request *ExecuteRequest) (*ExecuteResponse, error)

// Validate graph
func (s *Service) ValidateGraph(g *wfgraph.Graph) error

// Get graph info
func (s *Service) GetGraphInfo(g *wfgraph.Graph) *GraphInfo
```

## Design Decisions

### Why Not Depend on Workflow Engine?

1. **Static vs Dynamic**: Workflow Engine is a static DAG, cannot support dynamic conditional branching
2. **Conditional Branch Loss**: Converting to Workflow loses `Edge.Condition` information
3. **Different Scheduling Strategy**: Graph needs to support runtime scheduling decisions

### Why State is Lock-Free?

1. **Single-Threaded Execution**: Graph execution is single-threaded (unless parallel nodes are supported)
2. **Simplified Complexity**: Locks are unnecessary complexity
3. **Performance Consideration**: Avoid lock contention

### Why Only IfFunc?

1. **Flexibility**: `If("score", ">80")` cannot be implemented because `expected any` cannot compare strings
2. **Simplicity**: `IfAnd`、`IfOr` can be implemented with function composition
3. **Complete Flexibility**: Users can implement any complex logic themselves

## Integration with Existing Systems

### Observability

```go
// Graph automatically records execution traces
g.SetTracer(observability.NewLogTracer(cfg))
```

### Rate Limiting

```go
// Graph supports rate limiting
limiter := ratelimit.NewTokenBucketLimiter(config)
g.SetLimiter(limiter)
```

### Agent/Tool Integration

```go
// Wrap existing Agent
g.Node("agent", graph.NewAgentNode(agent))

// Wrap existing Tool
g.Node("tool", graph.NewToolNode(tool))
```

## Performance Features

- ✅ **BFS Execution** - Breadth-first traversal of DAG
- ✅ **readySet Optimization** - Avoid duplicate node pushes
- ✅ **Context Propagation** - Supports cancellation and timeout
- ✅ **Observability** - Automatically records execution time

## Best Practices

1. **Use conditional branching** for error handling scenarios
2. **Choose scheduler based on use case**
3. **Use unified "node." prefix** for result storage
4. **Set reasonable timeout**
5. **Leverage observability for debugging**

## Examples

See `examples/graph_demo/` directory for complete examples:

- `basic_example.go` - Basic usage
- `conditional_example.go` - Conditional branching
- `scheduler_example.go` - Scheduler usage
- `config.yaml` - Configuration file

## Version

**Current Version**: v1.0  
**Code Size**: ~1,500 lines (including tests)  
**Test Coverage**: 82.3%
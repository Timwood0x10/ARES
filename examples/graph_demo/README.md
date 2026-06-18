# Graph System Examples

This directory contains comprehensive examples demonstrating the capabilities of the GoAgent graph workflow system.

## Overview

The graph system provides a powerful way to orchestrate complex workflows using nodes and edges with conditional branching, multiple scheduling strategies, observability, and rate limiting.

## Examples

### 1. Basic Example (`basic/`)

Demonstrates the fundamental concepts of the graph system:
- Creating a simple linear workflow
- Using function nodes
- Sequential execution
- State management

**Run it:**
```bash
cd basic && go run *.go
```

**Output:**
```
Executing step1
Executing step2
Executing step3
Graph ID: basic-example
Duration: 582.75µs
```

### 2. Agent Integration (`agent/`)

Shows how to integrate agents into the graph workflow:
- Creating mock agents that implement the `base.Agent` interface
- Using `AgentNode` to wrap agents
- Passing data between nodes
- Agent-based processing pipeline

**Run it:**
```bash
cd agent && go run agent_example.go
```

**Features:**
- Multiple agents (collector, analyzer, aggregator)
- Data transformation through agent pipeline
- State preservation across agent executions

### 3. Conditional Branching (`conditional/`)

Demonstrates conditional execution based on state:
- Using `IfFunc` to create conditional edges
- Multiple execution paths
- Dynamic routing based on state values

**Run it:**
```bash
cd conditional && go run conditional_example.go
```

**Features:**
- Status checking with conditional routing
- Multiple handlers (success, error, fallback)
- State-driven execution flow

### 4. Scheduler Examples (`scheduler/`)

Shows different scheduling strategies:
- **FIFO Scheduler**: First-in-first-out execution
- **Priority Scheduler**: Priority-based execution
- **Short Job First**: SJF scheduling for optimal performance

**Run it:**
```bash
cd scheduler && go run *.go
```

**Features:**
- Custom scheduler implementation
- Performance comparison
- Flexible scheduling strategies

### 6. ReplaceNode — Dynamic Step Replacement (`replace_node/`)

Demonstrates `MutableDAG.ReplaceNode` for atomic in-graph step replacement:
- Replacing a node with a new ID (edge migration, downstream `DependsOn` update)
- In-place replacement (same ID, new dependencies added)
- Cycle safety: `ReplaceNode` simulates the post-replacement graph before mutating
- Graph events published for each replacement

**Run it:**
```bash
cd replace_node && go run replace_node_example.go
```

**Output:**
```
=== Before ReplaceNode ===
Execution order: [s1 s2 s3]
=== After ReplaceNode ===
Execution order: [s1 s2_v2 s3]
  Step "s1" (name="Fetch Data", dependsOn=[])
  Step "s2_v2" (name="Transform v2", dependsOn=["s1"])
  Step "s3" (name="Load", dependsOn=["s2_v2"])
...
```

### 7. Node-Level Recovery (`recovery/`)

Shows automated recovery from step failure using `StepRecoveryHandler`:
- A failing step with a `RecoveryPolicy` (strategy: `replace_node`)
- Custom `StepRecoveryHandler` that returns a replacement step
- Recovery events (`EventStepFailed`, `EventStepRecoveryStarted`, `EventStepRecoveryCompleted`)
- Workflow continues seamlessly after replacement

**Run it:**
```bash
cd recovery && go run recovery_example.go
```

**Output:**
```
Recovery triggered for step "s1" (error: step failed)
Workflow status: completed
Steps executed: 2
  Step "s1": status=failed output=""
  Step "s1_recovery": status=completed output="success: retry_s1"
  Step "s2": status=completed output="success: process"
Recovery events: [step.failed step.recovery.started step.recovery.completed]
```

### 8. GraphEventHub Subscription (`subscribe/`)

Illustrates subscribing to the `GraphEventHub` for real-time graph mutation visibility:
- Add/remove nodes and edges while an event subscriber prints each change
- Shows all five event types: `ChangeAddNode`, `ChangeRemoveNode`, `ChangeAddEdge`, `ChangeRemoveEdge`, `ChangeReplaceNode`
- Non-blocking publish with buffered channels

**Run it:**
```bash
cd subscribe && go run subscribe_example.go
```

**Output (approx):**
```
[EVENT] AddNode node="s1" ...
[EVENT] AddNode node="s2" ...
[EVENT] AddEdge node="" from="s1" to="s2" ...
[EVENT] ReplaceNode node="s2_v2" oldNode="s2" ...
...
```

### 9. ApplyMode Comparison (`applymode/`)

Compares `ApplyAtCheckpoint` vs `ApplyImmediate` modes in `DynamicExecutor`:
- `ApplyAtCheckpoint`: DAG mutations are picked up after each step completes
- `ApplyImmediate`: DAG mutations are picked up before each step starts
- Both modes handle dynamically added steps during workflow execution

**Run it:**
```bash
cd applymode && go run applymode_example.go
```

### 10. Real-World Integration (`integration/`)

A complete customer support ticket processing system:
- Ticket validation
- Agent-based classification (billing, account, technical)
- Priority analysis
- Dynamic routing to appropriate teams

**Run it:**
```bash
cd integration && go run integration_example.go
```

**Features:**
- Multi-agent workflow
- Complex business logic
- State transformation across nodes
- Real-world use case demonstration

## Core Concepts

### Nodes

The graph system supports three types of nodes:

1. **FuncNode**: Wraps a simple function
```go
wfgraph.NewFuncNode("node_id", func(ctx context.Context, state *wfgraph.State) error {
    // Your logic here
    return nil
})
```

2. **AgentNode**: Wraps an agent implementing `base.Agent`
```go
wfgraph.NewAgentNode(yourAgent)
```

3. **ToolNode**: Wraps a tool implementing `core.Tool`
```go
wfgraph.NewToolNode(yourTool)
```

### State Management

State is shared across nodes and allows data flow:
```go
// Set value
state.Set("key", value)

// Get value
value, exists := state.Get("key")
```

### Conditional Edges

Create conditional execution paths:
```go
wfgraph.Edge("from_node", "to_node", wfgraph.IfFunc(func(s *wfgraph.State) bool {
    // Return true to traverse this edge
    return someCondition
}))
```

### Graph Configuration

```go
service, err := graph.NewService(&graph.Config{
    RequestTimeout: 30 * time.Second,
    Tracer:         observability.NewLogTracer(nil),
})
```

## Building a Graph

```go
g := wfgraph.NewGraph("graph-id").
    Node("node1", wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
        // Node logic
        return nil
    })).
    Node("node2", wfgraph.NewAgentNode(agent)).
    Edge("node1", "node2").
    Start("node1")
```

## Execution

```go
request := &graph.ExecuteRequest{
    GraphID: "graph-id",
    State: map[string]any{
        "input": "your input data",
    },
}

response, err := service.Execute(context.Background(), g, request)
```

## Testing

Run all graph tests:
```bash
cd /Users/scc/go/src/goagentx
go test ./internal/workflow/graph/... ./api/service/graph/... -v
```

## Features

- ✅ **DAG Execution**: Ensures no cycles and proper topological ordering
- ✅ **Conditional Branching**: Dynamic routing based on state
- ✅ **Multiple Schedulers**: FIFO, Priority, SJF scheduling strategies
- ✅ **Observability**: Built-in tracing and logging
- ✅ **Rate Limiting**: Control execution throughput
- ✅ **Error Handling**: Graceful error propagation
- ✅ **Timeout Support**: Configurable timeouts for graph execution
- ✅ **State Management**: Share data across nodes
- ✅ **Agent Integration**: Seamless agent workflow integration

## Performance

The graph system is optimized for performance:
- Sub-millisecond execution for simple workflows
- Efficient memory usage with shared state
- Parallel execution support through custom schedulers
- Low overhead observability integration

## Best Practices

1. **Keep nodes focused**: Each node should do one thing well
2. **Use meaningful IDs**: Node and graph IDs should be descriptive
3. **Handle errors gracefully**: Always check and return errors
4. **Document state changes**: Comment state modifications
5. **Test workflows**: Write tests for complex graph structures
6. **Use appropriate schedulers**: Choose schedulers based on use case
7. **Add observability**: Enable tracing for production workflows

## Advanced Usage

### Custom Scheduler

```go
type MyScheduler struct{}

func (s *MyScheduler) Schedule(readyNodes []string) string {
    // Your scheduling logic
    return nextNode
}

g.SetScheduler(&MyScheduler{})
```

### Rate Limiting

```go
limiter := ratelimit.NewTokenBucket(10, time.Second)
g.SetLimiter(limiter)
```

### Custom Tracer

```go
tracer := &MyCustomTracer{}
g.SetTracer(tracer)
```

## Troubleshooting

### Common Issues

1. **"node not found"**: Ensure all referenced nodes are added to the graph
2. **"cycle detected"**: Check for circular dependencies in edges
3. **"timeout"**: Increase RequestTimeout or optimize node execution
4. **"no start node"**: Call `.Start("node_id")` to set the starting node

### Debug Tips

- Enable detailed logging with `observability.NewLogTracer(nil)`
- Use `state.Set("debug", true)` to enable debug mode in nodes
- Check graph structure with `service.GetGraphInfo(g)`
- Print state at each node for debugging

## Contributing

When adding new examples:
1. Keep them focused and educational
2. Add comprehensive comments
3. Test thoroughly
4. Update this README
5. Follow existing code style

## License

See the main project LICENSE file.
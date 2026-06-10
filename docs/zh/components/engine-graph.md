# Graph - 动态 Agent 编排

**GoAgent Graph** 是一个轻量级的动态 Agent 编排系统，作为 Workflow Engine 的可选插件存在。

## 概述

Graph 提供了基于 Go 代码的动态 DAG 执行能力，支持条件分支和可插拔调度器。它与现有的 Workflow Engine 平行存在，不依赖 Workflow Engine 的静态 DAG 执行。

### 核心特性

- ✅ **动态决策** - 支持运行时条件分支
- ✅ **可插拔调度器** - 默认 FIFO，可选优先级、短任务优先
- ✅ **零侵入** - 包装现有 Agent/Tool 接口
- ✅ **生产级** - 自动获得 observability、ratelimit 集成
- ✅ **极简** - 核心代码约 120 行

### 与 Workflow Engine 的对比

| 维度 | Workflow Engine | Graph |
|------|----------------|-------|
| **定义方式** | YAML/JSON 配置 | Go 代码 |
| **执行单元** | Step (Agent) | Node (Tool/Agent) |
| **流程类型** | 静态，预定义 | 动态，运行时决策 |
| **适用场景** | 预定义工作流 | Tool 组合、条件分支 |
| **调度策略** | 依赖驱动 + FIFO | 可插拔调度器 |

## 核心概念

### State - 共享状态

Graph 运行时的共享状态管理：

```go
type State struct {
    values map[string]any
}

func NewState() *State
func (s *State) Get(key string) (any, bool)
func (s *State) Set(key string, val any)
func (s *State) ToParams() map[string]any
```

**设计要点**：
- 无锁设计（单线程执行）
- 统一的 "node." 前缀存储节点结果
- 便于调试和状态追踪

### Node - 节点

统一的节点接口，包装现有 Agent/Tool/Func：

```go
type Node interface {
    Execute(ctx context.Context, state *State) error
    ID() string
}

// 三种节点类型：
// 1. AgentNode - 包装现有 Agent
AgentNode(agent base.Agent)

// 2. ToolNode - 包装现有 Tool
ToolNode(tool core.Tool)

// 3. FuncNode - 支持简单函数
FuncNode(id string, fn func(context.Context, *State) error)
```

### Edge - 边（条件分支）

支持条件分支的边定义：

```go
type Edge struct {
    from string
    to   string
    cond Condition
}

type Condition func(state *State) bool

// 便捷条件构造器
func IfFunc(fn func(state *State) bool) Condition
```

**使用示例**：
```go
g.Edge("check", "success", graph.IfFunc(func(s *State) bool {
    val, _ := s.Get("status")
    return val == "ok"
}))
```

### Graph - 图

Graph 的定义和构建器：

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

// 构建器模式
g := graph.NewGraph("my-graph").
    Node("node1", graph.NewFuncNode("node1", ...)).
    Node("node2", graph.NewToolNode(tool)).
    Edge("node1", "node2").
    Start("node1")
```

### Scheduler - 调度器

可插拔的调度器接口：

```go
type Scheduler interface {
    Select(ready []string) string
}

// 三种调度器：
// 1. DefaultScheduler - FIFO（默认）
DefaultScheduler{}

// 2. PriorityScheduler - 优先级调度
PriorityScheduler(priorities map[string]int)

// 3. ShortJobScheduler - 短任务优先
ShortJobScheduler(estimates map[string]int)
```

## 使用示例

### 基础用法

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

### 条件分支

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

### 自定义调度器

```go
// 使用优先级调度
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

### API 层使用

```go
import "goagent/api/service/graph"
import wfgraph "goagent/internal/workflow/graph"

// 创建服务
service, _ := graph.NewService(&graph.Config{
    RequestTimeout: 30 * time.Second,
    Tracer:        observability.NewLogTracer(nil),
})

// 执行图
request := &graph.ExecuteRequest{
    GraphID: "my-graph",
    State: map[string]any{"input": "test"},
}

response, err := service.Execute(context.Background(), g, request)
```

## 目录结构

```
internal/workflow/graph/
├── graph.go          # Graph 定义 + Edge
├── node.go           # Node 包装器
├── state.go          # State 管理
├── scheduler.go      # 可插拔调度器
└── executor.go      # 执行引擎

api/service/graph/
├── service.go        # Graph Service
└── service_test.go   # Service 测试

examples/graph_demo/
├── basic_example.go
├── conditional_example.go
├── scheduler_example.go
└── config.yaml
```

## API 参考

### 构造函数

```go
// 创建新图
func NewGraph(id string) *Graph

// 带自定义 tracer
func NewGraphWithTracer(id string, tracer observability.Tracer) *Graph

// 带自定义 rate limiter
func NewGraphWithLimiter(id string, limiter ratelimit.Limiter) *Graph
```

### Builder 方法

```go
// 添加节点
func (g *Graph) Node(id string, node Node) *Graph

// 添加边
func (g *Graph) Edge(from, to string, cond ...Condition) *Graph

// 设置起始节点
func (g *Graph) Start(id string) *Graph

// 设置调度器
func (g *Graph) SetScheduler(scheduler Scheduler) *Graph

// 设置 tracer
func (g *Graph) SetTracer(tracer observability.Tracer) *Graph

// 设置 limiter
func (g *Graph) SetLimiter(limiter ratelimit.Limiter) *Graph
```

### Service API

```go
// 创建服务
func NewService(config *Config) (*Service, error)

// 执行图
func (s *Service) Execute(ctx context.Context, g *wfgraph.Graph, request *ExecuteRequest) (*ExecuteResponse, error)

// 使用 builder 函数执行
func (s *Service) ExecuteWithGraphBuilder(ctx context.Context, graphID string, builder func(*wfgraph.Graph) *wfgraph.Graph, request *ExecuteRequest) (*ExecuteResponse, error)

// 验证图
func (s *Service) ValidateGraph(g *wfgraph.Graph) error

// 获取图信息
func (s *Service) GetGraphInfo(g *wfgraph.Graph) *GraphInfo
```

## 设计决策

### 为什么不依赖 Workflow Engine？

1. **静态 vs 动态**：Workflow Engine 是静态 DAG，无法支持动态条件分支
2. **条件分支丢失**：转换为 Workflow 会丢失 `Edge.Condition` 信息
3. **调度策略不同**：Graph 需要支持运行时调度决策

### 为什么 State 无锁？

1. **单线程执行**：Graph execution 是单线程（除非支持并行节点）
2. **简化复杂度**：锁是多余的复杂度
3. **性能考虑**：避免锁竞争

### 为什么只用 IfFunc？

1. **灵活性**：`If("score", ">80")` 无法实现，因为 `expected any` 无法比较字符串
2. **简洁性**：`IfAnd`、`IfOr` 可以用函数组合实现
3. **完全灵活**：用户可以自己实现任意复杂逻辑

## 集成现有系统

### Observability

```go
// Graph 自动记录执行追踪
g.SetTracer(observability.NewLogTracer(cfg))
```

### Rate Limiting

```go
// Graph 支持速率限制
limiter := ratelimit.NewTokenBucketLimiter(config)
g.SetLimiter(limiter)
```

### Agent/Tool 集成

```go
// 包装现有 Agent
g.Node("agent", graph.NewAgentNode(agent))

// 包装现有 Tool
g.Node("tool", graph.NewToolNode(tool))
```

## 性能特性

- ✅ **BFS 执行** - 广度优先遍历 DAG
- ✅ **readySet 优化** - 避免节点重复 push
- ✅ **Context 传播** - 支持取消和超时
- ✅ **可观测性** - 自动记录执行时间

## 最佳实践

1. **使用条件分支**处理错误场景
2. **根据场景选择调度器**
3. **使用统一的 "node." 前缀**存储结果
4. **合理设置超时时间**
5. **利用 observability 进行调试**

## 示例

查看 `examples/graph_demo/` 目录获取完整示例：

- `basic_example.go` - 基础用法
- `conditional_example.go` - 条件分支
- `scheduler_example.go` - 调度器使用
- `config.yaml` - 配置文件

## 版本

**当前版本**：v1.0  
**代码量**：~1,500 行（含测试）  
**测试覆盖率**：82.3%
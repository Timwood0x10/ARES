# Workflow Engine 设计文档

## 1. 概述

Workflow Engine 负责加载和执行用户定义的工作流，实现基于 DAG 的任务编排。用户通过 YAML/JSON 文件定义工作流，Engine 自动解析依赖关系并执行。

## 2. 工作流定义

### 2.1 基本结构

```yaml
# workflow.yaml
id: "workflow-001"
name: "穿搭推荐流程"
version: "1.0.0"
description: "默认的时尚穿搭推荐工作流"

variables:
  api_key: "${API_KEY}"

steps:
  - id: leader
    name: "Leader Agent"
    agent_type: "leader"
    input: "{{.input}}"
    
  - id: agent_top
    name: "上衣推荐"
    agent_type: "sub"
    input: "{{.input}}"
    depends_on: [leader]
    timeout: 60s
    retry_policy:
      max_attempts: 3
      initial_delay: 1s
      max_delay: 10s
      backoff_multiplier: 2.0
      
  - id: agent_bottom
    name: "下装推荐"
    agent_type: "sub"
    input: "{{.input}}"
    depends_on: [leader]
    
  - id: agent_shoes
    name: "鞋子推荐"
    agent_type: "sub"
    input: "{{.agent_top}} + {{.input}}"
    depends_on: [leader, agent_top]
```

### 2.2 字段说明

| 字段 | 必填 | 说明 |
|------|------|------|
| id | 是 | 工作流唯一标识 |
| name | 是 | 工作流名称 |
| version | 否 | 版本号 |
| description | 否 | 描述 |
| steps | 是 | 步骤列表 |
| variables | 否 | 变量映射 |
| metadata | 否 | 元数据 |

### 2.3 Step 字段说明

| 字段 | 必填 | 说明 |
|------|------|------|
| id | 是 | 步骤唯一标识 |
| name | 否 | 步骤名称 |
| agent_type | 是 | Agent 类型 |
| input | 否 | 输入模板 |
| depends_on | 否 | 依赖步骤 ID 列表 |
| timeout | 否 | 超时时间 |
| retry_policy | 否 | 重试策略 |

## 3. 核心类型

### 3.1 Workflow

```go
type Workflow struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Version     string            `json:"version"`
    Description string            `json:"description"`
    Steps       []*Step           `json:"steps"`
    Variables   map[string]string `json:"variables"`
    Metadata    map[string]string `json:"metadata"`
}
```

### 3.2 Step

```go
type Step struct {
    ID          string        `json:"id"`
    Name        string        `json:"name"`
    AgentType   string        `json:"agent_type"`
    Input       string        `json:"input"`
    DependsOn   []string      `json:"depends_on"`
    Timeout     time.Duration `json:"timeout"`
    RetryPolicy *RetryPolicy  `json:"retry_policy,omitempty"`
}
```

### 3.3 RetryPolicy

```go
type RetryPolicy struct {
    MaxAttempts       int
    InitialDelay      time.Duration
    MaxDelay          time.Duration
    BackoffMultiplier float64
}
```

## 4. DAG 执行

### 4.1 自动拓扑排序

Engine 自动分析 `depends_on` 依赖，构建 DAG 并执行拓扑排序。

```
步骤依赖图:
  leader ──┬── agent_top ── agent_shoes
           │
           └── agent_bottom

执行顺序: leader → [agent_top, agent_bottom] → agent_shoes
```

### 4.2 并行执行

无依赖或依赖已完成的步骤可并行执行：

```go
// 最大并行数控制
maxParallel := 4
```

## 5. 核心模块

### 5.1 Loader

```go
// WorkflowLoader 加载工作流
type WorkflowLoader interface {
    Load(ctx context.Context, source string) (*Workflow, error)
}

// FileLoader 从文件加载
type FileLoader struct {
    decoder Decoder
}

// 支持 JSON 和 YAML
func NewJSONFileLoader() *FileLoader
func NewYAMLFileLoader() *FileLoader

// DirectoryLoader 从目录加载多个工作流
type DirectoryLoader struct {
    fileLoader *FileLoader
}
func (l *DirectoryLoader) LoadAll(ctx context.Context, dir string) (map[string]*Workflow, error)
```

### 5.2 Executor

```go
// Executor 执行工作流
type Executor struct {
    registry    *AgentRegistry
    outputStore *OutputStore
    maxParallel int
    stepTimeout time.Duration
}

func NewExecutor(registry *AgentRegistry) *Executor

// Execute 执行工作流
func (e *Executor) Execute(ctx context.Context, workflow *Workflow, initialInput string) (*WorkflowResult, error)

// WorkflowResult 执行结果
type WorkflowResult struct {
    ExecutionID string                 `json:"execution_id"`
    WorkflowID  string                 `json:"workflow_id"`
    Status      WorkflowStatus         `json:"status"`
    Output      map[string]interface{} `json:"output"`
    Error       string                 `json:"error,omitempty"`
    Duration    time.Duration          `json:"duration"`
    Steps       []*StepResult          `json:"steps"`
}
```

### 5.3 AgentRegistry

```go
// AgentRegistry 管理 Agent 工厂
type AgentRegistry struct {
    factories map[string]AgentFactory
}

// AgentFactory 创建 Agent 实例
type AgentFactory func(ctx context.Context, config interface{}) (base.Agent, error)

// 注册 Agent 类型
func (r *AgentRegistry) Register(agentType string, factory AgentFactory) error

// 创建 Agent 实例
func (r *AgentRegistry) CreateAgent(ctx context.Context, agentType string, config interface{}) (base.Agent, error)

// AgentExecutor 执行步骤
type AgentExecutor struct {
    registry *AgentRegistry
}
func (e *AgentExecutor) Execute(ctx context.Context, step *Step, input string, taskCtx *models.TaskContext) (string, error)
```

### 5.4 OutputStore

```go
// OutputStore 存储步骤输出
type OutputStore struct {
    outputs map[string]*StepOutput
}

func (s *OutputStore) Set(stepID string, output *StepOutput)
func (s *OutputStore) Get(stepID string) (*StepOutput, bool)
func (s *OutputStore) GetMultiple(stepIDs []string) map[string]*StepOutput
func (s *OutputStore) Clear()
```

## 6. 模板变量

步骤 Input 支持模板变量：

| 变量 | 说明 |
|------|------|
| `{{.input}}` | 初始输入 |
| `{{.step_id}}` | 指定步骤的输出 |

```yaml
steps:
  - id: summary
    agent_type: "sub"
    input: "基于 {{.agent_top}} 和 {{.agent_bottom}} 进行总结"
    depends_on: [agent_top, agent_bottom]
```

## 7. 热加载

```go
// HotReloader 热加载工作流
type HotReloader struct {
    watcher    *fsnotify.Watcher
    registry   *AgentRegistry
    workflows  map[string]*Workflow
    onChange   func(workflow *Workflow)
}

func NewHotReloader(registry *AgentRegistry) *HotReloader
func (r *HotReloader) AddWorkflow(path string) error
func (r *HotReloader) Start(ctx context.Context) error
func (r *HotReloader) Stop() error
```

## 8. 执行状态

```go
// WorkflowStatus 工作流状态
const (
    WorkflowStatusPending   WorkflowStatus = "pending"
    WorkflowStatusRunning   WorkflowStatus = "running"
    WorkflowStatusCompleted WorkflowStatus = "completed"
    WorkflowStatusFailed    WorkflowStatus = "failed"
    WorkflowStatusCancelled WorkflowStatus = "cancelled"
)

// StepStatus 步骤状态
const (
    StepStatusPending   StepStatus = "pending"
    StepStatusRunning   StepStatus = "running"
    StepStatusCompleted StepStatus = "completed"
    StepStatusFailed    StepStatus = "failed"
    StepStatusSkipped   StepStatus = "skipped"
)
```

## 9. 使用示例

```go
// 创建 Registry 并注册 Agent
registry := engine.NewAgentRegistry()
registry.Register("leader", func(ctx context.Context, cfg interface{}) (base.Agent, error) {
    return leader.New(...), nil
})
registry.Register("sub", func(ctx context.Context, cfg interface{}) (base.Agent, error) {
    return sub.New(...), nil
})

// 创建 Executor
executor := engine.NewExecutor(registry)

// 加载工作流
loader := engine.NewYAMLFileLoader()
workflow, err := loader.Load(ctx, "workflows/default.yaml")

// 执行
result, err := executor.Execute(ctx, workflow, "用户输入")
```

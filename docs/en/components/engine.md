# Workflow Engine Design Document

## 1. Overview

The Workflow Engine handles loading and executing user-defined workflows, implementing DAG-based task orchestration. Users define workflows through YAML/JSON files, and the engine automatically parses dependencies and executes tasks.

## 2. Workflow Definition

### 2.1 Basic Structure

```yaml
# workflow.yaml
id: "workflow-001"
name: "Fashion Recommendation Flow"
version: "1.0.0"
description: "Default fashion recommendation workflow"

variables:
  api_key: "${API_KEY}"

steps:
  - id: leader
    name: "Leader Agent"
    agent_type: "leader"
    input: "{{.input}}"
    
  - id: agent_top
    name: "Top Recommendation"
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
    name: "Bottom Recommendation"
    agent_type: "sub"
    input: "{{.input}}"
    depends_on: [leader]
    
  - id: agent_shoes
    name: "Shoes Recommendation"
    agent_type: "sub"
    input: "{{.agent_top}} + {{.input}}"
    depends_on: [leader, agent_top]
```

### 2.2 Field Description

| Field | Required | Description |
|-------|----------|-------------|
| id | Yes | Workflow unique ID |
| name | Yes | Workflow name |
| version | No | Version number |
| description | No | Description |
| steps | Yes | Step list |
| variables | No | Variable mappings |
| metadata | No | Metadata |

### 2.3 Step Fields

| Field | Required | Description |
|-------|----------|-------------|
| id | Yes | Step unique ID |
| name | No | Step name |
| agent_type | Yes | Agent type |
| input | No | Input template |
| depends_on | No | Dependent step IDs |
| timeout | No | Timeout duration |
| retry_policy | No | Retry policy |

## 3. Core Types

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

## 4. DAG Execution

### 4.1 Automatic Topological Sort

The engine automatically analyzes `depends_on` dependencies, builds a DAG, and executes topological sort.

```
Dependency Graph:
  leader ──┬── agent_top ── agent_shoes
           │
           └── agent_bottom

Execution Order: leader → [agent_top, agent_bottom] → agent_shoes
```

### 4.2 Parallel Execution

Steps without dependencies or with completed dependencies can execute in parallel:

```go
// Max parallel control
maxParallel := 4
```

## 5. Core Modules

### 5.1 Loader

```go
// WorkflowLoader loads workflows
type WorkflowLoader interface {
    Load(ctx context.Context, source string) (*Workflow, error)
}

// FileLoader loads from files
type FileLoader struct {
    decoder Decoder
}

// Supports JSON and YAML
func NewJSONFileLoader() *FileLoader
func NewYAMLFileLoader() *FileLoader

// DirectoryLoader loads multiple workflows from directory
type DirectoryLoader struct {
    fileLoader *FileLoader
}
func (l *DirectoryLoader) LoadAll(ctx context.Context, dir string) (map[string]*Workflow, error)
```

### 5.2 Executor

```go
// Executor executes workflows
type Executor struct {
    registry    *AgentRegistry
    outputStore *OutputStore
    maxParallel int
    stepTimeout time.Duration
}

func NewExecutor(registry *AgentRegistry) *Executor

// Execute executes a workflow
func (e *Executor) Execute(ctx context.Context, workflow *Workflow, initialInput string) (*WorkflowResult, error)

// WorkflowResult execution result
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
// AgentRegistry manages agent factories
type AgentRegistry struct {
    factories map[string]AgentFactory
}

// AgentFactory creates agent instances
type AgentFactory func(ctx context.Context, config interface{}) (base.Agent, error)

// Register agent type
func (r *AgentRegistry) Register(agentType string, factory AgentFactory) error

// Create agent instance
func (r *AgentRegistry) CreateAgent(ctx context.Context, agentType string, config interface{}) (base.Agent, error)

// AgentExecutor executes steps
type AgentExecutor struct {
    registry *AgentRegistry
}
func (e *AgentExecutor) Execute(ctx context.Context, step *Step, input string, taskCtx *models.TaskContext) (string, error)
```

### 5.4 OutputStore

```go
// OutputStore stores step outputs
type OutputStore struct {
    outputs map[string]*StepOutput
}

func (s *OutputStore) Set(stepID string, output *StepOutput)
func (s *OutputStore) Get(stepID string) (*StepOutput, bool)
func (s *OutputStore) GetMultiple(stepIDs []string) map[string]*StepOutput
func (s *OutputStore) Clear()
```

## 6. Template Variables

Step Input supports template variables:

| Variable | Description |
|----------|-------------|
| `{{.input}}` | Initial input |
| `{{.step_id}}` | Output of specified step |

```yaml
steps:
  - id: summary
    agent_type: "sub"
    input: "Based on {{.agent_top}} and {{.agent_bottom}} summarize"
    depends_on: [agent_top, agent_bottom]
```

## 7. Hot Reload

```go
// HotReloader hot reloads workflows
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

## 8. Execution Status

```go
// WorkflowStatus workflow status
const (
    WorkflowStatusPending   WorkflowStatus = "pending"
    WorkflowStatusRunning   WorkflowStatus = "running"
    WorkflowStatusCompleted WorkflowStatus = "completed"
    WorkflowStatusFailed    WorkflowStatus = "failed"
    WorkflowStatusCancelled WorkflowStatus = "cancelled"
)

// StepStatus step status
const (
    StepStatusPending   StepStatus = "pending"
    StepStatusRunning   StepStatus = "running"
    StepStatusCompleted StepStatus = "completed"
    StepStatusFailed    StepStatus = "failed"
    StepStatusSkipped   StepStatus = "skipped"
)
```

## 9. Usage Example

```go
// Create Registry and register Agents
registry := engine.NewAgentRegistry()
registry.Register("leader", func(ctx context.Context, cfg interface{}) (base.Agent, error) {
    return leader.New(...), nil
})
registry.Register("sub", func(ctx context.Context, cfg interface{}) (base.Agent, error) {
    return sub.New(...), nil
})

// Create Executor
executor := engine.NewExecutor(registry)

// Load workflow
loader := engine.NewYAMLFileLoader()
workflow, err := loader.Load(ctx, "workflows/default.yaml")

// Execute
result, err := executor.Execute(ctx, workflow, "User input")
```

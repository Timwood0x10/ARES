# Client Design Document

## 1. Overview

The `api/client/` package provides a library-style embedding interface that allows Go applications to embed GoAgent as a library rather than running it as a standalone service. Behind a unified `Client` struct, it wraps 5 core services: Agent, Memory, Retrieval, LLM, and Workflow.

### Core Philosophy

- **Library Embedding**: Embed GoAgent directly into your Go application, no separate server process required
- **Modular Services**: Each service (Agent, Memory, Retrieval, LLM, Workflow) is independently configurable
- **Graceful Lifecycle**: Centralised `NewClient` -> accessors -> `Close` pattern with idempotent shutdown
- **Configurable with Fallbacks**: Sensible defaults for timeouts, retries, and retry delays
- **Simple & Flexible APIs**: `SimpleClient` for quick tasks, `WorkflowClient` for orchestration

## 2. Core Types

### 2.1 Client Struct

```go
type Client struct {
    config          *Config
    agentService    *agentSvc.Service
    memoryService   *memoryservice.Service
    retrievalService *retrievalservice.Service
    llmService      *llmservice.Service
    workflowService *workflowSvc.Service
    configFile      *ConfigFile
    mu              sync.RWMutex
    closed          bool
}
```

### 2.2 Config Struct

```go
type Config struct {
    BaseConfig *core.BaseConfig
    Agent      *agentSvc.Config
    Memory     *memoryservice.Config
    Retrieval  *retrievalservice.Config
    LLM        *llmservice.Config
    Workflow   *workflowSvc.Config
}
```

### 2.3 BaseConfig Struct

```go
type BaseConfig struct {
    RequestTimeout time.Duration // default 30s
    MaxRetries     int           // default 3
    RetryDelay     time.Duration // default 1s
}
```

## 3. Client API

### 3.1 Constructor Functions

| Function | Description |
|----------|-------------|
| `NewClient(config *Config)` | Nil-safe constructor; applies defaults (30s timeout, 3 retries, 1s delay) when `BaseConfig` is nil. Initialises each service whose config is non-nil. |
| `NewClientWithConfigFile(config *Config, configFile *ConfigFile)` | Constructor that attaches the raw YAML `ConfigFile` alongside the client `Config`. Use when server-level settings are needed. |

### 3.2 Service Accessors

Each accessor returns the corresponding service or a specific sentinel error if the service was not configured at creation time.

| Method | Return Type | Error if Unconfigured |
|--------|-------------|-----------------------|
| `Agent()` | `*agentSvc.Service` | `ErrAgentNotConfigured` |
| `Memory()` | `*memoryservice.Service` | `ErrMemoryNotConfigured` |
| `Retrieval()` | `*retrievalservice.Service` | `ErrRetrievalNotConfigured` |
| `LLM()` | `*llmservice.Service` | `ErrLLMNotConfigured` |
| `Workflow()` | `*workflowSvc.Service` | `ErrWorkflowNotConfigured` |

### 3.3 Lifecycle & Utility Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `Runtime` | `(config *runtimeSvc.Config, eventStore ares_events.EventStore) (*runtimeSvc.Service, error)` | Creates a runtime service for agent lifecycle management. Uses defaults if config is nil; in-memory store if eventStore is nil. |
| `Health` | `(ctx context.Context) (*HealthReport, error)` | Probes every configured service for availability and latency. |
| `Config` | `() *Config` | Returns a deep copy of the client configuration (RWMutex protected). |
| `GetConfig` | `() *ConfigFile` | Returns the raw YAML `ConfigFile`, or nil if not loaded via `NewClientWithConfigFile`. |
| `Ping` | `(ctx context.Context) bool` | Lightweight round-trip check; returns true only when all required services are configured and available. |
| `Close` | `(ctx context.Context) error` | Graceful, idempotent shutdown. Safe to call multiple times. |

### 3.4 Constructor Example

```go
client, err := client.NewClient(&client.Config{
    BaseConfig: &core.BaseConfig{
        RequestTimeout: 60 * time.Second,
        MaxRetries:     5,
        RetryDelay:     2 * time.Second,
    },
    Agent: &agentSvc.Config{...},
    LLM:   &llmservice.Config{...},
})
if err != nil {
    log.Fatal(err)
}
defer client.Close(ctx)
```

### 3.5 Health Check Example

```go
report, err := client.Health(ctx)
if err != nil {
    log.Error("health check failed", "error", err)
}
log.Info("LLM available", "latency_ms", report.LLMStatus.Latency.Milliseconds())
if !report.OverallStatus {
    log.Warn("system is degraded")
}
```

## 4. Error Sentinels

The following sentinel errors are defined in `api/client/errors.go`:

| Variable | Value |
|----------|-------|
| `ErrInvalidConfig` | `"invalid config"` |
| `ErrAgentNotConfigured` | `"agent service not configured"` |
| `ErrMemoryNotConfigured` | `"memory service not configured"` |
| `ErrRetrievalNotConfigured` | `"retrieval service not configured"` |
| `ErrLLMNotConfigured` | `"LLM service not configured"` |
| `ErrWorkflowNotConfigured` | `"workflow service not configured"` |

## 5. Health Types

Health types are defined in `api/client/health.go`.

### 5.1 ServiceStatus

```go
type ServiceStatus struct {
    Available bool          `json:"available"`
    Latency   time.Duration `json:"latency_ms"`
    Error     string        `json:"error,omitempty"`
}
```

### 5.2 HealthReport

```go
type HealthReport struct {
    LLMStatus       ServiceStatus `json:"llm"`
    MemoryStatus    ServiceStatus `json:"memory"`
    RetrievalStatus ServiceStatus `json:"retrieval"`
    WorkflowStatus  ServiceStatus `json:"workflow"`
    OverallStatus   bool          `json:"overall_status"`
    Timestamp       time.Time     `json:"timestamp"`
}
```

- `OverallStatus` is `true` only when every configured service reports healthy.

## 6. Configuration File & Loader

Configuration loading is managed through `ConfigFile` and `ConfigLoader` in `api/client/config.go`.

### 6.1 ConfigFile

```go
type ConfigFile struct {
    Server   ServerConfig   `yaml:"server"`
    API      APIConfig      `yaml:"api"`
    LLM      core.LLMConfig `yaml:"llm"`
    Database DatabaseConfig `yaml:"database"`
    Storage  StorageConfig  `yaml:"storage"`
    Memory   MemoryConfig   `yaml:"memory"`
    Agents   AgentsConfig   `yaml:"agents"`
    Prompts  PromptsConfig  `yaml:"prompts"`
    Output   OutputConfig   `yaml:"output"`
}
```

### 6.2 ConfigLoader

```go
type ConfigLoader struct {
    defaultPaths []string
    envPrefix    string
}
```

### 6.3 Loader Functions

| Function | Description |
|----------|-------------|
| `NewConfigLoader(opts ...ConfigLoaderOption)` | Creates a loader with default paths: `./config.yaml`, `./config/server.yaml`, `./examples/simple_newapi/config/server.yaml`. Default env prefix is `GOAGENT`. |
| `Load(path string)` | Loads YAML, applies env override, sets defaults, validates. Returns `(*ConfigFile, error)`. |
| `LoadConfigFile(path)` | **Deprecated.** Convenience function using default loader. |
| `NewClientFromConfigPath(configPath)` | Loads config from path, converts to `Config`, creates `Client`. |
| `NewClientFromDefaultPath()` | Searches default paths and creates a `Client`. |
| `SetAllowedConfigDir(dir)` | Path traversal security guard. |
| `ToClientConfig()` | Converts `ConfigFile` to `Config` with sensible defaults. |

### 6.4 Loader Options

```go
func WithDefaultPaths(paths ...string) ConfigLoaderOption
func WithEnvPrefix(prefix string) ConfigLoaderOption
```

### 6.5 Environment Variable Override

| Environment Variable | Config Field |
|----------------------|--------------|
| `GOAGENT_LLM_API_KEY` / `LLM_API_KEY` | `LLM.APIKey` |
| `GOAGENT_LLM_BASE_URL` / `LLM_BASE_URL` | `LLM.BaseURL` |
| `GOAGENT_LLM_MODEL` / `LLM_MODEL` | `LLM.Model` |
| `GOAGENT_LLM_PROVIDER` / `LLM_PROVIDER` | `LLM.Provider` |
| `GOAGENT_DB_PASSWORD` / `DB_PASSWORD` | `Database.Password` |
| `GOAGENT_DB_HOST` / `DB_HOST` | `Database.Host` |

### 6.6 Default Values

| Field | Default |
|-------|---------|
| `API.RequestTimeout` | 30s |
| `API.MaxRetries` | 3 |
| `API.RetryDelay` | 1s |
| `LLM.Timeout` | 60s |
| `LLM.Provider` | `ollama` |
| `LLM.BaseURL` | Provider-specific: Ollama -> `http://localhost:11434`, OpenRouter -> `https://openrouter.ai/api/v1`, OpenAI -> `https://api.openai.com/v1` |
| `LLM.Model` | Provider-specific: Ollama -> `llama3.2`, OpenRouter -> `meta-llama/llama-3.1-8b-instruct`, OpenAI -> `gpt-4o` |
| `Database.Port` | 5432 |
| `Database.User` | `postgres` |
| `Database.DBName` | `goagent` |
| `Memory.Session.MaxHistory` | 50 |

### 6.7 Validation Rules

| Rule | Constraint |
|------|------------|
| API timeout | 1-300s |
| Max retries | 0-10 |
| Retry delay | 0-60s |
| LLM timeout | 1-600s |
| LLM provider | Must be one of: `ollama`, `openrouter`, `openai`, `anthropic` |
| Database port | 1-65535 (required when enabled) |
| Session max history | 0-1000 |

### 6.8 Config Loading Example

```go
// Minimal setup: search default paths
client, err := client.NewClientFromDefaultPath()
if err != nil {
    log.Fatal(err)
}
defer client.Close(ctx)

// Custom loader with path traversal protection
client.SetAllowedConfigDir("/etc/goagent")
loader := client.NewConfigLoader(
    client.WithDefaultPaths("/etc/goagent/config.yaml"),
    client.WithEnvPrefix("MYAPP"),
)
cfg, err := loader.Load("")
```

## 7. SimpleClient

`SimpleClient` in `api/client/simple.go` provides the simplest possible API for GoAgent.

```go
type SimpleClient struct {
    client *Client
    config *ConfigFile
}
```

### 7.1 SimpleClient Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `NewSimpleClient` | `(configPath string) (*SimpleClient, error)` | Loads config from path and creates underlying client. Use empty string for default paths. |
| `Execute` | `(ctx context.Context, query string) (string, error)` | Executes a user query using prompt construction. |
| `ExecuteWithAgent` | `(ctx context.Context, agentID string, query string) (string, error)` | Executes a query with agent-specific context in the prompt. |
| `Chat` | `(ctx context.Context, messages []*core.Message) (string, error)` | Conducts multi-turn conversation by building conversation history text. |
| `Close` | `(ctx context.Context) error` | Delegates to underlying client's Close. |
| `GetConfig` | `() *ConfigFile` | Returns the loaded configuration. |

### 7.2 SimpleClient Example

```go
// Create client
sc, err := client.NewSimpleClient("config.yaml")
if err != nil {
    log.Fatal(err)
}
defer sc.Close(ctx)

// Execute a query
result, err := sc.Execute(ctx, "Find me some casual shirts for daily commute, budget 500-1000")
if err != nil {
    log.Error(err)
}
fmt.Println(result)

// Multi-turn chat
messages := []*core.Message{
    {Role: core.MessageRoleUser, Content: "I want to buy clothes"},
    {Role: core.MessageRoleAssistant, Content: "What style do you prefer?"},
    {Role: core.MessageRoleUser, Content: "Casual style"},
}
response, err := sc.Chat(ctx, messages)
```

## 8. WorkflowClient

`WorkflowClient` in `api/client/workflow.go` provides workflow orchestration capabilities.

```go
type WorkflowClient struct {
    client   *Client
    executor *engine.Executor
    loader   *engine.FileLoader
    registry *engine.AgentRegistry
}
```

### 8.1 WorkflowClient Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `NewWorkflowClient` | `(client *Client) (*WorkflowClient, error)` | Creates a workflow client with YAML file loader and agent registry. |
| `LoadWorkflow` | `(ctx context.Context, path string) (*engine.Workflow, error)` | Loads a workflow definition from a YAML file. |
| `Execute` | `(ctx context.Context, workflow *engine.Workflow, input string) (*engine.WorkflowResult, error)` | Executes a workflow with input. Registers agents from client configFile if present. |
| `ExecuteFromFile` | `(ctx context.Context, path string, input string) (*engine.WorkflowResult, error)` | Combines LoadWorkflow + Execute. |

### 8.2 WorkflowClient Example

```go
wc, err := client.NewWorkflowClient(client)
if err != nil {
    log.Fatal(err)
}

result, err := wc.ExecuteFromFile(ctx, "workflows/recommend.yaml", "user query here")
if err != nil {
    log.Error(err)
}
log.Info("workflow completed", "result", result)
```

## 9. Architecture Summary

```
+---------------------------------------------------+
|                     Client                         |
|  +----------+ +----------+ +----------+           |
|  |  Agent   | |  Memory  | | Retrieval|           |
|  | Service  | | Service  | | Service  |           |
|  +----------+ +----------+ +----------+           |
|  +----------+ +----------+                         |
|  |   LLM    | | Workflow |                         |
|  | Service  | | Service  |                         |
|  +----------+ +----------+                         |
|  +----------+ +----------+                         |
|  |  Config  | |ConfigFile|                         |
|  +----------+ +----------+                         |
+---------------------------------------------------+
           ^                    ^
           |                    |
  +--------+--------+  +-------+--------+
  |  SimpleClient   |  | WorkflowClient |
  |  (query/chat)   |  | (orchestration)|
  +-----------------+  +----------------+
```

## 10. Extension Guide

### Adding a New Service to Client

1. Add the config pointer field to `Config` (e.g., `NewService *newService.Config`)
2. Add the service pointer field to `Client` (e.g., `newService *newService.Service`)
3. In `NewClient`, initialise the service when its config is non-nil
4. Add a public accessor method (e.g., `NewService() (*newService.Service, error)`)
5. Add the corresponding sentinel error in `errors.go`
6. Integrate into `Health()` and `Close()` lifecycle methods
7. Update this document
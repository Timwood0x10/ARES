# GoAgent API Reference

## Phase 3: Plugin Tool System

### Core Interfaces

#### ToolFactory
```go
// Create custom tool factories for dynamic tool registration
type ToolFactory interface {
    Name() string
    Description() string
    Create(config map[string]interface{}) (Tool, error)
    ValidateConfig(config map[string]interface{}) error
}
```

**Usage:**
```go
// 1. Create registry
registry := core.NewPluginRegistry()

// 2. Register factory
registry.RegisterFactory(myFactory)

// 3. Load from config
configs := []core.PluginConfig{
    {Name: "my-tool", Factory: "my-factory", Enabled: true, Config: {...}},
}
registry.LoadPlugins(configs)

// 4. Get tool
tool, exists := registry.GetTool("my-tool")
```

#### PluginConfig
```go
type PluginConfig struct {
    Name    string
    Factory string
    Enabled bool
    Config  map[string]interface{}
}
```

#### PluginRegistry
```go
registry := core.NewPluginRegistry()
if err := registry.RegisterFactory(factory); err != nil {
    log.Fatalf("failed to register factory: %v", err)
}
registry.LoadPlugins(configs)                // Load plugins from config
registry.GetTool(name)                       // Get a tool by name
registry.ListPlugins()                       // List all loaded plugins
registry.ListFactories()                     // List all factories
```

---

## Phase 4: Evaluation Framework

### Core Types

#### TestCase
```go
type TestCase struct {
    ID             string
    Name           string
    Input          string
    ExpectedOutput string
    ExpectedTools  []string
    Timeout        time.Duration
    Metadata       map[string]interface{}
    Tags           []string
}
```

#### TestResult
```go
type TestResult struct {
    TestCaseID   string
    ActualOutput string
    ToolsUsed    []string
    Duration     time.Duration
    TokensUsed   int
    Error        string
    Metrics      map[string]float64
    Timestamp    time.Time
}
```

### Evaluators

#### Built-in Evaluators
```go
// Exact match
exactEval := eval.NewExactMatchEvaluator()

// Substring match
substringEval := eval.NewSubstringMatchEvaluator()

// Keyword presence
keywordEval := eval.NewKeywordPresenceEvaluator([]string{"AI", "tech"})

// Tool usage
toolEval := eval.NewToolUsageEvaluator()
```

### Test Runner

```go
// 1. Create loader
loader := eval.NewLoader()

// 2. Load test suite
suite, err := loader.Load("test/eval/basic.yaml")

// 3. Create runner with your agent executor
runner := eval.NewAgentTestRunner(myExecutor)

// 4. Run tests
results, err := runner.RunSuite(ctx, suite)
```

### Report Generation

```go
// Generate markdown report
reportGen := eval.NewReportGenerator()
markdown, _ := reportGen.GenerateMarkdown(suite, results, scores)

// Generate JSON for CI
json, _ := reportGen.GenerateJSON(suite, results, scores)

// Save to file
reportGen.SaveReport("report.md", markdown)
```

### Complete Evaluation Pipeline

```go
// One-liner evaluation
results, scores, err := eval.RunEvaluation(
    ctx,
    loader,
    runner,
    evaluator,
    "test/eval/basic.yaml",
)
```

---

## Quick Start Examples

### Example 1: Plugin System
```go
registry := core.NewPluginRegistry()
if err := registry.RegisterFactory(&MyToolFactory{}); err != nil {
    log.Fatalf("failed to register factory: %v", err)
}
registry.LoadPlugins([]core.PluginConfig{
    {Name: "tool1", Factory: "my-factory", Enabled: true},
})
tool, _ := registry.GetTool("tool1")
result, _ := tool.Execute(ctx, params)
```

### Example 2: Evaluation
```go
loader := eval.NewLoader()
suite, _ := loader.Load("test/eval/basic.yaml")
runner := eval.NewAgentTestRunner(myAgent)
results, err := runner.RunSuite(ctx, suite)

evaluator := eval.NewExactMatchEvaluator()
for i, result := range results {
    scores, _ := evaluator.Evaluate(ctx, suite.TestCases[i], result)
    fmt.Printf("Score: %.2f\n", scores[0].Score)
}
```

---

## File Locations

- **Plugin System**: `internal/tools/resources/core/factory.go`
- **Evaluation Types**: `internal/eval/types.go`
- **Evaluators**: `internal/eval/evaluator.go`
- **Test Runner**: `internal/eval/runner.go`, `internal/eval/agent_runner.go`
- **Report Generator**: `internal/eval/report.go`
- **Test Suite Loader**: `internal/eval/loader.go`
- **Example Test Cases**: `test/eval/basic.yaml`, `test/eval/tools.yaml`
- **Integration Example**: `examples/integration_example.go`

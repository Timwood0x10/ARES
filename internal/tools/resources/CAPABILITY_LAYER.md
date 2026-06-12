# Agent Capability Engine (ACE) Integration Guide

## Overview

The Agent Capability Engine (ACE) provides intelligent tool filtering based on task capabilities. It reduces the number of tools presented to the LLM, improving tool selection accuracy and efficiency.

## Architecture

```
                    +----------------------+
                    |        Leader        |
                    |   Task Planning      |
                    +----------+-----------+
                               |
                               v
                 +----------------------------+
                 |   Agent Capability Engine  |
                 |                            |
                 | 1 Capability Detection     |
                 | 2 Tool Filtering           |
                 | 3 Tool Ranking             |
                 +------------+---------------+
                              |
                              v
                     +------------------+
                     |       Tools       |
                     | calculator        |
                     | knowledge_search  |
                     | http_request      |
                     +------------------+
```

## Capabilities

The following capabilities are defined:

| Capability | Description | Example Keywords |
|------------|-------------|------------------|
| `math` | Mathematical calculations | calculate, sum, multiply, divide, number |
| `knowledge` | Knowledge retrieval | what, who, explain, search, find |
| `memory` | Memory access | remember, store, recall, profile, history |
| `text` | Text processing | parse, format, validate, transform |
| `network` | Network requests | api, request, fetch, http, url |
| `time` | Date/time operations | time, date, schedule, deadline |
| `file` | File operations | file, read, write, delete, directory |
| `external` | External system execution | execute, run, command, script |

## Usage

### Basic Setup

```go
import (
    "goagentx/internal/tools/resources"
)

// Create registry and register tools
registry := resources.NewRegistry()
registry.Register(resources.NewCalculator())
registry.Register(resources.NewHTTPRequest())
registry.Register(resources.NewFileTools())

// Create capability engine
engine := resources.NewCapabilityEngine(registry)
```

### Detecting Capabilities

```go
// Detect capabilities from a query
query := "calculate the sum of 1 to 100"
capabilities := engine.Detect(query)
// Returns: [math]
```

### Filtering Tools

```go
// Get tools for a specific capability
mathTools := engine.ToolsFor(resources.CapabilityMath)
// Returns: [calculator]

// Filter by multiple capabilities
tools := engine.Filter([]resources.Capability{
    resources.CapabilityMath,
    resources.CapabilityNetwork,
})
```

### Matching Tools

```go
// Complete workflow: detect capabilities and match tools
query := "fetch data from API and calculate results"
tools := engine.Match(query)
// Returns: [http_request, calculator]
```

### Tool Capability Summary

```go
// Get all available capabilities
capabilities := engine.GetAllCapabilities()

// Get capability summary (capability -> tool count)
summary := engine.GetCapabilitySummary()
// Returns: {math: 1, network: 2, text: 3, ...}
```

## Tool Registration with Capabilities

### Option 1: Using BaseTool with Capabilities

```go
func NewMyTool() *MyTool {
    params := &resources.ParameterSchema{
        Type: "object",
        Properties: map[string]*resources.Parameter{
            "input": {
                Type:        "string",
                Description: "Input parameter",
            },
        },
        Required: []string{"input"},
    }

    return &MyTool{
        BaseTool: resources.NewBaseToolWithCapabilities(
            "my_tool",
            "Tool description",
            resources.CategoryCore,
            []resources.Capability{resources.CapabilityMath, resources.CapabilityText},
            params,
        ),
    }
}
```

### Option 2: Implementing Capabilities() Method

```go
type MyTool struct {
    *resources.BaseTool
}

func (t *MyTool) Capabilities() []resources.Capability {
    return []resources.Capability{
        resources.CapabilityMath,
        resources.CapabilityText,
    }
}
```

## Integration with Agents

### Example: Agent with Capability Filtering

```go
type MyAgent struct {
    name     string
    engine   *resources.CapabilityEngine
    llm      LLMClient
}

func NewMyAgent(name string) (*MyAgent, error) {
    // Create registry and register tools
    registry := resources.NewRegistry()
    tools := []resources.Tool{
        resources.NewCalculator(),
        resources.NewHTTPRequest(),
        resources.NewFileTools(),
        resources.NewJSONTools(),
        resources.NewKnowledgeSearch(nil),
    }

    for _, tool := range tools {
        if err := registry.Register(tool); err != nil {
            return nil, err
        }
    }

    // Create capability engine
    engine := resources.NewCapabilityEngine(registry)

    return &MyAgent{
        name:   name,
        engine: engine,
        llm:    NewLLMClient(),
    }, nil
}

func (a *MyAgent) Process(ctx context.Context, query string) (string, error) {
    // Detect capabilities and match tools
    tools := a.engine.Match(query)

    // Log matched tools
    log.Info("Matched tools", "count", len(tools))
    for _, tool := range tools {
        log.Info("  - " + tool.Name())
    }

    // Get tool schemas for LLM
    schemas := make([]resources.ToolSchema, len(tools))
    for i, tool := range tools {
        schemas[i] = resources.ToolSchema{
            Name:        tool.Name(),
            Description: tool.Description(),
            Category:    tool.Category(),
            Parameters:  tool.Parameters(),
        }
    }

    // Call LLM with filtered tools
    response, err := a.llm.ChatWithTools(ctx, query, schemas)
    if err != nil {
        return "", err
    }

    // Execute tool if LLM requests it
    if response.ToolCall != nil {
        tool, exists := a.engine.Registry().Get(response.ToolCall.Name)
        if !exists {
            return "", fmt.Errorf("tool not found: %s", response.ToolCall.Name)
        }

        result, err := tool.Execute(ctx, response.ToolCall.Params)
        if err != nil {
            return "", err
        }

        return fmt.Sprintf("Tool result: %v", result.Data), nil
    }

    return response.Content, nil
}
```

## Benefits

### 1. Reduced Tool Count

Instead of presenting all 20+ tools to the LLM, ACE filters to only 2-4 relevant tools:

```
Total tools: 20
LLM sees: 2-4 (based on query)
```

### 2. Improved Tool Selection

By filtering based on capabilities, the LLM receives more focused and relevant tools:

```
Query: "calculate 1 + 1"
Without ACE: LLM sees 20 tools, might pick wrong one
With ACE: LLM sees only calculator, picks correctly
```

### 3. Easier Tool Extension

Adding new tools is simple - just declare their capabilities:

```go
func NewWeatherTool() *WeatherTool {
    return &WeatherTool{
        BaseTool: resources.NewBaseToolWithCapabilities(
            "weather",
            "Get weather information",
            resources.CategoryDomain,
            []resources.Capability{resources.CapabilityNetwork, resources.CapabilityKnowledge},
            params,
        ),
    }
}
```

## Capability Detection Algorithm

The capability detection uses keyword matching:

1. **Query Normalization**: Convert query to lowercase
2. **Keyword Matching**: Check for capability keywords in the query
3. **Deduplication**: Remove duplicate capabilities
4. **Return**: List of detected capabilities

```go
query := "fetch data from API and calculate results"
// 1. Normalize: "fetch data from api and calculate results"
// 2. Match keywords:
//    - "fetch" -> CapabilityNetwork
//    - "calculate" -> CapabilityMath
// 3. Deduplicate: [network, math]
// 4. Return: [CapabilityNetwork, CapabilityMath]
```

## Testing

### Unit Tests

```go
func TestCapabilityEngine(t *testing.T) {
    registry := resources.NewRegistry()
    registry.Register(resources.NewCalculator())
    registry.Register(resources.NewHTTPRequest())

    engine := resources.NewCapabilityEngine(registry)

    // Test detection
    caps := engine.Detect("calculate 1 + 1")
    if !contains(caps, resources.CapabilityMath) {
        t.Error("Expected math capability")
    }

    // Test matching
    tools := engine.Match("calculate 1 + 1")
    if len(tools) == 0 {
        t.Error("Expected at least one tool")
    }
}
```

### Integration Tests

```go
func TestAgentWithCapabilityEngine(t *testing.T) {
    agent, err := NewMyAgent("test_agent")
    if err != nil {
        t.Fatal(err)
    }

    response, err := agent.Process(context.Background(), "calculate 1 + 1")
    if err != nil {
        t.Fatal(err)
    }

    if response == "" {
        t.Error("Expected response")
    }
}
```

## Best Practices

1. **Declare Capabilities Clearly**: Each tool should declare all relevant capabilities
2. **Use Granular Capabilities**: Prefer specific capabilities over generic ones
3. **Test Keyword Matching**: Ensure queries correctly detect capabilities
4. **Monitor Tool Selection**: Log which tools are matched for debugging
5. **Update Capability Keywords**: Add relevant keywords to improve detection

## Troubleshooting

### Tools Not Found

If tools are not being matched:

1. Check tool capabilities are declared correctly
2. Verify capability keywords include relevant terms
3. Ensure tools are registered before engine creation
4. Call `engine.Rebuild()` after registering new tools

### Too Many Tools Matched

If too many tools are being returned:

1. Review capability declarations - they may be too broad
2. Add more specific keywords to capability detection
3. Consider using category filtering as a secondary filter

### Capability Detection Fails

If capabilities are not being detected:

1. Check query normalization (case sensitivity)
2. Verify keyword spelling in `capabilityKeywords` map
3. Add more keywords to improve matching

## Example Workflows

### Workflow 1: Simple Calculation

```
Query: "calculate 1 + 1"
↓
Detect: [math]
↓
Filter: [calculator]
↓
LLM sees: 1 tool
↓
Result: 2
```

### Workflow 2: Complex Task

```
Query: "fetch weather data and calculate average temperature"
↓
Detect: [network, math]
↓
Filter: [http_request, calculator]
↓
LLM sees: 2 tools
↓
Execute: http_request → calculator
↓
Result: Average temperature
```

### Workflow 3: Multi-Step Task

```
Query: "read config file, parse JSON, and update knowledge base"
↓
Detect: [file, text, knowledge]
↓
Filter: [file_tools, json_tools, knowledge_add]
↓
LLM sees: 3 tools
↓
Execute: file_tools → json_tools → knowledge_add
↓
Result: Knowledge updated
```

## Performance Considerations

- **Detection**: O(n) where n is the number of keywords
- **Filtering**: O(m) where m is the number of registered tools
- **Matching**: O(n + m) - detection + filtering
- **Rebuild**: O(m) - rebuilds capability map after tool registration

## Future Enhancements

Potential improvements to the capability engine:

1. **Semantic Similarity**: Use embeddings for capability detection instead of keywords
2. **Tool Ranking**: Rank tools by relevance score
3. **Capability Hierarchy**: Support hierarchical capabilities (e.g., math -> algebra, calculus)
4. **Context Awareness**: Consider conversation context for capability detection
5. **Learning**: Learn from tool usage patterns to improve detection

## References

- [CapabilityLayer.md](../../plan/CapabilityLayer.md) - Design specification
- [code_rules.md](../../plan/code_rules.md) - Coding standards
- [AGENT_TOOLS_INTEGRATION.md](AGENT_TOOLS_INTEGRATION.md) - Agent tools integration guide
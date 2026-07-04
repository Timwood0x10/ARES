# tools — Public API for Tool Registration and Execution

External projects can use this package to register and call tools without importing `internal/` packages.

## Quick Start

```go
package main

import (
    "context"
    "fmt"

    "github.com/Timwood0x10/ares/api/tools"
)

func main() {
    // 1. Create registry and register built-in tools
    registry := tools.NewRegistry()

    // 2. Call a built-in tool
    result, err := registry.Execute(context.Background(), "web_search", map[string]any{
        "query": "golang concurrency best practices",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Data)

    // 3. Register your custom tool
    registry.Register(tools.ToolFunc{
        ToolName: "my_tool",
        ToolDesc: "My custom tool",
        Fn: func(ctx context.Context, params map[string]any) (any, error) {
            return map[string]any{"result": "ok"}, nil
        },
    })

    // 4. Call custom tool
    result, _ = registry.Execute(context.Background(), "my_tool", map[string]any{})
    fmt.Println(result.Data)
}
```

## Available Built-in Tools

| Name | Description |
|------|-------------|
| `web_search` | SearXNG meta search |
| `http_request` | HTTP client (GET/POST/PUT/DELETE) |
| `web_scraper` | Web page content extraction |
| `regex` | Regex match/extract/replace |
| `json_tools` | JSON parse/transform/validate |

## Custom Tool Interface

```go
type Tool interface {
    Name() string
    Description() string
    Capabilities() []string    // Optional: declare capabilities for planner discovery
    Execute(ctx context.Context, params map[string]any) (Result, error)
}

type Result struct {
    Success bool `json:"success"`
    Data    any  `json:"data,omitempty"`
}
```

Custom tools can optionally declare capabilities via `Capabilities()`. When
capabilities are declared, the planner can discover the tool dynamically
without requiring a static capability mapping. Return nil if your tool
does not participate in capability-based discovery.

## Using with the Capability Planner

The `Registry` satisfies the planner's `ToolProvider` interface via
`ListToolNames()` and `GetToolCapabilities()`. Connect it with a thin adapter:

```go
import (
    "github.com/Timwood0x10/ares/api/tools"
    "github.com/Timwood0x10/ares/internal/tools/planner"
)

reg := tools.NewRegistry()

// Adapter: api/tools.Registry → planner.ToolProvider
adapter := &struct {
    *tools.Registry
    ListTools func() []string
}{Registry: reg, ListTools: reg.ListToolNames}

resolver, _ := planner.NewToolResolver(adapter)
```

The planner provides:
- **Intent analysis** — parse user request into structured intent
- **Capability decomposition** — break intent into capability requirements
- **Tool resolution** — find tools matching each requirement
- **Evidence-aware scoring** — rank tools by success rate + latency
- **Execution planning** — single-step or multi-step DAG
- **Planner fallback** — auto-select tool when LLM provides no name

# mcp — Public API for MCP Integration

External projects can use this package to connect to MCP servers and use their tools.

## Quick Start

```go
package main

import (
    "context"
    "fmt"

    "github.com/Timwood0x10/ares/api/mcp"
    "github.com/Timwood0x10/ares/api/tools"
)

func main() {
    ctx := context.Background()

    // 1. Connect to MCP server via stdio
    client, err := mcp.ConnectStdio(ctx, "codegraph", "codegraph", []string{"serve", "--mcp"})
    if err != nil {
        panic(err)
    }
    defer client.Close()

    // 2. List tools
    toolList, _ := client.ListTools(ctx)
    for _, t := range toolList {
        fmt.Printf("  %s: %s\n", t.Name, t.Description)
    }

    // 3. Call a tool directly
    result, _ := client.CallTool(ctx, "codegraph_files", map[string]any{"query": "*.go"})
    fmt.Println(result.Content)

    // 4. Register all MCP tools into a tools.Registry
    registry := tools.NewRegistry()
    tools.RegisterBuiltinTools(registry)
    client.RegisterTools(registry) // adds "mcp.codegraph.*" tools

    // Now use registry to call any tool
    registry.Execute(ctx, "mcp.codegraph.codegraph_files", map[string]any{"query": "*.go"})
}
```

## API

| Function | Description |
|----------|-------------|
| `ConnectStdio(ctx, name, command, args)` | Connect via stdio transport |
| `ConnectSSE(ctx, name, url)` | Connect via SSE transport |
| `client.ListTools(ctx)` | List available tools |
| `client.CallTool(ctx, name, args)` | Call a tool |
| `client.RegisterTools(registry)` | Register tools into a tools.Registry |
| `client.Close()` | Close connection |

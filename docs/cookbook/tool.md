# Tool Calling Cookbook

Register custom tools and let the LLM call them automatically.

## Code

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	rt := sdk.MustNew(sdk.WithOllama("llama3.2"))
	defer rt.Close()

	// Register a custom tool.
	rt.ToolRegistry().Register(tools.ToolFunc{
		ToolName: "weather",
		ToolDesc: "Get weather for a city",
		Fn: func(_ context.Context, p map[string]any) (any, error) {
			city, _ := p["city"].(string)
			return fmt.Sprintf(`{"city":%q,"temp":22}`, city), nil
		},
	})

	agent := rt.NewAgent("assistant",
		sdk.WithInstruction("Use tools when needed."),
	)
	result, err := agent.Run(ctx, "What's the weather in Tokyo?")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(result.Output)
}
```

## Key Points

- Tools implement `tools.Tool` interface: `Name()`, `Description()`, `Execute()`.
- `ToolFunc` wraps a function as a tool — quickest way to get started.
- The LLM decides when to call tools via ReAct loop.
- Built-in tools (calculator, web_search, file_tools) are auto-registered.
